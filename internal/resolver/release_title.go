package resolver

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/flight"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

const (
	releaseTitleTTL                 = 24 * time.Hour
	releaseTitleMissTTL             = 10 * time.Minute
	releaseTitleLookupTimeout       = 3 * time.Second
	releaseTitleWarmErrBackoff      = 5 * time.Minute
	defaultReleaseTitleWarmInterval = time.Second
	maxReleaseTitleEntries          = 4096
	maxReleaseTitleLookups          = 4
	maxReleaseTitleWarmQueue        = 64
)

func init() {
	store.Register("resolver", 10, `
CREATE TABLE resolver_release_titles (
  media_type TEXT NOT NULL,
  language TEXT NOT NULL,
  normalized_title TEXT NOT NULL,
  tmdb_id INTEGER NOT NULL DEFAULT 0,
  imdb_id TEXT NOT NULL DEFAULT '',
  tvdb_id INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  PRIMARY KEY (media_type, language, normalized_title)
);
CREATE INDEX resolver_release_titles_expires ON resolver_release_titles(expires_at);
CREATE INDEX resolver_release_titles_updated ON resolver_release_titles(updated_at);`)
}

type releaseTitleKey struct {
	kind            core.MediaType
	language, title string
}

type releaseTitleVerdict struct {
	ids       core.IDs
	expires   time.Time
	persisted bool
}

type releaseTitleCache struct {
	mu       sync.Mutex
	seen     map[releaseTitleKey]releaseTitleVerdict
	warmErr  map[releaseTitleKey]time.Time
	lastWarm time.Time
	flights  flight.Group[releaseTitleKey, core.IDs]
}

type releaseTitleWarmJob struct {
	key         releaseTitleKey
	title       string
	expectedIDs core.IDs
}

type releaseTitleRejections struct {
	playable []core.Release // rejected for title mismatch alone (would pass identity and policy)
	identity []core.Release // rejected on title mismatch while passing all other identity checks
}

func (s *Service) releaseTitleCap() int {
	if s.releaseTitleMaxEntries > 0 {
		return s.releaseTitleMaxEntries
	}
	return maxReleaseTitleEntries
}

func (s *Service) releaseTitleKey(rel core.Release, req core.SearchRequest) releaseTitleKey {
	kind := core.TypeMovie
	if req.Type == core.TypeEpisode {
		kind = core.TypeShow
	}
	return releaseTitleKey{kind: kind, language: s.catalogLanguage(), title: releaseparse.NormalizeTitle(rel.Info.Title)}
}

func (s *Service) cachedReleaseTitleVerdict(key releaseTitleKey) (releaseTitleVerdict, bool) {
	s.releaseTitles.mu.Lock()
	defer s.releaseTitles.mu.Unlock()
	v, ok := s.releaseTitles.seen[key]
	if !ok || !s.now().Before(v.expires) {
		return releaseTitleVerdict{}, false
	}
	return v, true
}

func (s *Service) cachedReleaseTitle(key releaseTitleKey) (core.IDs, bool) {
	v, ok := s.cachedReleaseTitleVerdict(key)
	return v.ids, ok
}

func releaseTitleMatchesExpected(ids, expected core.IDs) bool {
	if ids == (core.IDs{}) || expected == (core.IDs{}) {
		return false
	}
	match, conflict := releaseparse.CompareTitleIDs(ids, expected)
	return match && !conflict
}

func releaseTitleLastResortAllowed(ctx context.Context, req core.ResolveRequest) bool {
	if req.Client.BroadcastBitrateKbps > 0 {
		return false
	}
	if core.IsBackgroundCheck(ctx) || core.IsUnattendedSearch(ctx) || core.IsSpeculative(ctx) || core.SpeculativeIndexerWork(ctx) || core.LocalResponse(ctx) {
		return false
	}
	switch core.IndexerPurpose(ctx) {
	case core.IndexerPurposeAvailability, core.IndexerPurposeFeed, core.IndexerPurposeWarmup, core.IndexerPurposeDetail:
		return false
	}
	return true
}

func releaseTitleWarmAllowed(ctx context.Context, req core.ResolveRequest) bool {
	if req.Client.BroadcastBitrateKbps > 0 {
		return false
	}
	if core.IsBackgroundCheck(ctx) || core.IsUnattendedSearch(ctx) {
		return false
	}
	switch core.IndexerPurpose(ctx) {
	case core.IndexerPurposeAvailability, core.IndexerPurposeFeed, core.IndexerPurposeWarmup:
		return false
	}
	return true
}

func (s *Service) titleMismatchIdentityOnly(rel core.Release, t *core.Title, sreq core.SearchRequest, catalogLang string) bool {
	if releaseparse.IsObfuscated(rel.Info.Title) {
		return false
	}
	acceptYear := func(y int) bool { return t != nil && s.seriesYearAccepted(t.ShowKey, y) }
	return rejectIdentityMetadata(rel, t, sreq, catalogLang, acceptYear, true) == ""
}

func (s *Service) titleMismatchPolicyOK(rel core.Release, t *core.Title, profile core.QualityProfile, rcfg config.Resolver, req core.ResolveRequest, preferred bool) bool {
	videoOff, remoteCap := s.transcodeSettingsFor(req.Client)
	reason := rejectPolicyWithTranscode(rel, t, profile, req.Client, preferred, preferred && rcfg.OfferHigherVersions, videoOff, remoteCap)
	if !videoOff && reason == "video codec unsupported by client" && req.ExactHEVCTranscode && !req.Client.Download && req.Prefer != "" && rel.ID == req.Prefer && strings.EqualFold(rel.Info.VideoCodec, "hevc") {
		caps := req.Client
		caps.VideoCodecs = append(slices.Clone(caps.VideoCodecs), rel.Info.VideoCodec)
		reason = rejectPolicyWithTranscode(rel, t, profile, caps, preferred, preferred && rcfg.OfferHigherVersions, videoOff, remoteCap)
	}
	return reason == ""
}

// prepareReleaseTitles spends a bounded metadata budget only on unmatched
// names. Reported IDs and existing aliases need no lookup. The cache stores
// the provider's answer independently of the requested title's identity.
func (s *Service) prepareReleaseTitles(ctx context.Context, t *core.Title, req core.SearchRequest, results []core.Release) bool {
	return s.prepareReleaseTitlesBound(ctx, t, req, results, false)
}

// prepareReleaseTitlesLastResort runs only when normal ranking found nothing
// playable and candidates were rejected for title mismatch alone. It stops as
// soon as a lookup unlocks a matching title, while keeping the PR #557 bounds
// (at most 4 lookups and 3 s total).
func (s *Service) prepareReleaseTitlesLastResort(ctx context.Context, t *core.Title, req core.SearchRequest, results []core.Release) bool {
	return s.prepareReleaseTitlesBound(ctx, t, req, results, true)
}

func (s *Service) prepareReleaseTitlesBound(ctx context.Context, t *core.Title, req core.SearchRequest, results []core.Release, stopOnMatch bool) bool {
	if s.releaseTitleIDs == nil || t == nil || req.IDs == (core.IDs{}) || (req.Type != core.TypeEpisode && req.Type != core.TypeMovie) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, releaseTitleLookupTimeout)
	defer cancel()
	seen := map[releaseTitleKey]bool{}
	lookups := 0
	matched := false
	for _, rel := range results {
		if ctx.Err() != nil {
			return matched
		}
		// Only the title mismatch gate may be relaxed by this evidence.
		if !strings.HasPrefix(rejectIdentity(rel, t, req, s.catalogLanguage()), "title mismatch") || releaseparse.IsObfuscated(rel.Info.Title) {
			continue
		}
		key := s.releaseTitleKey(rel, req)
		if key.title == "" || seen[key] {
			continue
		}
		seen[key] = true
		if v, ok := s.cachedReleaseTitleVerdict(key); ok {
			if !v.persisted && releaseTitleMatchesExpected(v.ids, req.IDs) {
				s.ensureReleaseTitlePersisted(ctx, key, v.ids)
			}
			continue
		}
		if lookups >= maxReleaseTitleLookups {
			break
		}
		lookups++
		ids, err := s.fetchReleaseTitle(ctx, key, rel.Info.Title, req.IDs)
		if err != nil {
			s.log.Debug("release title metadata lookup failed; keeping title check", "title", rel.Info.Title, "err", err)
			continue
		}
		if releaseTitleMatchesExpected(ids, req.IDs) {
			matched = true
			if stopOnMatch {
				return true
			}
		}
	}
	return matched
}

func (s *Service) fetchReleaseTitle(ctx context.Context, key releaseTitleKey, rawTitle string, expectedIDs core.IDs) (core.IDs, error) {
	ids, err := s.releaseTitles.flights.Do(ctx, key, func(ctx context.Context) (core.IDs, error) {
		if ids, ok := s.cachedReleaseTitle(key); ok {
			return ids, nil
		}
		ids, err := s.releaseTitleIDs(ctx, key.kind, rawTitle)
		if err != nil {
			return core.IDs{}, err
		}
		if err := ctx.Err(); err != nil {
			return core.IDs{}, err
		}
		ttl := releaseTitleTTL
		if ids == (core.IDs{}) {
			ttl = releaseTitleMissTTL
		}
		now := s.now()
		expires := now.Add(ttl)
		accepted := releaseTitleMatchesExpected(ids, expectedIDs)
		s.storeReleaseTitleMem(key, ids, expires, accepted, now)
		if accepted {
			s.persistReleaseTitle(ctx, key, ids, now, expires)
		}
		return ids, nil
	})
	if err != nil {
		return core.IDs{}, err
	}
	if releaseTitleMatchesExpected(ids, expectedIDs) {
		s.ensureReleaseTitlePersisted(ctx, key, ids)
	}
	return ids, nil
}

func (s *Service) storeReleaseTitleMem(key releaseTitleKey, ids core.IDs, expires time.Time, persisted bool, now time.Time) {
	capEntries := s.releaseTitleCap()
	s.releaseTitles.mu.Lock()
	defer s.releaseTitles.mu.Unlock()
	if s.releaseTitles.seen == nil {
		s.releaseTitles.seen = map[releaseTitleKey]releaseTitleVerdict{}
	}
	if _, exists := s.releaseTitles.seen[key]; !exists && len(s.releaseTitles.seen) >= capEntries {
		var oldest releaseTitleKey
		var oldestExpires time.Time
		for k, v := range s.releaseTitles.seen {
			if !now.Before(v.expires) {
				delete(s.releaseTitles.seen, k)
			} else if oldestExpires.IsZero() || v.expires.Before(oldestExpires) {
				oldest, oldestExpires = k, v.expires
			}
		}
		if len(s.releaseTitles.seen) >= capEntries {
			delete(s.releaseTitles.seen, oldest)
		}
	}
	s.releaseTitles.seen[key] = releaseTitleVerdict{ids: ids, expires: expires, persisted: persisted}
	delete(s.releaseTitles.warmErr, key)
}

func (s *Service) ensureReleaseTitlePersisted(ctx context.Context, key releaseTitleKey, ids core.IDs) {
	if s.db == nil || ids == (core.IDs{}) {
		return
	}
	now := s.now()
	var expires time.Time
	s.releaseTitles.mu.Lock()
	v, ok := s.releaseTitles.seen[key]
	if !ok || v.persisted || !now.Before(v.expires) || v.ids != ids {
		s.releaseTitles.mu.Unlock()
		return
	}
	v.persisted = true
	s.releaseTitles.seen[key] = v
	expires = v.expires
	s.releaseTitles.mu.Unlock()
	s.persistReleaseTitle(ctx, key, ids, now, expires)
}

func (s *Service) persistReleaseTitle(ctx context.Context, key releaseTitleKey, ids core.IDs, now, expires time.Time) {
	if s.db == nil || ids == (core.IDs{}) {
		return
	}
	s.persistBookkeeping(ctx, func(ctx context.Context) {
		s.saveReleaseTitleDB(ctx, key, ids, now, expires)
	})
}

func (s *Service) saveReleaseTitleDB(ctx context.Context, key releaseTitleKey, ids core.IDs, now, expires time.Time) {
	if s.db == nil || ids == (core.IDs{}) || key.title == "" {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, err := s.db.ExecContext(wctx,
		`INSERT INTO resolver_release_titles(media_type, language, normalized_title, tmdb_id, imdb_id, tvdb_id, updated_at, expires_at)
		 VALUES (?,?,?,?,?,?,?,?)
		 ON CONFLICT(media_type, language, normalized_title) DO UPDATE SET
		   tmdb_id=excluded.tmdb_id,
		   imdb_id=excluded.imdb_id,
		   tvdb_id=excluded.tvdb_id,
		   updated_at=excluded.updated_at,
		   expires_at=excluded.expires_at`,
		string(key.kind), key.language, key.title, ids.TMDB, ids.IMDB, ids.TVDB, now.Unix(), expires.Unix())
	if err != nil {
		s.log.Debug("resolver release title alias not saved", "title", key.title, "err", err)
		return
	}
	s.trimReleaseTitlesDB(wctx, now, s.releaseTitleCap())
	s.notify("reltitle")
}

func (s *Service) trimReleaseTitlesDB(ctx context.Context, now time.Time, limit int) {
	if s.db == nil || limit <= 0 {
		return
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM resolver_release_titles WHERE expires_at<=?`, now.Unix())
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM resolver_release_titles`).Scan(&count); err != nil || count <= limit {
		return
	}
	excess := count - limit
	rows, err := s.db.QueryContext(ctx,
		`SELECT media_type, language, normalized_title FROM resolver_release_titles ORDER BY expires_at ASC, updated_at ASC LIMIT ?`,
		excess)
	if err != nil {
		return
	}
	var drop []releaseTitleKey
	for rows.Next() {
		var kind, lang, title string
		if rows.Scan(&kind, &lang, &title) == nil {
			drop = append(drop, releaseTitleKey{kind: core.MediaType(kind), language: lang, title: title})
		}
	}
	rows.Close()
	for _, k := range drop {
		_, _ = s.db.ExecContext(ctx,
			`DELETE FROM resolver_release_titles WHERE media_type=? AND language=? AND normalized_title=?`,
			string(k.kind), k.language, k.title)
	}
}

func (s *Service) loadReleaseTitles(ctx context.Context) {
	if s.db == nil {
		return
	}
	now := s.now()
	rows, err := s.db.QueryContext(ctx,
		`SELECT media_type, language, normalized_title, tmdb_id, imdb_id, tvdb_id, expires_at
		 FROM resolver_release_titles
		 WHERE expires_at>?
		 ORDER BY updated_at DESC
		 LIMIT ?`,
		now.Unix(), s.releaseTitleCap())
	if err != nil {
		return
	}
	type row struct {
		key     releaseTitleKey
		verdict releaseTitleVerdict
	}
	var loaded []row
	for rows.Next() {
		var kind, lang, title, imdb string
		var tmdb, tvdb, expiresUnix int64
		if rows.Scan(&kind, &lang, &title, &tmdb, &imdb, &tvdb, &expiresUnix) == nil {
			ids := core.IDs{TMDB: tmdb, IMDB: imdb, TVDB: tvdb}
			if ids == (core.IDs{}) || title == "" {
				continue
			}
			loaded = append(loaded, row{
				key:     releaseTitleKey{kind: core.MediaType(kind), language: lang, title: title},
				verdict: releaseTitleVerdict{ids: ids, expires: time.Unix(expiresUnix, 0), persisted: true},
			})
		}
	}
	rows.Close()
	if len(loaded) == 0 {
		return
	}
	capEntries := s.releaseTitleCap()
	s.releaseTitles.mu.Lock()
	defer s.releaseTitles.mu.Unlock()
	if s.releaseTitles.seen == nil {
		s.releaseTitles.seen = make(map[releaseTitleKey]releaseTitleVerdict, len(loaded))
	}
	for _, r := range loaded {
		if existing, ok := s.releaseTitles.seen[r.key]; ok && existing.expires.After(r.verdict.expires) {
			continue
		}
		if _, exists := s.releaseTitles.seen[r.key]; !exists && len(s.releaseTitles.seen) >= capEntries {
			break
		}
		s.releaseTitles.seen[r.key] = r.verdict
	}
}

func (s *Service) pruneReleaseTitlesMem(now time.Time) {
	s.releaseTitles.mu.Lock()
	defer s.releaseTitles.mu.Unlock()
	for k, v := range s.releaseTitles.seen {
		if !now.Before(v.expires) {
			delete(s.releaseTitles.seen, k)
		}
	}
	for k, at := range s.releaseTitles.warmErr {
		if now.Sub(at) >= releaseTitleWarmErrBackoff {
			delete(s.releaseTitles.warmErr, k)
		}
	}
}

func (s *Service) clearReleaseTitlesMem() {
	s.releaseTitles.mu.Lock()
	defer s.releaseTitles.mu.Unlock()
	clear(s.releaseTitles.seen)
	clear(s.releaseTitles.warmErr)
}

func (s *Service) releaseTitleWarmBackedOff(key releaseTitleKey) bool {
	now := s.now()
	s.releaseTitles.mu.Lock()
	defer s.releaseTitles.mu.Unlock()
	at, ok := s.releaseTitles.warmErr[key]
	if !ok {
		return false
	}
	if now.Sub(at) >= releaseTitleWarmErrBackoff {
		delete(s.releaseTitles.warmErr, key)
		return false
	}
	return true
}

func (s *Service) noteReleaseTitleWarmError(key releaseTitleKey) {
	now := s.now()
	s.releaseTitles.mu.Lock()
	defer s.releaseTitles.mu.Unlock()
	if s.releaseTitles.warmErr == nil {
		s.releaseTitles.warmErr = map[releaseTitleKey]time.Time{}
	}
	boundMap(s.releaseTitles.warmErr, s.releaseTitleCap(), func(t time.Time) bool {
		return now.Sub(t) >= releaseTitleWarmErrBackoff
	})
	s.releaseTitles.warmErr[key] = now
}

// warmReleaseTitlesForSearch queues async verification for releases from a
// background/detail search that fail identity only on title mismatch.
func (s *Service) warmReleaseTitlesForSearch(t *core.Title, req core.SearchRequest, results []core.Release) {
	if s.releaseTitleIDs == nil || t == nil || req.IDs == (core.IDs{}) || (req.Type != core.TypeEpisode && req.Type != core.TypeMovie) {
		return
	}
	lang := s.catalogLanguage()
	var unmatched []core.Release
	for _, rel := range results {
		if !strings.HasPrefix(rejectIdentity(rel, t, req, lang), "title mismatch") {
			continue
		}
		if s.titleMismatchIdentityOnly(rel, t, req, lang) {
			unmatched = append(unmatched, rel)
		}
	}
	if len(unmatched) > 0 {
		s.queueReleaseTitleWarm(t, req, unmatched)
	}
}

// queueReleaseTitleWarm schedules deduplicated, rate-limited background
// verification for releases rejected only on title mismatch so future searches
// can accept verified aliases from the local durable table without TMDB waits.
func (s *Service) queueReleaseTitleWarm(t *core.Title, req core.SearchRequest, results []core.Release) {
	if s.releaseTitleIDs == nil || t == nil || req.IDs == (core.IDs{}) || (req.Type != core.TypeEpisode && req.Type != core.TypeMovie) {
		return
	}
	seen := map[releaseTitleKey]bool{}
	queued := 0
	for _, rel := range results {
		if releaseparse.IsObfuscated(rel.Info.Title) {
			continue
		}
		key := s.releaseTitleKey(rel, req)
		if key.title == "" || seen[key] {
			continue
		}
		seen[key] = true
		if v, ok := s.cachedReleaseTitleVerdict(key); ok {
			if !v.persisted && releaseTitleMatchesExpected(v.ids, req.IDs) {
				s.ensureReleaseTitlePersisted(s.bgCtx, key, v.ids)
			}
			continue
		}
		if s.releaseTitleWarmBackedOff(key) || s.releaseTitles.flights.Has(key) {
			continue
		}
		if s.enqueueReleaseTitleWarm(releaseTitleWarmJob{key: key, title: rel.Info.Title, expectedIDs: req.IDs}) {
			queued++
			if queued >= maxReleaseTitleLookups {
				break
			}
		}
	}
}

func (s *Service) enqueueReleaseTitleWarm(job releaseTitleWarmJob) bool {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	if s.bgClosed {
		return false
	}
	if s.releaseTitlePending == nil {
		s.releaseTitlePending = map[releaseTitleKey]bool{}
	}
	if s.releaseTitlePending[job.key] || len(s.releaseTitleQueue) >= maxReleaseTitleWarmQueue {
		return false
	}
	s.releaseTitlePending[job.key] = true
	s.releaseTitleQueue = append(s.releaseTitleQueue, job)
	if s.releaseTitleRunning {
		return true
	}
	s.releaseTitleRunning = true
	s.releaseTitleIdle = make(chan struct{})
	s.bgWG.Add(1)
	go s.runReleaseTitleWarmWorker()
	return true
}

func (s *Service) runReleaseTitleWarmWorker() {
	defer s.bgWG.Done()
	for {
		s.bgMu.Lock()
		if s.bgClosed || len(s.releaseTitleQueue) == 0 {
			s.releaseTitleQueue = nil
			clear(s.releaseTitlePending)
			s.releaseTitleRunning = false
			close(s.releaseTitleIdle)
			s.bgMu.Unlock()
			return
		}
		next := s.releaseTitleQueue[0]
		s.releaseTitleQueue[0] = releaseTitleWarmJob{}
		s.releaseTitleQueue = s.releaseTitleQueue[1:]
		s.bgMu.Unlock()

		s.runReleaseTitleWarmJob(next)

		s.bgMu.Lock()
		delete(s.releaseTitlePending, next.key)
		s.bgMu.Unlock()
	}
}

func (s *Service) runReleaseTitleWarmJob(job releaseTitleWarmJob) {
	if s.bgCtx.Err() != nil {
		return
	}
	if v, ok := s.cachedReleaseTitleVerdict(job.key); ok {
		if !v.persisted && releaseTitleMatchesExpected(v.ids, job.expectedIDs) {
			s.ensureReleaseTitlePersisted(s.bgCtx, job.key, v.ids)
		}
		return
	}
	if s.releaseTitleWarmBackedOff(job.key) {
		return
	}
	if err := s.paceReleaseTitleWarm(s.bgCtx); err != nil {
		return
	}
	if v, ok := s.cachedReleaseTitleVerdict(job.key); ok {
		if !v.persisted && releaseTitleMatchesExpected(v.ids, job.expectedIDs) {
			s.ensureReleaseTitlePersisted(s.bgCtx, job.key, v.ids)
		}
		return
	}
	lctx, cancel := context.WithTimeout(s.bgCtx, releaseTitleLookupTimeout)
	defer cancel()
	ids, err := s.fetchReleaseTitle(lctx, job.key, job.title, job.expectedIDs)
	if err != nil {
		if s.bgCtx.Err() == nil {
			s.noteReleaseTitleWarmError(job.key)
			s.log.Debug("background release title verification failed", "title", job.title, "err", err)
		}
		return
	}
	if releaseTitleMatchesExpected(ids, job.expectedIDs) {
		// Drop short-lived detail variant snapshots so the next detail read
		// sees the newly verified localized alias immediately.
		s.bgMu.Lock()
		if len(s.detailVariants) > 0 {
			clear(s.detailVariants)
		}
		s.bgMu.Unlock()
	}
}

func (s *Service) paceReleaseTitleWarm(ctx context.Context) error {
	interval := s.releaseTitleWarmInterval
	if interval <= 0 {
		interval = defaultReleaseTitleWarmInterval
	}
	now := s.now()
	var wait time.Duration
	s.releaseTitles.mu.Lock()
	if !s.releaseTitles.lastWarm.IsZero() {
		if remaining := interval - now.Sub(s.releaseTitles.lastWarm); remaining > 0 {
			wait = remaining
		}
	}
	if wait == 0 {
		s.releaseTitles.lastWarm = now
	}
	s.releaseTitles.mu.Unlock()
	if wait > 0 {
		waitFn := s.releaseTitleWarmWait
		if waitFn == nil {
			waitFn = waitReleaseTitleContext
		}
		if err := waitFn(ctx, wait); err != nil {
			return err
		}
		s.releaseTitles.mu.Lock()
		s.releaseTitles.lastWarm = s.now()
		s.releaseTitles.mu.Unlock()
	}
	return nil
}

func waitReleaseTitleContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// flushReleaseTitleWarm waits for queued background release-title verification
// to finish in tests.
func (s *Service) flushReleaseTitleWarm(ctx context.Context) error {
	s.bgMu.Lock()
	idle := s.releaseTitleIdle
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

func (s *Service) metadataTitleMatches(rel core.Release, req core.SearchRequest) bool {
	ids, ok := s.cachedReleaseTitle(s.releaseTitleKey(rel, req))
	if !ok {
		return false
	}
	return releaseTitleMatchesExpected(ids, req.IDs)
}
