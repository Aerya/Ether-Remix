package resolver

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// TitleIDVerdict is the result of independently verifying an indexer's
// conflicting reported title identifiers against upstream external-ID
// mappings (TMDB / TVDB / IMDb).
type TitleIDVerdict uint8

const (
	// TitleIDConflict means the reported IDs belong to a different title or
	// could not be verified. Keep rejecting the release.
	TitleIDConflict TitleIDVerdict = iota
	// TitleIDVerified means upstream metadata confirms the reported IDs map
	// to the expected title (e.g. an alternate/episode TVDB or IMDb ID, or
	// updated external IDs for the expected TMDB entry).
	TitleIDVerified
	// TitleIDWrongMedia means the reported IDs resolve exclusively to the
	// opposite media type (for example, a movie IMDb/TMDB ID mis-tagged by an
	// indexer onto a TV episode release).
	TitleIDWrongMedia
)

const (
	titleIDTTL           = 24 * time.Hour
	titleIDLookupTimeout = 3 * time.Second
	maxTitleIDLookups    = 4
	maxTitleIDEntries    = 4096
)

type titleIDCacheKey struct {
	kind         core.MediaType
	reportedTMDB int64
	reportedTVDB int64
	reportedIMDB string
	expectedTMDB int64
	expectedTVDB int64
	expectedIMDB string
}

type titleIDCacheEntry struct {
	verdict TitleIDVerdict
	at      time.Time
}

type titleIDVerifications struct {
	mu   sync.Mutex
	seen map[titleIDCacheKey]titleIDCacheEntry
}

func makeTitleIDCacheKey(kind core.MediaType, reported, expected core.IDs) titleIDCacheKey {
	return titleIDCacheKey{
		kind:         kind,
		reportedTMDB: reported.TMDB,
		reportedTVDB: reported.TVDB,
		reportedIMDB: strings.ToLower(strings.TrimSpace(reported.IMDB)),
		expectedTMDB: expected.TMDB,
		expectedTVDB: expected.TVDB,
		expectedIMDB: strings.ToLower(strings.TrimSpace(expected.IMDB)),
	}
}

func titleIDMediaKind(req core.SearchRequest) core.MediaType {
	if req.Type == core.TypeEpisode {
		return core.TypeShow
	}
	return core.TypeMovie
}

// prepareTitleIDVerifications verifies conflicting indexer title IDs only for
// releases that already pass every other identity gate (title/alias, year,
// episode/air-date). Unrelated shows never trigger lookups.
func (s *Service) prepareTitleIDVerifications(ctx context.Context, t *core.Title, sreq core.SearchRequest, catalogLang string, results []core.Release) {
	if s.verifyTitleIDs == nil || t == nil || sreq.IDs == (core.IDs{}) {
		return
	}
	kind := titleIDMediaKind(sreq)
	acceptYear := func(y int) bool { return s.seriesYearAccepted(t.ShowKey, y) }
	seenKeys := map[titleIDCacheKey]core.IDs{}
	var order []titleIDCacheKey
	for _, rel := range results {
		reported := rel.ReportedTitleIDs()
		_, conflict := releaseparse.CompareTitleIDs(reported, sreq.IDs)
		if !conflict {
			continue
		}
		stripped := rel
		stripped.TitleIDs = core.IDs{}
		stripped.TitleIDsEchoed = false
		if rejectIdentityMetadata(stripped, t, sreq, catalogLang, acceptYear, false) != "" {
			continue
		}
		key := makeTitleIDCacheKey(kind, reported, sreq.IDs)
		if _, dup := seenKeys[key]; !dup {
			seenKeys[key] = reported
			order = append(order, key)
		}
	}
	if len(order) == 0 {
		return
	}
	now := s.now()
	lookups := 0
	for _, key := range order {
		if _, ok := s.cachedTitleIDVerdict(key, now); ok {
			continue
		}
		if lookups >= maxTitleIDLookups {
			break
		}
		lookups++
		lctx, cancel := context.WithTimeout(ctx, titleIDLookupTimeout)
		verdict, err := s.verifyTitleIDs(lctx, kind, seenKeys[key], sreq.IDs)
		cancel()
		if err != nil {
			s.log.Debug("title ID verification failed; keeping ID mismatch rejection", "reported", seenKeys[key], "expected", sreq.IDs, "err", err)
			continue
		}
		s.storeTitleIDVerdict(key, verdict, now)
	}
}

func (s *Service) cachedTitleIDVerdict(key titleIDCacheKey, now time.Time) (TitleIDVerdict, bool) {
	s.titleIDs.mu.Lock()
	defer s.titleIDs.mu.Unlock()
	e, ok := s.titleIDs.seen[key]
	if !ok || now.Sub(e.at) >= titleIDTTL {
		return TitleIDConflict, false
	}
	return e.verdict, true
}

func (s *Service) storeTitleIDVerdict(key titleIDCacheKey, verdict TitleIDVerdict, now time.Time) {
	s.titleIDs.mu.Lock()
	defer s.titleIDs.mu.Unlock()
	if s.titleIDs.seen == nil {
		s.titleIDs.seen = map[titleIDCacheKey]titleIDCacheEntry{}
	}
	if len(s.titleIDs.seen) >= maxTitleIDEntries {
		for k, old := range s.titleIDs.seen {
			if now.Sub(old.at) >= titleIDTTL {
				delete(s.titleIDs.seen, k)
			}
		}
		if len(s.titleIDs.seen) >= maxTitleIDEntries {
			clear(s.titleIDs.seen)
		}
	}
	s.titleIDs.seen[key] = titleIDCacheEntry{verdict: verdict, at: now}
}

// applyVerifiedTitleIDs updates rel's reported IDs when independent metadata
// verification has already confirmed the mapping (or proved a TV episode was
// mis-tagged with a movie-only ID while strongly matching on title + episode).
func (s *Service) applyVerifiedTitleIDs(rel core.Release, t *core.Title, sreq core.SearchRequest) core.Release {
	if s.verifyTitleIDs == nil || sreq.IDs == (core.IDs{}) {
		return rel
	}
	reported := rel.ReportedTitleIDs()
	_, conflict := releaseparse.CompareTitleIDs(reported, sreq.IDs)
	if !conflict {
		return rel
	}
	verdict, ok := s.cachedTitleIDVerdict(makeTitleIDCacheKey(titleIDMediaKind(sreq), reported, sreq.IDs), s.now())
	if !ok {
		return rel
	}
	switch verdict {
	case TitleIDVerified:
		rel.TitleIDs = sreq.IDs
		rel.TitleIDsEchoed = false
	case TitleIDWrongMedia:
		if sreq.Type == core.TypeEpisode && strongEpisodeCorroboration(rel, t, sreq) {
			rel.TitleIDs = core.IDs{}
			rel.TitleIDsEchoed = false
		}
	}
	return rel
}

// strongEpisodeCorroboration requires unambiguous TV episode proof beyond a
// bare colliding title + S01E01 before ignoring a mis-tagged movie-only ID.
func strongEpisodeCorroboration(rel core.Release, t *core.Title, sreq core.SearchRequest) bool {
	if sreq.Type != core.TypeEpisode || !episodeMatchesRequest(rel.Info, sreq) {
		return false
	}
	if releaseparse.EpisodeMatchesAirDate(rel.Info, sreq.Released) {
		return true
	}
	if year := releaseparse.EpisodeSeriesYear(rel.Name, rel.Info.Year); year > 0 && sreq.Year > 0 && releaseparse.SeriesYearMatches(year, sreq.Year) {
		return true
	}
	if matchesDistinctNonPrimaryTitle(rel.Info, t, sreq) && (sreq.Episode >= 2 || sreq.Season >= 2 || rel.Info.AirDate != "") {
		return true
	}
	return false
}

func matchesDistinctNonPrimaryTitle(info core.ReleaseInfo, t *core.Title, sreq core.SearchRequest) bool {
	if releaseparse.TitleMatches(info, sreq.Title) {
		return false
	}
	if sreq.OriginalTitle != "" && releaseparse.TitleMatches(info, sreq.OriginalTitle) {
		return true
	}
	if t != nil && t.ShowOriginalTitle != "" && releaseparse.TitleMatches(info, t.ShowOriginalTitle) {
		return true
	}
	if sreq.EnglishTitle != "" && releaseparse.TitleMatches(info, sreq.EnglishTitle) {
		return true
	}
	if t != nil && t.ShowEnglishTitle != "" && releaseparse.TitleMatches(info, t.ShowEnglishTitle) {
		return true
	}
	if matchesAlternativeTitle(info, sreq.AlternativeTitles) {
		return true
	}
	return t != nil && matchesAlternativeTitle(info, t.ShowAlternativeTitles)
}
