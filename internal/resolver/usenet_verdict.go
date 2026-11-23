package resolver

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nzb"
)

const (
	// MaxUsenetVerdictEntries caps the shared in-memory Usenet verdict cache.
	MaxUsenetVerdictEntries = 1024
	// UsenetVerdictPositiveTTL is how long a passed Usenet check is reused
	// across background plugin checks, on-demand checks, and Play.
	UsenetVerdictPositiveTTL = 15 * time.Minute
	// UsenetVerdictNegativeTTL is how long a failed Usenet check or permanent
	// playback failure is remembered across Ether and plugins.
	UsenetVerdictNegativeTTL = 30 * time.Minute
)

type usenetVerdictEntry struct {
	result      core.UsenetCheckResult
	releaseKeys []string
	nzbHash     string
	contentID   string
	storedAt    time.Time
	expiresAt   time.Time
}

// UsenetVerdictCache is the bounded, thread-safe Usenet article availability
// verdict cache shared between Ether's resolver/availability checks and plugin
// usenet_check calls (good 15 min, bad 30 min). Entries are indexed by keyless
// NZB URL, raw NZB SHA-256 hash, and cross-indexer NZB content ID ("nzb:...").
type UsenetVerdictCache struct {
	mu        sync.Mutex
	byRelease map[string]*usenetVerdictEntry
	byHash    map[string]*usenetVerdictEntry
	byContent map[string]*usenetVerdictEntry
	entries   map[*usenetVerdictEntry]struct{}
}

// NewUsenetVerdictCache constructs an empty shared Usenet verdict cache.
func NewUsenetVerdictCache() *UsenetVerdictCache {
	return &UsenetVerdictCache{
		byRelease: make(map[string]*usenetVerdictEntry),
		byHash:    make(map[string]*usenetVerdictEntry),
		byContent: make(map[string]*usenetVerdictEntry),
		entries:   make(map[*usenetVerdictEntry]struct{}),
	}
}

func (c *UsenetVerdictCache) initLocked() {
	if c.byRelease == nil {
		c.byRelease = make(map[string]*usenetVerdictEntry)
	}
	if c.byHash == nil {
		c.byHash = make(map[string]*usenetVerdictEntry)
	}
	if c.byContent == nil {
		c.byContent = make(map[string]*usenetVerdictEntry)
	}
	if c.entries == nil {
		c.entries = make(map[*usenetVerdictEntry]struct{})
	}
}

func (c *UsenetVerdictCache) deleteEntryLocked(e *usenetVerdictEntry) {
	if e == nil {
		return
	}
	for _, k := range e.releaseKeys {
		if c.byRelease[k] == e {
			delete(c.byRelease, k)
		}
	}
	if e.nzbHash != "" && c.byHash[e.nzbHash] == e {
		delete(c.byHash, e.nzbHash)
	}
	if e.contentID != "" && c.byContent[e.contentID] == e {
		delete(c.byContent, e.contentID)
	}
	delete(c.entries, e)
}

func (c *UsenetVerdictCache) isReferencedLocked(e *usenetVerdictEntry) bool {
	if len(e.releaseKeys) > 0 {
		return true
	}
	if e.nzbHash != "" && c.byHash[e.nzbHash] == e {
		return true
	}
	if e.contentID != "" && c.byContent[e.contentID] == e {
		return true
	}
	return false
}

func (c *UsenetVerdictCache) detachReleaseKeyLocked(e *usenetVerdictEntry, releaseKey string) {
	if e == nil {
		return
	}
	e.releaseKeys = slices.DeleteFunc(e.releaseKeys, func(k string) bool { return k == releaseKey })
	if !c.isReferencedLocked(e) {
		delete(c.entries, e)
	}
}

func (c *UsenetVerdictCache) attachReleaseKeyLocked(e *usenetVerdictEntry, releaseKey string) {
	if e == nil || releaseKey == "" {
		return
	}
	if old := c.byRelease[releaseKey]; old != nil && old != e {
		c.detachReleaseKeyLocked(old, releaseKey)
	}
	c.byRelease[releaseKey] = e
	if !slices.Contains(e.releaseKeys, releaseKey) {
		e.releaseKeys = append(e.releaseKeys, releaseKey)
	}
}

func (c *UsenetVerdictCache) attachHashLocked(e *usenetVerdictEntry, nzbHash string) {
	if e == nil || nzbHash == "" {
		return
	}
	if old := c.byHash[nzbHash]; old != nil && old != e {
		old.nzbHash = ""
		if !c.isReferencedLocked(old) {
			delete(c.entries, old)
		}
	}
	if e.nzbHash != "" && e.nzbHash != nzbHash && c.byHash[e.nzbHash] == e {
		delete(c.byHash, e.nzbHash)
	}
	c.byHash[nzbHash] = e
	e.nzbHash = nzbHash
}

func (c *UsenetVerdictCache) attachContentIDLocked(e *usenetVerdictEntry, contentID string) {
	if e == nil || contentID == "" {
		return
	}
	if old := c.byContent[contentID]; old != nil && old != e {
		old.contentID = ""
		if !c.isReferencedLocked(old) {
			delete(c.entries, old)
		}
	}
	if e.contentID != "" && e.contentID != contentID && c.byContent[e.contentID] == e {
		delete(c.byContent, e.contentID)
	}
	c.byContent[contentID] = e
	e.contentID = contentID
}

func (c *UsenetVerdictCache) attachAllLocked(e *usenetVerdictEntry, releaseKey, nzbHash, contentID string) {
	if releaseKey != "" {
		c.attachReleaseKeyLocked(e, releaseKey)
	}
	if nzbHash != "" && e.nzbHash == "" {
		c.attachHashLocked(e, nzbHash)
	}
	if contentID != "" && e.contentID == "" {
		c.attachContentIDLocked(e, contentID)
	}
}

func (c *UsenetVerdictCache) pruneLocked(now time.Time) {
	for e := range c.entries {
		if now.After(e.expiresAt) {
			c.deleteEntryLocked(e)
		}
	}
	for len(c.entries) >= MaxUsenetVerdictEntries {
		var oldest *usenetVerdictEntry
		for e := range c.entries {
			if oldest == nil || e.storedAt.Before(oldest.storedAt) {
				oldest = e
			}
		}
		if oldest == nil {
			break
		}
		c.deleteEntryLocked(oldest)
	}
}

func (c *UsenetVerdictCache) liveEntryLocked(now time.Time, e *usenetVerdictEntry) *usenetVerdictEntry {
	if e == nil {
		return nil
	}
	if now.After(e.expiresAt) {
		c.deleteEntryLocked(e)
		return nil
	}
	return e
}

func (c *UsenetVerdictCache) mergeIntoLocked(dst, src *usenetVerdictEntry) {
	if dst == nil || src == nil || dst == src {
		return
	}
	extraKeys := slices.Clone(src.releaseKeys)
	srcHash := src.nzbHash
	srcCID := src.contentID
	c.deleteEntryLocked(src)
	for _, k := range extraKeys {
		c.attachReleaseKeyLocked(dst, k)
	}
	if dst.nzbHash == "" && srcHash != "" {
		c.attachHashLocked(dst, srcHash)
	}
	if dst.contentID == "" && srcCID != "" {
		c.attachContentIDLocked(dst, srcCID)
	}
}

// Lookup returns a live cached verdict matching releaseKey, nzbHash, or
// contentID. When found, any additional non-empty keys are aliased onto the
// matched entry so future lookups hit immediately.
func (c *UsenetVerdictCache) Lookup(now time.Time, releaseKey, nzbHash, contentID string) (core.UsenetCheckResult, bool) {
	if c == nil {
		return core.UsenetCheckResult{}, false
	}
	releaseKey = strings.TrimSpace(releaseKey)
	nzbHash = strings.TrimSpace(nzbHash)
	contentID = strings.TrimSpace(contentID)
	if releaseKey == "" && nzbHash == "" && contentID == "" {
		return core.UsenetCheckResult{}, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()

	if releaseKey != "" {
		if e := c.liveEntryLocked(now, c.byRelease[releaseKey]); e != nil {
			c.attachAllLocked(e, releaseKey, nzbHash, contentID)
			res := e.result
			res.Cached = true
			return res, true
		}
	}
	if nzbHash != "" {
		if e := c.liveEntryLocked(now, c.byHash[nzbHash]); e != nil {
			c.attachAllLocked(e, releaseKey, nzbHash, contentID)
			res := e.result
			res.Cached = true
			return res, true
		}
	}
	if contentID != "" {
		if e := c.liveEntryLocked(now, c.byContent[contentID]); e != nil {
			c.attachAllLocked(e, releaseKey, nzbHash, contentID)
			res := e.result
			res.Cached = true
			return res, true
		}
	}
	return core.UsenetCheckResult{}, false
}

// Store records a sampled verdict (good 15 min, bad 30 min). A live recorded
// playback failure is never overwritten by a sampled check.
func (c *UsenetVerdictCache) Store(now time.Time, releaseKey, nzbHash, contentID string, res core.UsenetCheckResult) core.UsenetCheckResult {
	if c == nil {
		return res
	}
	releaseKey = strings.TrimSpace(releaseKey)
	nzbHash = strings.TrimSpace(nzbHash)
	contentID = strings.TrimSpace(contentID)
	if releaseKey == "" && nzbHash == "" && contentID == "" {
		return res
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()

	// Do not let a concurrent sampled check overwrite a recorded playback failure.
	for _, existing := range []*usenetVerdictEntry{c.byRelease[releaseKey], c.byHash[nzbHash], c.byContent[contentID]} {
		existing = c.liveEntryLocked(now, existing)
		if existing == nil {
			continue
		}
		if existing.result.PlaybackFailed && !res.PlaybackFailed {
			c.attachReleaseKeyLocked(existing, releaseKey)
			c.attachHashLocked(existing, nzbHash)
			c.attachContentIDLocked(existing, contentID)
			out := existing.result
			out.Cached = true
			return out
		}
	}

	ttl := UsenetVerdictPositiveTTL
	if !res.Available {
		ttl = UsenetVerdictNegativeTTL
	}
	res.Cached = false
	if res.CheckedAt == 0 {
		res.CheckedAt = now.Unix()
	}

	c.pruneLocked(now)
	e := &usenetVerdictEntry{
		result:    res,
		storedAt:  now,
		expiresAt: now.Add(ttl),
	}
	c.entries[e] = struct{}{}
	c.attachReleaseKeyLocked(e, releaseKey)
	c.attachHashLocked(e, nzbHash)
	c.attachContentIDLocked(e, contentID)
	return res
}

// RecordPlaybackFailure records a permanent playback failure (bad 30 min) across
// every alias of the release so sampled checks and subsequent opens agree.
func (c *UsenetVerdictCache) RecordPlaybackFailure(now time.Time, releaseKey, nzbHash, contentID, reason string, articleMissing bool) {
	if c == nil {
		return
	}
	releaseKey = strings.TrimSpace(releaseKey)
	nzbHash = strings.TrimSpace(nzbHash)
	contentID = strings.TrimSpace(contentID)
	if releaseKey == "" && nzbHash == "" && contentID == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()

	var matched []*usenetVerdictEntry
	for _, cand := range []*usenetVerdictEntry{c.byRelease[releaseKey], c.byHash[nzbHash], c.byContent[contentID]} {
		cand = c.liveEntryLocked(now, cand)
		if cand != nil && !slices.Contains(matched, cand) {
			matched = append(matched, cand)
		}
	}

	var e *usenetVerdictEntry
	var prevChecked, prevMissing int
	for _, m := range matched {
		prevChecked = max(prevChecked, m.result.Checked)
		prevMissing = max(prevMissing, m.result.Missing)
		if e == nil {
			e = m
		} else {
			c.mergeIntoLocked(e, m)
		}
	}

	checked, missing := prevChecked, prevMissing
	if articleMissing {
		missing = max(prevMissing, 1)
		checked = max(prevChecked, missing)
	}
	res := core.UsenetCheckResult{
		Available:      false,
		Checked:        checked,
		Missing:        missing,
		PlaybackFailed: true,
		Reason:         reason,
		CheckedAt:      now.Unix(),
	}

	if e != nil {
		e.result = res
		e.storedAt = now
		e.expiresAt = now.Add(UsenetVerdictNegativeTTL)
		c.attachReleaseKeyLocked(e, releaseKey)
		c.attachHashLocked(e, nzbHash)
		c.attachContentIDLocked(e, contentID)
		return
	}

	c.pruneLocked(now)
	e = &usenetVerdictEntry{
		result:    res,
		storedAt:  now,
		expiresAt: now.Add(UsenetVerdictNegativeTTL),
	}
	c.entries[e] = struct{}{}
	c.attachReleaseKeyLocked(e, releaseKey)
	c.attachHashLocked(e, nzbHash)
	c.attachContentIDLocked(e, contentID)
}

// PlaybackFailed reports whether any key maps to a live playback-failure verdict.
func (c *UsenetVerdictCache) PlaybackFailed(now time.Time, releaseKey, nzbHash, contentID string) bool {
	if c == nil {
		return false
	}
	releaseKey = strings.TrimSpace(releaseKey)
	nzbHash = strings.TrimSpace(nzbHash)
	contentID = strings.TrimSpace(contentID)
	if releaseKey == "" && nzbHash == "" && contentID == "" {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.initLocked()

	for _, e := range []*usenetVerdictEntry{c.byRelease[releaseKey], c.byHash[nzbHash], c.byContent[contentID]} {
		if e != nil && !now.After(e.expiresAt) && e.result.PlaybackFailed {
			return true
		}
	}
	return false
}

// Delete removes any entries matching the given release URLs, hashes, or content IDs.
func (c *UsenetVerdictCache) Delete(keys ...string) {
	if c == nil || len(keys) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, raw := range keys {
		k := strings.TrimSpace(raw)
		if k == "" {
			continue
		}
		if e := c.byRelease[k]; e != nil {
			c.deleteEntryLocked(e)
		}
		if e := c.byHash[k]; e != nil {
			c.deleteEntryLocked(e)
		}
		if e := c.byContent[k]; e != nil {
			c.deleteEntryLocked(e)
		}
	}
}

// Clear drops every cached verdict (e.g. when Usenet providers change).
func (c *UsenetVerdictCache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.byRelease)
	clear(c.byHash)
	clear(c.byContent)
	clear(c.entries)
}

// Stats returns the current entry and index map sizes for testing.
func (c *UsenetVerdictCache) Stats() (entries, byRelease, byHash, byContent int) {
	if c == nil {
		return 0, 0, 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), len(c.byRelease), len(c.byHash), len(c.byContent)
}

// UsenetVerdicts returns the Usenet verdict cache shared with plugin
// usenet_check calls (good 15 min, bad 30 min).
func (s *Service) UsenetVerdicts() *UsenetVerdictCache {
	if s == nil {
		return nil
	}
	return s.usenetVerdicts
}

func (s *Service) usenetVerdictURLKey(rel core.Release) string {
	raw := strings.TrimSpace(rel.NZBURL)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	if s != nil && s.indexer != nil {
		if strip, ok := s.indexer.(interface {
			StripLinkAuth(core.Release) core.Release
		}); ok {
			rel = strip.StripLinkAuth(rel)
			raw = strings.TrimSpace(rel.NZBURL)
		}
	}
	return raw
}

func unscopedNZBContentID(id string) string {
	if !strings.HasPrefix(id, nzb.ContentIDPrefix) {
		return ""
	}
	if cut := strings.IndexByte(id, '|'); cut > 0 {
		return id[:cut]
	}
	return id
}

// usenetVerdictKeys computes the shared verdict cache keys for a Usenet
// release. Bare test stubs without an NZB URL or message-ID content hash return
// empty keys so unrelated dummy fixtures never collide.
func (s *Service) usenetVerdictKeys(rel core.Release, payload []byte) (urlKey, nzbHash, contentID string) {
	urlKey = s.usenetVerdictURLKey(rel)
	contentID = unscopedNZBContentID(rel.ContentID)
	if contentID == "" && len(payload) > 0 {
		cid, _, _, _ := nzb.InspectContent(payload)
		contentID = unscopedNZBContentID(cid)
	}
	if urlKey == "" && contentID == "" {
		return "", "", ""
	}
	nzbHash = strings.TrimSpace(rel.SourceHash)
	if nzbHash == "" && len(payload) > 0 {
		sum := sha256.Sum256(payload)
		nzbHash = hex.EncodeToString(sum[:])
	}
	return urlKey, nzbHash, contentID
}

func (s *Service) recordUsenetVerdict(rel core.Release, payload []byte, available bool, reason string) {
	if s == nil || s.usenetVerdicts == nil || protocolOf(rel) != core.ProtocolUsenet {
		return
	}
	urlKey, nzbHash, contentID := s.usenetVerdictKeys(rel, payload)
	if urlKey == "" && nzbHash == "" && contentID == "" {
		return
	}
	now := s.now()
	s.usenetVerdicts.Store(now, urlKey, nzbHash, contentID, core.UsenetCheckResult{
		Available: available,
		Reason:    reason,
		CheckedAt: now.Unix(),
	})
}

func (s *Service) usenetKeysForReleaseID(releaseID string, contentKeys []string) (urlKey, nzbHash, contentID string) {
	if s == nil {
		return "", "", ""
	}
	s.con.mu.Lock()
	if rec, ok := s.con.recent[releaseID]; ok {
		urlKey = s.usenetVerdictURLKey(rec.rel)
		nzbHash = strings.TrimSpace(rec.rel.SourceHash)
		contentID = unscopedNZBContentID(rec.rel.ContentID)
	}
	s.con.mu.Unlock()
	if contentID == "" {
		for _, k := range contentKeys {
			if cid := unscopedNZBContentID(k); cid != "" {
				contentID = cid
				break
			}
		}
	}
	if urlKey == "" && contentID == "" {
		return "", "", ""
	}
	return urlKey, nzbHash, contentID
}
