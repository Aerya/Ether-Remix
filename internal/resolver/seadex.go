package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/animemap"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/httpclient"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/safefetch"
	"github.com/braedonsaunders/ether/internal/store"
)

// DefaultSeaDexURL is the public SeaDex PocketBase origin.
const DefaultSeaDexURL = "https://releases.moe"

// Built-in trace format IDs for SeaDex curation boosts. Prefixed with
// core.BuiltinFormatPrefix ("ether:") so core.CountsTowardMinimum is false.
const (
	FormatSeaDexBest = core.BuiltinFormatPrefix + "seadex-best"
	FormatSeaDexAlt  = core.BuiltinFormatPrefix + "seadex-alt"
)

const (
	defaultSeaDexTTL     = 24 * time.Hour
	seaDexFailureBackoff = time.Hour
	seaDexRetention      = 60 * 24 * time.Hour
	seaDexRequestTimeout = 15 * time.Second
	seaDexMaxResponse    = 2 << 20 // 2 MiB bound on untrusted API responses
	seaDexDBMissTTL      = time.Minute
	maxSeaDexMem         = 4096
	maxSeaDexKeys        = 8192
	maxSeaDexQueue       = 64

	// Boost magnitudes sit above defaultSourceScoreMargin (25) between tiers so
	// source routing never swaps Alt ahead of Best or uncurated ahead of Alt,
	// while Best hash (+220) and Best group (+200) stay within 20 points so
	// Usenet and Torrent releases of the same SeaDex Best group remain eligible
	// for fastest-source routing.
	seaDexBestHashScore  = 220
	seaDexBestGroupScore = 200
	seaDexAltHashScore   = 110
	seaDexAltGroupScore  = 100
)

func init() {
	store.Register("resolver", 8, `
CREATE TABLE resolver_seadex (
  anilist_id INTEGER PRIMARY KEY,
  payload TEXT NOT NULL,
  fetched_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
CREATE TABLE resolver_seadex_keys (
  lookup_key TEXT PRIMARY KEY,
  anilist_id INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);`)
}

// AnimeIDs maps an anime search request to a database id (such as AniList)
// for SeaDex curation lookups. Implemented by *animemap.Service.
type AnimeIDs interface {
	Lookup(ctx context.Context, req core.SearchRequest, db string) (id int64, episode int, ok bool, err error)
	LookupCached(req core.SearchRequest, db string) (id int64, episode int, ok bool)
}

type seaDexPayload struct {
	BestHashes []string `json:"bestHashes,omitempty"`
	AltHashes  []string `json:"altHashes,omitempty"`
	BestGroups []string `json:"bestGroups,omitempty"`
	AltGroups  []string `json:"altGroups,omitempty"`
}

type seaDexEntry struct {
	alID       int64
	bestHashes map[string]struct{}
	altHashes  map[string]struct{}
	bestGroups map[string]struct{}
	altGroups  map[string]struct{}
	fetchedAt  time.Time
	expiresAt  time.Time
}

type seaDexKeyCache struct {
	alID      int64 // 0 when animemap confirmed no AniList mapping exists
	updatedAt time.Time
}

type seaDexJob struct {
	dedupKey string
	reqKey   string
	sreq     core.SearchRequest
	alID     int64
}

func (s *Service) initSeaDex(opts Options) {
	s.animeIDs = opts.AnimeIDs
	s.seaDexURL = strings.TrimRight(strings.TrimSpace(opts.SeaDexURL), "/")
	if s.seaDexURL == "" {
		s.seaDexURL = DefaultSeaDexURL
	}
	s.seaDexClient = opts.SeaDexClient
	if s.seaDexClient == nil {
		s.seaDexClient = (&safefetch.Guard{}).Client(&http.Client{Transport: egress.Transport(egress.CapMetadata)})
	}
	s.seaDexTTL = opts.SeaDexTTL
	if s.seaDexTTL <= 0 {
		s.seaDexTTL = defaultSeaDexTTL
	}
	s.seaDexMem = map[int64]*seaDexEntry{}
	s.seaDexDBMiss = map[int64]time.Time{}
	s.seaDexKeys = map[string]seaDexKeyCache{}
	s.seaDexKeyMiss = map[string]time.Time{}
	s.seaDexFail = map[int64]time.Time{}
}

func seaDexFetchAllowed(ctx context.Context, req core.ResolveRequest) bool {
	if req.Client.BroadcastBitrateKbps > 0 {
		return false
	}
	if core.IsBackgroundCheck(ctx) || core.IsUnattendedSearch(ctx) || core.IsSpeculative(ctx) {
		return false
	}
	switch core.IndexerPurpose(ctx) {
	case core.IndexerPurposeAvailability, core.IndexerPurposeFeed, core.IndexerPurposeWarmup:
		return false
	}
	return true
}

func seaDexLookupKey(sreq core.SearchRequest) string {
	switch sreq.Type {
	case core.TypeMovie:
		imdb := strings.ToLower(strings.TrimSpace(sreq.IDs.IMDB))
		if sreq.IDs.TMDB <= 0 && imdb == "" {
			return ""
		}
		return fmt.Sprintf("m:%d:%s", sreq.IDs.TMDB, imdb)
	case core.TypeEpisode:
		if sreq.Season <= 0 || sreq.Episode <= 0 || (sreq.IDs.TMDB <= 0 && sreq.IDs.TVDB <= 0) {
			return ""
		}
		return fmt.Sprintf("e:%d:%d:%d:%d:%d", sreq.IDs.TMDB, sreq.IDs.TVDB, sreq.Season, sreq.Episode, sreq.AbsoluteEpisode)
	}
	return ""
}

// seaDexForRank reads any cached SeaDex entry for sreq without performing
// network I/O. When allowFetch is true (an actual play or detail open) and the
// entry is missing or expired, it schedules a background fetch/refresh.
func (s *Service) seaDexForRank(ctx context.Context, sreq core.SearchRequest, allowFetch bool) *seaDexEntry {
	if s == nil || s.animeIDs == nil || !sreq.Anime {
		return nil
	}
	reqKey := seaDexLookupKey(sreq)
	if reqKey == "" {
		return nil
	}
	now := s.now()
	alID, known := s.cachedSeaDexAniListID(ctx, reqKey, sreq, now)
	if !known {
		if allowFetch {
			s.enqueueSeaDexJob(seaDexJob{dedupKey: "k:" + reqKey, reqKey: reqKey, sreq: sreq})
		}
		return nil
	}
	if alID <= 0 {
		// Known unmapped anime title; recheck only after TTL expires.
		return nil
	}
	entry := s.cachedSeaDexEntry(ctx, alID, now)
	if entry == nil {
		if allowFetch && !s.seaDexBackedOff(alID, now) {
			s.enqueueSeaDexJob(seaDexJob{dedupKey: "a:" + strconv.FormatInt(alID, 10), reqKey: reqKey, sreq: sreq, alID: alID})
		}
		return nil
	}
	if !now.Before(entry.expiresAt) && allowFetch && !s.seaDexBackedOff(alID, now) {
		s.enqueueSeaDexJob(seaDexJob{dedupKey: "a:" + strconv.FormatInt(alID, 10), reqKey: reqKey, sreq: sreq, alID: alID})
	}
	return entry
}

// warmSeaDexForOpen schedules a background SeaDex lookup when a viewer opens
// an anime title's detail page (RefillSearchCache with unattended == false).
func (s *Service) warmSeaDexForOpen(title *core.Title, req core.SearchRequest) {
	if s == nil || s.animeIDs == nil || title == nil || !req.Anime {
		return
	}
	if title.Type == core.TypeShow || title.Type == core.TypeSeason {
		req.Type = core.TypeEpisode
		req.IDs = title.IDs
		if req.Season <= 0 {
			req.Season = max(title.Season, 1)
		}
		if req.Episode <= 0 {
			req.Episode = 1
		}
	}
	_ = s.seaDexForRank(s.bgCtx, req, true)
}

func (s *Service) cachedSeaDexAniListID(ctx context.Context, reqKey string, sreq core.SearchRequest, now time.Time) (int64, bool) {
	if id, _, ok := s.animeIDs.LookupCached(sreq, animemap.AniList); ok && id > 0 {
		return id, true
	}
	s.mu.RLock()
	kc, ok := s.seaDexKeys[reqKey]
	missedAt := s.seaDexKeyMiss[reqKey]
	s.mu.RUnlock()
	if ok {
		if kc.alID > 0 {
			return kc.alID, true
		}
		if now.Sub(kc.updatedAt) < s.seaDexTTL {
			return 0, true
		}
		return 0, false
	}
	if s.db == nil || (!missedAt.IsZero() && now.Sub(missedAt) < seaDexDBMissTTL) {
		return 0, false
	}
	var alID, updatedUnix int64
	if err := s.db.QueryRowContext(ctx, `SELECT anilist_id, updated_at FROM resolver_seadex_keys WHERE lookup_key=?`, reqKey).Scan(&alID, &updatedUnix); err != nil {
		s.mu.Lock()
		boundMap(s.seaDexKeyMiss, maxSeaDexKeys, func(t time.Time) bool { return now.Sub(t) >= seaDexDBMissTTL })
		s.seaDexKeyMiss[reqKey] = now
		s.mu.Unlock()
		return 0, false
	}
	updatedAt := time.Unix(updatedUnix, 0)
	s.mu.Lock()
	boundMap(s.seaDexKeys, maxSeaDexKeys, func(v seaDexKeyCache) bool { return now.Sub(v.updatedAt) >= seaDexRetention })
	s.seaDexKeys[reqKey] = seaDexKeyCache{alID: alID, updatedAt: updatedAt}
	delete(s.seaDexKeyMiss, reqKey)
	s.mu.Unlock()
	if alID <= 0 && now.Sub(updatedAt) >= s.seaDexTTL {
		return 0, false
	}
	return alID, true
}

func (s *Service) cachedSeaDexEntry(ctx context.Context, alID int64, now time.Time) *seaDexEntry {
	s.mu.RLock()
	entry, ok := s.seaDexMem[alID]
	missedAt := s.seaDexDBMiss[alID]
	s.mu.RUnlock()
	if ok {
		return entry
	}
	if s.db == nil || (!missedAt.IsZero() && now.Sub(missedAt) < seaDexDBMissTTL) {
		return nil
	}
	var raw string
	var fetchedUnix, expiresUnix int64
	if err := s.db.QueryRowContext(ctx, `SELECT payload, fetched_at, expires_at FROM resolver_seadex WHERE anilist_id=?`, alID).Scan(&raw, &fetchedUnix, &expiresUnix); err != nil {
		s.mu.Lock()
		boundMap(s.seaDexDBMiss, maxSeaDexMem, func(t time.Time) bool { return now.Sub(t) >= seaDexDBMissTTL })
		s.seaDexDBMiss[alID] = now
		s.mu.Unlock()
		return nil
	}
	var p seaDexPayload
	if json.Unmarshal([]byte(raw), &p) != nil {
		return nil
	}
	entry = buildSeaDexEntry(alID, p, time.Unix(fetchedUnix, 0), time.Unix(expiresUnix, 0))
	s.mu.Lock()
	boundMap(s.seaDexMem, maxSeaDexMem, func(e *seaDexEntry) bool { return e == nil || !now.Before(e.expiresAt) })
	s.seaDexMem[alID] = entry
	delete(s.seaDexDBMiss, alID)
	s.mu.Unlock()
	return entry
}

func (s *Service) seaDexBackedOff(alID int64, now time.Time) bool {
	s.mu.RLock()
	failedAt := s.seaDexFail[alID]
	s.mu.RUnlock()
	return !failedAt.IsZero() && now.Sub(failedAt) < seaDexFailureBackoff
}

func (s *Service) enqueueSeaDexJob(job seaDexJob) {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	if s.bgClosed {
		return
	}
	if s.seaDexPending == nil {
		s.seaDexPending = map[string]bool{}
	}
	if s.seaDexPending[job.dedupKey] || len(s.seaDexQueue) >= maxSeaDexQueue {
		return
	}
	s.seaDexPending[job.dedupKey] = true
	s.seaDexQueue = append(s.seaDexQueue, job)
	if s.seaDexRunning {
		return
	}
	s.seaDexRunning = true
	s.seaDexIdle = make(chan struct{})
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		for {
			s.bgMu.Lock()
			if s.bgClosed || len(s.seaDexQueue) == 0 {
				s.seaDexQueue = nil
				clear(s.seaDexPending)
				s.seaDexRunning = false
				close(s.seaDexIdle)
				s.bgMu.Unlock()
				return
			}
			next := s.seaDexQueue[0]
			s.seaDexQueue[0] = seaDexJob{}
			s.seaDexQueue = s.seaDexQueue[1:]
			s.bgMu.Unlock()

			s.runSeaDexJob(next)

			s.bgMu.Lock()
			delete(s.seaDexPending, next.dedupKey)
			s.bgMu.Unlock()
		}
	}()
}

// flushSeaDex waits for queued SeaDex background work to finish in tests.
func (s *Service) flushSeaDex(ctx context.Context) error {
	s.bgMu.Lock()
	idle := s.seaDexIdle
	s.bgMu.Unlock()
	if idle == nil {
		return nil
	}
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) runSeaDexJob(job seaDexJob) {
	if s.bgCtx.Err() != nil {
		return
	}
	alID := job.alID
	now := s.now()
	if alID <= 0 {
		id, _, ok, err := s.animeIDs.Lookup(s.bgCtx, job.sreq, animemap.AniList)
		if err != nil {
			return
		}
		now = s.now()
		if !ok || id <= 0 {
			s.saveSeaDexKey(s.bgCtx, job.reqKey, 0, now)
			return
		}
		alID = id
		s.saveSeaDexKey(s.bgCtx, job.reqKey, alID, now)
	} else if job.reqKey != "" {
		s.saveSeaDexKey(s.bgCtx, job.reqKey, alID, now)
	}

	// Re-check cache freshness in case an earlier job already populated alID.
	if existing := s.cachedSeaDexEntry(s.bgCtx, alID, now); existing != nil && now.Before(existing.expiresAt) {
		return
	}
	if s.seaDexBackedOff(alID, now) {
		return
	}

	payload, err := s.fetchSeaDex(s.bgCtx, alID)
	fetchedAt := s.now()
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.mu.Lock()
			boundMap(s.seaDexFail, maxSeaDexMem, func(t time.Time) bool { return fetchedAt.Sub(t) >= seaDexFailureBackoff })
			s.seaDexFail[alID] = fetchedAt
			s.mu.Unlock()
			s.log.Debug("seadex lookup failed", "anilist_id", alID, "err", err)
		}
		return
	}

	expiresAt := fetchedAt.Add(s.seaDexTTL)
	entry := buildSeaDexEntry(alID, payload, fetchedAt, expiresAt)
	s.mu.Lock()
	boundMap(s.seaDexMem, maxSeaDexMem, func(e *seaDexEntry) bool { return e == nil || !fetchedAt.Before(e.expiresAt) })
	s.seaDexMem[alID] = entry
	delete(s.seaDexDBMiss, alID)
	delete(s.seaDexFail, alID)
	s.mu.Unlock()

	if s.db != nil {
		if raw, merr := json.Marshal(payload); merr == nil {
			wctx, cancel := context.WithTimeout(s.bgCtx, 2*time.Second)
			_, dbErr := s.db.ExecContext(wctx,
				`INSERT INTO resolver_seadex(anilist_id, payload, fetched_at, expires_at)
				 VALUES (?,?,?,?)
				 ON CONFLICT(anilist_id) DO UPDATE SET
				   payload=excluded.payload,
				   fetched_at=excluded.fetched_at,
				   expires_at=excluded.expires_at`,
				alID, string(raw), fetchedAt.Unix(), expiresAt.Unix())
			cancel()
			if dbErr == nil {
				s.notify("seadex:" + strconv.FormatInt(alID, 10))
			}
		}
	}

	// Drop short-lived detail variant snapshots so the next detail read sees
	// the newly fetched SeaDex ranking immediately.
	s.bgMu.Lock()
	if len(s.detailVariants) > 0 {
		clear(s.detailVariants)
	}
	s.bgMu.Unlock()
}

func (s *Service) saveSeaDexKey(ctx context.Context, reqKey string, alID int64, now time.Time) {
	if reqKey == "" {
		return
	}
	s.mu.Lock()
	boundMap(s.seaDexKeys, maxSeaDexKeys, func(v seaDexKeyCache) bool { return now.Sub(v.updatedAt) >= seaDexRetention })
	s.seaDexKeys[reqKey] = seaDexKeyCache{alID: alID, updatedAt: now}
	delete(s.seaDexKeyMiss, reqKey)
	s.mu.Unlock()
	if s.db == nil {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, _ = s.db.ExecContext(wctx,
		`INSERT INTO resolver_seadex_keys(lookup_key, anilist_id, updated_at)
		 VALUES (?,?,?)
		 ON CONFLICT(lookup_key) DO UPDATE SET
		   anilist_id=excluded.anilist_id,
		   updated_at=excluded.updated_at`,
		reqKey, alID, now.Unix())
}

type seaDexRecordsResponse struct {
	Items []struct {
		AlID   int64 `json:"alID"`
		Expand struct {
			Trs []struct {
				InfoHash     string `json:"infoHash"`
				ReleaseGroup string `json:"releaseGroup"`
				IsBest       bool   `json:"isBest"`
			} `json:"trs"`
		} `json:"expand"`
	} `json:"items"`
}

func (s *Service) fetchSeaDex(parent context.Context, alID int64) (seaDexPayload, error) {
	ctx, cancel := context.WithTimeout(parent, seaDexRequestTimeout)
	defer cancel()

	q := url.Values{}
	q.Set("perPage", "50")
	q.Set("filter", fmt.Sprintf("(alID=%d)", alID))
	q.Set("expand", "trs")
	endpoint := s.seaDexURL + "/api/collections/entries/records?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return seaDexPayload{}, err
	}
	req.Header.Set("User-Agent", httpclient.UserAgent())
	req.Header.Set("Accept", "application/json")

	resp, err := s.seaDexClient.Do(req)
	if err != nil {
		return seaDexPayload{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return seaDexPayload{}, fmt.Errorf("seadex HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > seaDexMaxResponse {
		return seaDexPayload{}, fmt.Errorf("seadex response %d bytes exceeds limit", resp.ContentLength)
	}
	lr := &io.LimitedReader{R: resp.Body, N: seaDexMaxResponse + 1}
	var decoded seaDexRecordsResponse
	if err := json.NewDecoder(lr).Decode(&decoded); err != nil {
		return seaDexPayload{}, err
	}
	if lr.N <= 0 {
		return seaDexPayload{}, errors.New("seadex response exceeds byte limit")
	}

	bestHashes := map[string]bool{}
	altHashes := map[string]bool{}
	bestGroups := map[string]bool{}
	altGroups := map[string]bool{}

	for _, item := range decoded.Items {
		if item.AlID != 0 && item.AlID != alID {
			continue
		}
		for _, tr := range item.Expand.Trs {
			h := normalizeSeaDexHash(tr.InfoHash)
			g := normalizeSeaDexGroup(tr.ReleaseGroup)
			if tr.IsBest {
				if h != "" {
					bestHashes[h] = true
				}
				if g != "" {
					bestGroups[g] = true
				}
			} else {
				if h != "" {
					altHashes[h] = true
				}
				if g != "" {
					altGroups[g] = true
				}
			}
		}
	}
	for h := range bestHashes {
		delete(altHashes, h)
	}
	for g := range bestGroups {
		delete(altGroups, g)
	}

	return seaDexPayload{
		BestHashes: sortedKeys(bestHashes),
		AltHashes:  sortedKeys(altHashes),
		BestGroups: sortedKeys(bestGroups),
		AltGroups:  sortedKeys(altGroups),
	}, nil
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func buildSeaDexEntry(alID int64, p seaDexPayload, fetchedAt, expiresAt time.Time) *seaDexEntry {
	toSet := func(list []string) map[string]struct{} {
		if len(list) == 0 {
			return nil
		}
		m := make(map[string]struct{}, len(list))
		for _, v := range list {
			m[v] = struct{}{}
		}
		return m
	}
	return &seaDexEntry{
		alID:       alID,
		bestHashes: toSet(p.BestHashes),
		altHashes:  toSet(p.AltHashes),
		bestGroups: toSet(p.BestGroups),
		altGroups:  toSet(p.AltGroups),
		fetchedAt:  fetchedAt,
		expiresAt:  expiresAt,
	}
}

func (e *seaDexEntry) empty() bool {
	return e == nil || (len(e.bestHashes) == 0 && len(e.altHashes) == 0 && len(e.bestGroups) == 0 && len(e.altGroups) == 0)
}

func (e *seaDexEntry) match(rel core.Release) (formatID, name string, points int) {
	if e.empty() {
		return "", "", 0
	}
	if h := releaseHashForSeaDex(rel); h != "" {
		if _, ok := e.bestHashes[h]; ok {
			return FormatSeaDexBest, "SeaDex Best", seaDexBestHashScore
		}
		if _, ok := e.altHashes[h]; ok {
			return FormatSeaDexAlt, "SeaDex Alt", seaDexAltHashScore
		}
	}
	if g := releaseGroupForSeaDex(rel); g != "" {
		if _, ok := e.bestGroups[g]; ok {
			return FormatSeaDexBest, "SeaDex Best", seaDexBestGroupScore
		}
		if _, ok := e.altGroups[g]; ok {
			return FormatSeaDexAlt, "SeaDex Alt", seaDexAltGroupScore
		}
	}
	return "", "", 0
}

func (s *Service) addSeaDexScores(entry *seaDexEntry, cands []candidate, trace *Trace) {
	if entry.empty() {
		return
	}
	for i := range cands {
		if cands[i].keys.addonOrder > 0 {
			continue
		}
		id, name, points := entry.match(cands[i].rel)
		if points == 0 {
			continue
		}
		cands[i].cf += points
		cands[i].rel.Score += float64(points)
		if trace != nil && cands[i].trace >= 0 && cands[i].trace < len(trace.Candidates) {
			ct := &trace.Candidates[cands[i].trace]
			ct.Score = cands[i].rel.Score
			ct.CustomFormats = append(ct.CustomFormats, FormatMatch{ID: id, Name: name, Score: points})
		}
	}
}

func normalizeSeaDexHash(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if len(s) != 40 {
		return ""
	}
	for i := 0; i < 40; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return ""
		}
	}
	return s
}

func normalizeSeaDexGroup(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	s = strings.TrimPrefix(s, "(")
	s = strings.TrimSuffix(s, ")")
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "unknown", "nogrp", "no group", "noname", "none", "?", "-":
		return ""
	}
	return s
}

func releaseHashForSeaDex(rel core.Release) string {
	if h := normalizeSeaDexHash(rel.InfoHash); h != "" {
		return h
	}
	if rel.MagnetURL == "" {
		return ""
	}
	lower := strings.ToLower(rel.MagnetURL)
	idx := strings.Index(lower, "urn:btih:")
	if idx < 0 {
		return ""
	}
	rest := lower[idx+len("urn:btih:"):]
	if amp := strings.IndexByte(rest, '&'); amp >= 0 {
		rest = rest[:amp]
	}
	return normalizeSeaDexHash(rest)
}

func releaseGroupForSeaDex(rel core.Release) string {
	if g := normalizeSeaDexGroup(rel.Info.Group); g != "" {
		return g
	}
	if rel.Name != "" {
		if parsed := normalizeSeaDexGroup(releaseparse.Parse(rel.Name).Group); parsed != "" {
			return parsed
		}
		if g := leadingBracketGroup(rel.Name); g != "" {
			return g
		}
	}
	if rel.FileName != "" {
		base := filepath.Base(rel.FileName)
		if parsed := normalizeSeaDexGroup(releaseparse.Parse(base).Group); parsed != "" {
			return parsed
		}
		if g := leadingBracketGroup(base); g != "" {
			return g
		}
	}
	return ""
}

// leadingBracketGroup extracts a leading "[Group]" tag up to 48 characters
// so joint fansub names longer than releaseparse's 20-char cap still match.
func leadingBracketGroup(raw string) string {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "[") {
		return ""
	}
	end := strings.IndexByte(s, ']')
	if end <= 1 || end > 49 {
		return ""
	}
	return normalizeSeaDexGroup(s[1:end])
}
