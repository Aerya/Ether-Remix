package debrid

import (
	"context"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

const maxPayloadMetaCache = 4096

// CachedFileMeta holds file-level metadata already learned for a debrid
// release (no network or database reads on lookup).
type CachedFileMeta struct {
	Path     string
	Size     int64
	InfoHash string
	Provider string
}

func payloadMetaHashKey(hash string, target *core.ReleaseTarget) string {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if hash == "" {
		return ""
	}
	return (core.Release{
		Protocol: core.ProtocolTorrent,
		InfoHash: hash,
		Target:   target,
	}).ScopedContentID("bt:" + hash)
}

func (s *Service) rememberPayloadMeta(p Payload) {
	if s == nil || p.Path == "" {
		return
	}
	meta := CachedFileMeta{
		Path:     p.Path,
		Size:     p.Size,
		InfoHash: strings.ToLower(strings.TrimSpace(p.InfoHash)),
		Provider: p.Provider,
	}
	s.payloadMetaMu.Lock()
	if s.payloadMeta == nil {
		s.payloadMeta = make(map[string]CachedFileMeta)
	}
	if len(s.payloadMeta) < maxPayloadMetaCache {
		if p.Key != "" {
			s.payloadMeta[p.Key] = meta
		}
		if hk := payloadMetaHashKey(p.InfoHash, p.Target); hk != "" {
			s.payloadMeta[hk] = meta
		}
	} else {
		if _, ok := s.payloadMeta[p.Key]; ok && p.Key != "" {
			s.payloadMeta[p.Key] = meta
		}
		if hk := payloadMetaHashKey(p.InfoHash, p.Target); hk != "" {
			if _, ok := s.payloadMeta[hk]; ok {
				s.payloadMeta[hk] = meta
			}
		}
	}
	s.payloadMetaMu.Unlock()
}

func (s *Service) preloadPayloadMeta() {
	if s == nil || s.db == nil {
		return
	}
	cutoff := s.now().Add(-s.opts.PayloadTTL).Unix()
	rows, err := s.db.Query(`SELECT payload FROM debrid_payloads WHERE updated_at >= ? ORDER BY updated_at DESC LIMIT ?`, cutoff, maxPayloadMetaCache)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		if p, err := DecodePayload([]byte(raw)); err == nil {
			s.rememberPayloadMeta(p)
		}
	}
}

// CachedFileMetadata returns already-fetched debrid file metadata for rel from
// memory without any database or provider calls.
func (s *Service) CachedFileMetadata(rel core.Release) (CachedFileMeta, bool) {
	if s == nil {
		return CachedFileMeta{}, false
	}
	s.payloadMetaMu.RLock()
	defer s.payloadMetaMu.RUnlock()
	if len(s.payloadMeta) == 0 {
		return CachedFileMeta{}, false
	}
	if key := payloadKey(rel); key != "" {
		if m, ok := s.payloadMeta[key]; ok {
			return m, true
		}
	}
	if hash := strings.ToLower(strings.TrimSpace(rel.InfoHash)); hash != "" && !(rel.Info.FullSeason && !rel.HasFileOrEpisodeScope()) {
		if hk := rel.ScopedContentID("bt:" + hash); hk != "" {
			if m, ok := s.payloadMeta[hk]; ok {
				return m, true
			}
		}
	}
	return CachedFileMeta{}, false
}

// probeProviderAllowed reports whether provider id is healthy and idle enough
// for a low-priority viewer-triggered header probe. Unlike AnalysisAllowed, it
// does not require a library-analysis sweep lease, but it still yields to
// provider backoffs, auth pauses, rate-limit holds, and active live playback.
func (s *Service) probeProviderAllowed(id string) bool {
	if s == nil || id == "" {
		return false
	}
	now := s.now()
	e := s.entryByID(id)
	if e == nil || e.blocked(now) != nil {
		return false
	}
	s.analysisMu.Lock()
	state := s.analysis[id]
	blocked := state != nil && (now.Before(state.until) || now.Before(state.liveUntil))
	s.analysisMu.Unlock()
	return !blocked
}

// ProbePayload obtains a readable debrid payload for low-priority header
// probing without touching any indexer, queuing downloads, or adding uncached
// torrents. It succeeds only for:
//   - non-on-demand direct HTTP streams already carrying a StreamURL;
//   - torrents with a stored reusable payload; or
//   - torrents already known cached on an active provider that is not paused
//     or cooling down from live playback.
func (s *Service) ProbePayload(ctx context.Context, rel core.Release) ([]byte, bool) {
	if s == nil {
		return nil, false
	}
	bgCtx := ctx
	if !core.IsBackgroundCheck(bgCtx) {
		bgCtx = core.WithBackgroundBudget(bgCtx, func(context.Context, string, string, int64) error { return nil })
	}
	if rel.Protocol == core.ProtocolHTTP {
		if rel.StreamURL == "" || rel.OpensOnDemand() {
			return nil, false
		}
		b, err := s.DirectPayload(bgCtx, rel)
		if err != nil || len(b) == 0 {
			return nil, false
		}
		return b, true
	}
	if rel.Protocol != core.ProtocolTorrent {
		return nil, false
	}
	entries := s.active()
	if len(entries) == 0 {
		return nil, false
	}
	if p, ok := s.reusablePayload(bgCtx, rel); ok {
		if !s.probeProviderAllowed(p.Provider) {
			return nil, false
		}
		return p.Encode(), true
	}
	hash := strings.ToLower(strings.TrimSpace(rel.InfoHash))
	if hash == "" {
		return nil, false
	}
	magnet := rel.MagnetURL
	if rel.Private || magnet == "" {
		magnet = bareMagnet(hash)
	}
	base := Payload{
		Key:       payloadKey(rel),
		ReleaseID: rel.ID,
		InfoHash:  hash,
		Magnet:    magnet,
		Private:   rel.Private,
		Target:    rel.Target,
		Release:   rel.Name,
	}
	choose := func(files []File) (File, error) {
		s.recordTorrentFiles(bgCtx, rel, files)
		return ChooseFile(files, rel.Target)
	}
	for _, e := range entries {
		if bgCtx.Err() != nil {
			return nil, false
		}
		if !s.probeProviderAllowed(e.cfg.ID) || s.refusedFor(e.cfg.ID, hash) {
			continue
		}
		cached, known, _ := s.knownFor(bgCtx, e.cfg.ID, hash)
		isCached := (known && cached) || (!known && slices.Contains(rel.DebridCached, e.cfg.Kind))
		if !isCached {
			continue
		}
		p, err := s.acquireOn(bgCtx, e, base, choose, false)
		if err == nil {
			s.savePayload(bgCtx, p)
			return p.Encode(), true
		}
	}
	return nil, false
}
