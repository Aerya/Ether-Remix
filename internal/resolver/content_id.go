package resolver

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/nzb"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("resolver", 9, `
ALTER TABLE resolver_bad ADD COLUMN content_id TEXT NOT NULL DEFAULT '';
CREATE INDEX resolver_bad_content ON resolver_bad(content_id);
CREATE TABLE resolver_content_links (
  release_id TEXT PRIMARY KEY,
  content_id TEXT NOT NULL DEFAULT '',
  prefetch_id TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL
);
CREATE INDEX resolver_content_links_cid ON resolver_content_links(content_id);
CREATE INDEX resolver_content_links_pre ON resolver_content_links(prefetch_id);
CREATE TABLE resolver_failed_grabs (
  content_id TEXT PRIMARY KEY,
  reason TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL
);
CREATE INDEX resolver_failed_grabs_updated ON resolver_failed_grabs(updated_at);`)
}

const (
	// failedGrabRetention is how long a dead post or missing NZB grab is
	// remembered across indexers so other indexers are not queried for the
	// same dead upload. Changing configured Usenet providers or indexers
	// clears it immediately via forgetSourceResults.
	failedGrabRetention = 24 * time.Hour

	maxContentLinks = 20_000
	maxFailedGrabs  = 20_000
)

type contentLink struct {
	contentID  string
	preFetchID string
	at         time.Time
}

type failedGrabMark struct {
	reason string
	at     time.Time
}

func (m failedGrabMark) live(now time.Time) bool {
	return now.Sub(m.at) < failedGrabRetention
}

// permanentGrabFailure reports whether an error from FetchNZB proves the
// release's NZB or underlying post is permanently gone/corrupt rather than
// transiently unreachable or quota-limited.
func permanentGrabFailure(err error) bool {
	if err == nil {
		return false
	}
	if permanentFailure(err) || errors.Is(err, nzb.ErrInvalid) {
		return true
	}
	var apiErr *newznab.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Budget {
			return false
		}
		switch apiErr.Code {
		case 202, 300, 404, 410:
			return true
		}
	}
	return strings.Contains(err.Error(), "is not an NZB")
}

func appendUniqueKey(dst []string, k string) []string {
	if k == "" || slices.Contains(dst, k) {
		return dst
	}
	return append(dst, k)
}

// releaseContentKeys returns all known cross-indexer content keys for rel
// ("nzb:<sha256>" and/or "pre:<sizeBucket>|<poster>|<uploadDay>"), scoped to
// the targeted episode when rel is a season pack.
func (s *Service) releaseContentKeys(rel core.Release, extra ...string) []string {
	var keys []string
	if rel.ContentID != "" {
		keys = appendUniqueKey(keys, rel.ScopedContentID(rel.ContentID))
	}
	if pre := rel.PreFetchContentID(); pre != "" {
		keys = appendUniqueKey(keys, pre)
	}
	if s != nil && rel.ID != "" {
		s.mu.RLock()
		link, ok := s.contentByRelease[rel.ID]
		s.mu.RUnlock()
		if ok {
			keys = appendUniqueKey(keys, link.contentID)
			keys = appendUniqueKey(keys, link.preFetchID)
		}
	}
	for _, k := range extra {
		keys = appendUniqueKey(keys, k)
	}
	return keys
}

// idContentKeys resolves known content keys for a release ID from in-memory
// links and recent/pinned releases without touching the database.
func (s *Service) idContentKeys(releaseID string) []string {
	if s == nil || releaseID == "" {
		return nil
	}
	var keys []string
	if strings.HasPrefix(releaseID, nzb.ContentIDPrefix) || strings.HasPrefix(releaseID, core.PreFetchContentIDPrefix) {
		keys = appendUniqueKey(keys, releaseID)
	}
	s.mu.RLock()
	link, ok := s.contentByRelease[releaseID]
	s.mu.RUnlock()
	if ok {
		keys = appendUniqueKey(keys, link.contentID)
		keys = appendUniqueKey(keys, link.preFetchID)
	}
	if rel, ok := s.LookupRelease(releaseID); ok {
		for _, k := range s.releaseContentKeys(rel) {
			keys = appendUniqueKey(keys, k)
		}
	}
	return keys
}

// idContentKeysDB extends idContentKeys with stored resolver_content_links when
// releaseID is no longer in memory.
func (s *Service) idContentKeysDB(ctx context.Context, releaseID string) []string {
	keys := s.idContentKeys(releaseID)
	if s == nil || s.db == nil || releaseID == "" {
		return keys
	}
	var cid, pre string
	err := s.db.QueryRowContext(ctx,
		`SELECT content_id, prefetch_id FROM resolver_content_links WHERE release_id=?`,
		releaseID).Scan(&cid, &pre)
	if err == nil {
		keys = appendUniqueKey(keys, cid)
		keys = appendUniqueKey(keys, pre)
	}
	return keys
}

// preferredContentKey chooses the strongest content key (preferring "nzb:..."
// over "pre:...") to store on resolver_bad.content_id.
func preferredContentKey(keys []string) string {
	for _, k := range keys {
		if strings.HasPrefix(k, nzb.ContentIDPrefix) {
			return k
		}
	}
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

// learnReleaseContent derives the NZB message-ID content identity (and fills
// any missing poster/upload-day pre-fetch identity) from a fetched NZB payload,
// linking rel.ID to those cross-indexer keys in memory and storage.
func (s *Service) learnReleaseContent(ctx context.Context, rel core.Release, payload []byte) (core.Release, []string) {
	origPre := rel.PreFetchContentID()
	cid, poster, uploadDate, totalBytes := nzb.InspectContent(payload)
	if cid != "" {
		rel.ContentID = cid
	}
	if rel.Poster == "" && poster != "" {
		rel.Poster = poster
	}
	if rel.UsenetDate.IsZero() && !uploadDate.IsZero() {
		rel.UsenetDate = uploadDate
	}
	var extra []string
	extra = appendUniqueKey(extra, origPre)
	size := rel.Size
	if size <= 0 && totalBytes > 0 {
		size = totalBytes
	}
	p := rel.Poster
	if p == "" {
		p = poster
	}
	dt := rel.UsenetDate
	if dt.IsZero() {
		dt = uploadDate
	}
	if dt.IsZero() {
		dt = rel.PubDate
	}
	nzbPre := rel.ScopedContentID(core.PreFetchContentIDFor(size, p, dt))
	extra = appendUniqueKey(extra, nzbPre)

	scopedCID := rel.ScopedContentID(rel.ContentID)
	bestPre := origPre
	if bestPre == "" {
		bestPre = nzbPre
	}
	if s != nil && rel.ID != "" && (scopedCID != "" || bestPre != "") {
		now := s.now()
		s.mu.Lock()
		if s.contentByRelease == nil {
			s.contentByRelease = map[string]contentLink{}
		}
		prev := s.contentByRelease[rel.ID]
		if scopedCID == "" {
			scopedCID = prev.contentID
		}
		if bestPre == "" {
			bestPre = prev.preFetchID
		}
		boundMap(s.contentByRelease, maxContentLinks, func(l contentLink) bool {
			return now.Sub(l.at) >= badRetention
		})
		s.contentByRelease[rel.ID] = contentLink{contentID: scopedCID, preFetchID: bestPre, at: now}
		s.mu.Unlock()

		s.con.mu.Lock()
		if recent, ok := s.con.recent[rel.ID]; ok {
			if rel.ContentID != "" {
				recent.rel.ContentID = rel.ContentID
			}
			if recent.rel.Poster == "" && rel.Poster != "" {
				recent.rel.Poster = rel.Poster
			}
			if recent.rel.UsenetDate.IsZero() && !rel.UsenetDate.IsZero() {
				recent.rel.UsenetDate = rel.UsenetDate
			}
			s.con.recent[rel.ID] = recent
		}
		s.con.mu.Unlock()

		if s.db != nil && ctx.Value(speculativeHealthKey{}) == nil {
			relID, cVal, pVal := rel.ID, scopedCID, bestPre
			s.persistBookkeeping(ctx, func(ctx context.Context) {
				if s.db == nil {
					return
				}
				_, _ = s.db.ExecContext(ctx,
					`INSERT INTO resolver_content_links(release_id, content_id, prefetch_id, updated_at)
					 VALUES (?, ?, ?, ?)
					 ON CONFLICT(release_id) DO UPDATE SET
					   content_id=CASE WHEN excluded.content_id<>'' THEN excluded.content_id ELSE resolver_content_links.content_id END,
					   prefetch_id=CASE WHEN excluded.prefetch_id<>'' THEN excluded.prefetch_id ELSE resolver_content_links.prefetch_id END,
					   updated_at=excluded.updated_at`,
					relID, cVal, pVal, now.Unix())
			})
		}
	}
	return rel, extra
}

// loadContentIdentityState loads cross-indexer blocklist marks, content links,
// and failed-grab memory on startup or cluster resync.
func (s *Service) loadContentIdentityState(ctx context.Context) {
	if s.db == nil {
		return
	}
	now := s.now()
	linkCutoff := now.Add(-badRetention).Unix()
	if lrows, err := s.db.QueryContext(ctx,
		`SELECT release_id, content_id, prefetch_id, updated_at FROM resolver_content_links WHERE updated_at>=? ORDER BY updated_at DESC LIMIT ?`,
		linkCutoff, maxContentLinks); err == nil {
		s.mu.Lock()
		if s.contentByRelease == nil {
			s.contentByRelease = map[string]contentLink{}
		}
		for lrows.Next() {
			var rid, cid, pre string
			var at int64
			if lrows.Scan(&rid, &cid, &pre, &at) == nil && rid != "" {
				s.contentByRelease[rid] = contentLink{contentID: cid, preFetchID: pre, at: time.Unix(at, 0)}
			}
		}
		s.mu.Unlock()
		lrows.Close()
	}

	if brows, err := s.db.QueryContext(ctx,
		`SELECT b.release_id, b.content_id, COALESCE(l.content_id, ''), COALESCE(l.prefetch_id, ''), b.reason, b.updated_at, b.manual
		 FROM resolver_bad b
		 LEFT JOIN resolver_content_links l ON l.release_id = b.release_id
		 WHERE b.manual=1 OR b.updated_at>=?`,
		now.Add(-badRetention).Unix()); err == nil {
		s.mu.Lock()
		if s.badContent == nil {
			s.badContent = map[string]badMark{}
		}
		for brows.Next() {
			var rid, bcid, lcid, lpre, reason string
			var at int64
			var manual int
			if brows.Scan(&rid, &bcid, &lcid, &lpre, &reason, &at, &manual) == nil {
				mark := badMark{reason: reason, at: time.Unix(at, 0), manual: manual == 1}
				for _, k := range []string{bcid, lcid, lpre} {
					if k == "" {
						continue
					}
					if prev, exists := s.badContent[k]; exists && prev.manual && !mark.manual {
						continue
					}
					s.badContent[k] = mark
				}
			}
		}
		s.mu.Unlock()
		brows.Close()
	}

	if frows, err := s.db.QueryContext(ctx,
		`SELECT content_id, reason, updated_at FROM resolver_failed_grabs WHERE updated_at>=? ORDER BY updated_at DESC LIMIT ?`,
		now.Add(-failedGrabRetention).Unix(), maxFailedGrabs); err == nil {
		s.mu.Lock()
		if s.failedGrabs == nil {
			s.failedGrabs = map[string]failedGrabMark{}
		}
		for frows.Next() {
			var cid, reason string
			var at int64
			if frows.Scan(&cid, &reason, &at) == nil && cid != "" {
				s.failedGrabs[cid] = failedGrabMark{reason: reason, at: time.Unix(at, 0)}
			}
		}
		s.mu.Unlock()
		frows.Close()
	}
}

func (s *Service) markBadContentLocked(keys []string, mark badMark) {
	if len(keys) == 0 {
		return
	}
	if s.badContent == nil {
		s.badContent = map[string]badMark{}
	}
	for _, k := range keys {
		if k == "" {
			continue
		}
		m := mark
		if prev, ok := s.badContent[k]; ok && prev.manual && !m.manual {
			m.reason, m.manual = prev.reason, true
		}
		s.badContent[k] = m
	}
}

func (s *Service) isBadRelease(rel core.Release, extraKeys ...string) bool {
	_, ok := s.badReasonRelease(rel, extraKeys...)
	return ok
}

func (s *Service) badReasonRelease(rel core.Release, extraKeys ...string) (string, bool) {
	if s == nil {
		return "", false
	}
	keys := s.releaseContentKeys(rel, extraKeys...)
	now := s.now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if rel.ID != "" {
		if m, ok := s.bad[rel.ID]; ok && m.live(now) {
			return m.reason, true
		}
	}
	for _, k := range keys {
		if m, ok := s.badContent[k]; ok && m.live(now) {
			return m.reason, true
		}
	}
	return "", false
}

// allVerificationKeys returns the release ID together with all cross-indexer
// content keys so a passed health check is shared across indexers.
func (s *Service) allVerificationKeys(rel core.Release, extraKeys ...string) []string {
	var keys []string
	keys = appendUniqueKey(keys, rel.ID)
	for _, k := range s.releaseContentKeys(rel, extraKeys...) {
		keys = appendUniqueKey(keys, k)
	}
	return keys
}

func (s *Service) markVerifiedKeys(ctx context.Context, keys []string) {
	for _, k := range keys {
		if k != "" {
			s.markVerified(ctx, k)
		}
	}
}

func (s *Service) recentlyVerifiedKeys(ctx context.Context, keys []string) bool {
	for _, k := range keys {
		if k != "" && s.recentlyVerified(ctx, k) {
			return true
		}
	}
	return false
}

func (s *Service) verifiedAtRelease(ctx context.Context, rel core.Release) (time.Time, bool) {
	for _, k := range s.allVerificationKeys(rel) {
		if at, ok := s.verifiedAt(ctx, k); ok {
			return at, true
		}
	}
	return time.Time{}, false
}

// verifiedReleases checks resolver_verified in batch for every release's ID and
// content identities, returning a map keyed by Release.ID.
func (s *Service) verifiedReleases(ctx context.Context, results []core.Release) map[string]bool {
	if s == nil || s.healthTTL <= 0 || len(results) == 0 {
		return nil
	}
	allKeys := make([]string, 0, len(results)*2)
	relKeys := make([][]string, len(results))
	for i, r := range results {
		ks := s.allVerificationKeys(r)
		relKeys[i] = ks
		allKeys = append(allKeys, ks...)
	}
	vmap := s.verifiedAmong(ctx, allKeys)
	if len(vmap) == 0 {
		return nil
	}
	out := make(map[string]bool, len(results))
	for i, r := range results {
		if r.ID == "" {
			continue
		}
		for _, k := range relKeys[i] {
			if vmap[k] {
				out[r.ID] = true
				break
			}
		}
	}
	return out
}

func (s *Service) forgetVerifiedWithContent(ctx context.Context, releaseID string, extraKeys ...string) {
	var keys []string
	keys = appendUniqueKey(keys, releaseID)
	for _, k := range s.idContentKeys(releaseID) {
		keys = appendUniqueKey(keys, k)
	}
	for _, k := range extraKeys {
		keys = appendUniqueKey(keys, k)
	}
	for _, k := range keys {
		s.forgetVerified(ctx, k)
	}
}

// noteFailedGrab persists a dead-post or failed-grab content identity so every
// indexer skips the dead upload without re-grabbing it.
func (s *Service) noteFailedGrab(ctx context.Context, keys []string, reason string) {
	if s == nil || len(keys) == 0 {
		return
	}
	if reason == "" {
		reason = "failed grab"
	}
	now := s.now()
	var saved []string
	s.mu.Lock()
	if s.failedGrabs == nil {
		s.failedGrabs = map[string]failedGrabMark{}
	}
	for _, k := range keys {
		if k == "" {
			continue
		}
		boundMap(s.failedGrabs, maxFailedGrabs, func(m failedGrabMark) bool { return !m.live(now) })
		s.failedGrabs[k] = failedGrabMark{reason: reason, at: now}
		saved = appendUniqueKey(saved, k)
	}
	s.mu.Unlock()
	if len(saved) == 0 || s.db == nil || ctx.Value(speculativeHealthKey{}) != nil {
		return
	}
	s.persistBookkeeping(ctx, func(ctx context.Context) {
		if s.db == nil {
			return
		}
		for _, k := range saved {
			if _, err := s.db.ExecContext(ctx,
				`INSERT INTO resolver_failed_grabs(content_id, reason, updated_at) VALUES (?, ?, ?)
				 ON CONFLICT(content_id) DO UPDATE SET reason=excluded.reason, updated_at=excluded.updated_at`,
				k, reason, now.Unix()); err == nil {
				s.notify("grabfail:" + k)
			}
		}
	})
}

func (s *Service) forgetFailedGrabs(ctx context.Context, keys []string) {
	if s == nil || len(keys) == 0 {
		return
	}
	s.mu.Lock()
	for _, k := range keys {
		delete(s.failedHealth, k)
		delete(s.failedGrabs, k)
	}
	s.mu.Unlock()
	if s.db != nil {
		for _, k := range keys {
			if k == "" {
				continue
			}
			if _, err := s.db.ExecContext(ctx, `DELETE FROM resolver_failed_grabs WHERE content_id=?`, k); err == nil {
				s.notify("grabfail:" + k)
			}
		}
	}
}

func (s *Service) recentlyFailedGrabKey(key string) bool {
	if s == nil || key == "" {
		return false
	}
	now := s.now()
	s.mu.RLock()
	m, ok := s.failedGrabs[key]
	s.mu.RUnlock()
	return ok && m.live(now)
}

// recentlyFailedRelease reports whether rel (or any of its cross-indexer
// content keys) recently failed health validation or a permanent NZB grab.
func (s *Service) recentlyFailedRelease(rel core.Release, extraKeys ...string) bool {
	if s == nil {
		return false
	}
	if rel.ID != "" && (s.recentlyFailedHealth(rel.ID) || s.recentlyFailedGrabKey(rel.ID)) {
		return true
	}
	for _, k := range s.releaseContentKeys(rel, extraKeys...) {
		if s.recentlyFailedHealth(k) || s.recentlyFailedGrabKey(k) {
			return true
		}
	}
	return false
}

// recordFailedValidation records a confirmed validation failure against both
// the short in-memory health skip map and durable cross-indexer failed-grab
// memory. Callers pass a non-speculative Resolve context.
func (s *Service) recordFailedValidation(ctx context.Context, rel core.Release, v validation) {
	if s == nil || ctx.Value(speculativeHealthKey{}) != nil {
		return
	}
	if v.rel.ID != "" {
		rel = v.rel
	}
	contentKeys := s.releaseContentKeys(rel, v.extraContentKeys...)
	s.noteFailedHealth(rel.ID)
	for _, k := range contentKeys {
		s.noteFailedHealth(k)
	}
	reason := "health: unhealthy"
	if v.err != nil {
		reason = v.err.Error()
	}
	if !v.healthChecked {
		// FetchNZB itself failed permanently: remember both content keys and rel.ID.
		if permanentGrabFailure(v.err) {
			keys := append([]string(nil), contentKeys...)
			keys = appendUniqueKey(keys, rel.ID)
			s.noteFailedGrab(ctx, keys, reason)
		}
		return
	}
	if (!v.healthy && v.err == nil) || permanentFailure(v.err) {
		// Post-fetch health failure: persist cross-indexer content identities
		// ("nzb:<sha256>" and "pre:<bucket>|<poster>|<day>").
		if len(contentKeys) > 0 {
			s.noteFailedGrab(ctx, contentKeys, reason)
		}
	}
}

func (s *Service) reloadFailedGrab(ctx context.Context, id string) {
	if s.db == nil || id == "" {
		return
	}
	var reason string
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT reason, updated_at FROM resolver_failed_grabs WHERE content_id=?`, id).Scan(&reason, &at)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failedGrabs == nil {
		s.failedGrabs = map[string]failedGrabMark{}
	}
	switch {
	case err == nil:
		s.failedGrabs[id] = failedGrabMark{reason: reason, at: time.Unix(at, 0)}
	case errors.Is(err, sql.ErrNoRows):
		delete(s.failedGrabs, id)
	}
}

// expandReleaseFallbacks unpacks cross-indexer duplicates merged onto
// Release.Fallbacks by newznab.Manager so ranking and repost grouping see
// every indexer's candidate while preserving each indexer's Release.ID.
func expandReleaseFallbacks(in []core.Release) []core.Release {
	hasFallbacks := false
	for i := range in {
		if len(in[i].Fallbacks) > 0 {
			hasFallbacks = true
			break
		}
	}
	if !hasFallbacks {
		return in
	}
	seen := make(map[string]bool, len(in)*2)
	out := make([]core.Release, 0, len(in)*2)
	var visit func(r core.Release)
	visit = func(r core.Release) {
		fbs := r.Fallbacks
		r.Fallbacks = nil
		if r.ID != "" {
			if seen[r.ID] {
				for _, fb := range fbs {
					visit(fb)
				}
				return
			}
			seen[r.ID] = true
		}
		out = append(out, r)
		for _, fb := range fbs {
			visit(fb)
		}
	}
	for _, r := range in {
		visit(r)
	}
	return out
}
