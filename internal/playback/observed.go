package playback

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/store"
)

// ResumedTracks is the probe a detail page may show for the release Prepare
// would still open. The key is the viewer plus the profile selection key
// ResumeFor already stores, not a client id and not a clock.
//
// A hit requires all of: this viewer's resume row, the same profile
// selection key, the source preference still allowing that protocol, the
// release not blocked, and a probe row for that same release whose NZB
// hash and file size still match. Saving a different release deletes the
// previous probe, so track ids from the old file cannot be served.
// Another viewer's row is never read. External subtitle URLs are stripped
// on save. There is no separate TTL; when the resume row stops being the
// next Prepare, the tracks stop too.
func (m *Manager) ResumedTracks(ctx context.Context, user *core.User, key core.RatingKey, caps core.ClientCaps) (core.MediaInfo, string, bool) {
	info, rel, _, ok := m.ResumedVersion(ctx, user, key, caps)
	if !ok {
		return core.MediaInfo{}, "", false
	}
	return info, rel.ID, true
}

// ResumedVersion returns the persisted probe, release record, and version
// selector ("" for Auto, non-empty for an explicit non-Auto version slot) of
// the viewer's stored resume row for key.
func (m *Manager) ResumedVersion(ctx context.Context, user *core.User, key core.RatingKey, caps core.ClientCaps) (core.MediaInfo, core.Release, string, bool) {
	if user == nil || user.ID <= 0 {
		return core.MediaInfo{}, core.Release{}, "", false
	}
	rp, err := m.resumeFor(ctx, user, key, caps, false)
	if rp == nil && err == nil && !caps.Remote && m.deps.SelectProfile != nil {
		// A client behind a Docker bridge or LAN subnet may send location=wan
		// on /decision while /library/metadata carries no location query param.
		rcaps := caps
		rcaps.Remote = true
		if m.cfg.Transcode.RemoteMaxBitrateKbps > 0 && (rcaps.MaxBitrateKbps == 0 || m.cfg.Transcode.RemoteMaxBitrateKbps < rcaps.MaxBitrateKbps) {
			rcaps.MaxBitrateKbps = m.cfg.Transcode.RemoteMaxBitrateKbps
		}
		if profileKey(m.selectProfile(rcaps, user, key)) != profileKey(m.selectProfile(caps, user, key)) {
			rp, err = m.resumeFor(ctx, user, key, rcaps, false)
		}
	}
	if err != nil || rp == nil || rp.Release.ID == "" || rp.payloadHash() == "" {
		return core.MediaInfo{}, core.Release{}, "", false
	}
	facts := m.persistedProbeInfoHash(ctx, user.ID, key, rp.Release.ID, rp.payloadHash())
	if facts == nil || facts.releaseID != rp.Release.ID || facts.info.Estimated || !facts.info.HasPlayableMedia() || len(facts.info.Streams) == 0 {
		// Check active/prepared sessions for this viewer + title + release as well.
		m.mu.Lock()
		for _, s := range m.sessions {
			if s == nil || s.closed || s.ident.userID != user.ID || s.ident.key != key {
				continue
			}
			if g := s.current(); g != nil && g.releaseID() == rp.Release.ID {
				if sanitized, ok := persistedProbeFacts(g.infoSnapshot()); ok && rememberedInfoCodecCurrent(sanitized, caps) {
					m.mu.Unlock()
					sanitized.Keyframes = nil
					return sanitized, rp.Release, rp.VersionSelector, true
				}
			}
		}
		m.mu.Unlock()
		return core.MediaInfo{}, core.Release{}, "", false
	}
	if !rememberedInfoCodecCurrent(facts.info, caps) {
		return core.MediaInfo{}, core.Release{}, "", false
	}
	info := facts.info
	info.Keyframes = nil
	return info, rp.Release, rp.VersionSelector, true
}

type preloadedBlocksKey struct{}

func withPreloadedBlocks(ctx context.Context, blocks map[string]bool) context.Context {
	if len(blocks) == 0 {
		return ctx
	}
	if prev, ok := ctx.Value(preloadedBlocksKey{}).(map[string]bool); ok {
		merged := make(map[string]bool, len(prev)+len(blocks))
		for k, v := range prev {
			merged[k] = v
		}
		for k, v := range blocks {
			merged[k] = v
		}
		return context.WithValue(ctx, preloadedBlocksKey{}, merged)
	}
	return context.WithValue(ctx, preloadedBlocksKey{}, blocks)
}

func preloadedReleaseBlocked(ctx context.Context, id string) (bool, bool) {
	if id == "" {
		return false, false
	}
	blocks, ok := ctx.Value(preloadedBlocksKey{}).(map[string]bool)
	if !ok {
		return false, false
	}
	blocked, found := blocks[id]
	return blocked, found
}

// PreloadResumes batches playback_resume, playback_probe, and resolver_bad
// reads for a rendered page of playable titles so ResumedTracks performs
// bounded batch queries instead of per-item lookups. It returns the enriched
// context and the keys that currently have a live or persisted resume point.
func (m *Manager) PreloadResumes(ctx context.Context, user *core.User, keys []core.RatingKey) (context.Context, []core.RatingKey) {
	if user == nil || user.ID <= 0 || len(keys) == 0 {
		return ctx, nil
	}
	if !reqmemo.Active(ctx) {
		ctx = reqmemo.With(ctx)
	}
	if m.resume == nil {
		return ctx, nil
	}
	seen := make(map[core.RatingKey]bool, len(keys))
	uniqueKeys := make([]core.RatingKey, 0, len(keys))
	for _, k := range keys {
		if k > 0 && !seen[k] {
			seen[k] = true
			uniqueKeys = append(uniqueKeys, k)
		}
	}
	if len(uniqueKeys) == 0 {
		return ctx, nil
	}

	records := make(map[core.RatingKey]resumeRecord)
	if m.resume.db != nil {
		queryOK := true
		const batch = 500
		for chunk := range store.Chunks(uniqueKeys, batch) {
			args := make([]any, 0, 1+len(chunk))
			args = append(args, user.ID)
			for _, k := range chunk {
				args = append(args, int64(k))
			}
			rows, err := m.resume.db.QueryContext(ctx, `SELECT `+resumeColumns+` FROM playback_resume WHERE user_id=? AND rating_key IN (`+store.Placeholders(len(chunk))+`)`, args...)
			if err != nil {
				queryOK = false
				break
			}
			for rows.Next() {
				rec, err := scanResume(rows)
				if err != nil {
					queryOK = false
					break
				}
				records[rec.key] = rec
			}
			if rows.Err() != nil {
				queryOK = false
			}
			rows.Close()
			if !queryOK {
				break
			}
		}
		if queryOK {
			for _, k := range uniqueKeys {
				if rec, ok := records[k]; ok {
					cp := rec
					_, _ = reqmemo.Do(ctx, resumeMemoKey(user.ID, k), func() (*resumeRecord, error) {
						return &cp, nil
					})
				} else {
					_, _ = reqmemo.Do(ctx, resumeMemoKey(user.ID, k), func() (*resumeRecord, error) {
						return nil, nil
					})
				}
			}
		}
	}

	type resumeCandidate struct {
		key       core.RatingKey
		releaseID string
		hash      string
	}
	var (
		candidates  []resumeCandidate
		matchedKeys []core.RatingKey
		releaseIDs  []string
		seenRel     = map[string]bool{}
	)
	addReleaseID := func(id string) {
		if id != "" && !seenRel[id] {
			seenRel[id] = true
			releaseIDs = append(releaseIDs, id)
		}
	}
	for _, k := range uniqueKeys {
		if live := m.resume.livePoint(user.ID, k); live != nil {
			candidates = append(candidates, resumeCandidate{
				key:       k,
				releaseID: live.Release.ID,
				hash:      live.payloadHash(),
			})
			matchedKeys = append(matchedKeys, k)
			addReleaseID(live.Release.ID)
			continue
		}
		if rec, ok := records[k]; ok {
			candidates = append(candidates, resumeCandidate{
				key:       k,
				releaseID: rec.releaseID,
				hash:      rec.nzbHash,
			})
			matchedKeys = append(matchedKeys, k)
			addReleaseID(rec.releaseID)
		}
	}
	if len(candidates) == 0 {
		return ctx, nil
	}

	if batcher, ok := m.deps.Resolver.(core.ReleaseBatchBlockChecker); ok && len(releaseIDs) > 0 {
		if blocked, err := batcher.ReleasesBlocked(ctx, releaseIDs); err == nil {
			ctx = withPreloadedBlocks(ctx, blocked)
		}
	}

	if m.resume.db != nil {
		var probeKeys []core.RatingKey
		seenProbeKey := map[core.RatingKey]bool{}
		for _, c := range candidates {
			if c.releaseID == "" || c.hash == "" {
				continue
			}
			if m.resume.liveProbeHash(user.ID, c.key, c.releaseID, c.hash) != nil {
				continue
			}
			if !seenProbeKey[c.key] {
				seenProbeKey[c.key] = true
				probeKeys = append(probeKeys, c.key)
			}
		}
		if len(probeKeys) > 0 {
			probes := map[string]*probeCache{}
			probeOK := true
			const batch = 500
			for chunk := range store.Chunks(probeKeys, batch) {
				args := make([]any, 0, 1+len(chunk))
				args = append(args, user.ID)
				for _, k := range chunk {
					args = append(args, int64(k))
				}
				rows, err := m.resume.db.QueryContext(ctx, `SELECT rating_key, release_id, nzb_hash, size, info_json FROM playback_probe WHERE user_id=? AND rating_key IN (`+store.Placeholders(len(chunk))+`)`, args...)
				if err != nil {
					probeOK = false
					break
				}
				for rows.Next() {
					var (
						rk          int64
						relID, hash string
						size        int64
						raw         string
					)
					if err := rows.Scan(&rk, &relID, &hash, &size, &raw); err != nil {
						probeOK = false
						break
					}
					if pc := decodeStoredProbeRow(relID, hash, size, raw); pc != nil {
						probes[probeInfoMemoKey(user.ID, core.RatingKey(rk), relID, hash)] = pc
					}
				}
				if rows.Err() != nil {
					probeOK = false
				}
				rows.Close()
				if !probeOK {
					break
				}
			}
			if probeOK {
				for _, c := range candidates {
					if c.releaseID == "" || c.hash == "" || m.resume.liveProbeHash(user.ID, c.key, c.releaseID, c.hash) != nil {
						continue
					}
					memoKey := probeInfoMemoKey(user.ID, c.key, c.releaseID, c.hash)
					val := probes[memoKey]
					_, _ = reqmemo.Do(ctx, memoKey, func() (*probeCache, error) {
						return val, nil
					})
				}
			}
		}
	}

	return ctx, matchedKeys
}

// ObservedMedia is historical evidence, not a promise about the next selected release.
// No provider, probe, source opening or keyframe loading is performed by this lookup.
type ObservedMedia struct {
	Info core.MediaInfo
	At   time.Time
}

// ObservedMediaInfo returns this viewer's most recent exact probe only while it
// still matches their current resume release, payload hash and media size. Facts
// expire after 30 days and never cross viewer boundaries.
func (m *Manager) ObservedMediaInfo(ctx context.Context, userID int64, key core.RatingKey) (*ObservedMedia, error) {
	if userID <= 0 {
		return nil, nil
	}
	// Avoid allocating/sorting full public session snapshots for every poster.
	m.mu.Lock()
	var matching []*session
	for _, session := range m.sessions {
		if session.ident.userID == userID && session.ident.key == key {
			matching = append(matching, session)
		}
	}
	m.mu.Unlock()
	var latest *ObservedMedia
	for _, session := range matching {
		session.mu.Lock()
		g, closed, started := session.cur, session.closed, session.startedAt
		session.mu.Unlock()
		if g == nil || closed {
			continue
		}
		info, ok := persistedProbeFacts(g.infoSnapshot())
		if ok && (latest == nil || started.After(latest.At)) {
			info.Chapters = nil
			latest = &ObservedMedia{Info: info, At: started}
		}
	}
	if latest != nil {
		return latest, nil
	}
	if m.resume == nil {
		return nil, nil
	}
	return m.resume.observedMedia(ctx, userID, key, m.now())
}

// ObservedMediaInfoMany batches ObservedMediaInfo across a page of titles so
// quality overlay badges scan active sessions once and query playback_probe +
// playback_resume in bounded chunks instead of once per tile.
func (m *Manager) ObservedMediaInfoMany(ctx context.Context, userID int64, keys []core.RatingKey) (map[core.RatingKey]*ObservedMedia, error) {
	out := make(map[core.RatingKey]*ObservedMedia, len(keys))
	if userID <= 0 || len(keys) == 0 {
		return out, nil
	}
	wanted := make(map[core.RatingKey]bool, len(keys))
	unique := make([]core.RatingKey, 0, len(keys))
	for _, k := range keys {
		if k > 0 && !wanted[k] {
			wanted[k] = true
			unique = append(unique, k)
		}
	}
	if len(unique) == 0 {
		return out, nil
	}

	var byKey map[core.RatingKey][]*session
	m.mu.Lock()
	for _, sess := range m.sessions {
		if sess != nil && sess.ident.userID == userID && wanted[sess.ident.key] {
			if byKey == nil {
				byKey = map[core.RatingKey][]*session{}
			}
			byKey[sess.ident.key] = append(byKey[sess.ident.key], sess)
		}
	}
	m.mu.Unlock()

	for k, sessions := range byKey {
		var latest *ObservedMedia
		for _, sess := range sessions {
			sess.mu.Lock()
			g, closed, started := sess.cur, sess.closed, sess.startedAt
			sess.mu.Unlock()
			if g == nil || closed {
				continue
			}
			info, ok := persistedProbeFacts(g.infoSnapshot())
			if ok && (latest == nil || started.After(latest.At)) {
				info.Chapters = nil
				latest = &ObservedMedia{Info: info, At: started}
			}
		}
		if latest != nil {
			out[k] = latest
		}
	}

	if m.resume == nil {
		return out, nil
	}
	missing := make([]core.RatingKey, 0, len(unique)-len(out))
	for _, k := range unique {
		if _, have := out[k]; !have {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	persisted, err := m.resume.observedMediaMany(ctx, userID, missing, m.now())
	if err != nil {
		return nil, err
	}
	for k, obs := range persisted {
		out[k] = obs
	}
	return out, nil
}

func decodeObservedMediaRow(raw string, size, updated int64) *ObservedMedia {
	if len(raw) > maxPersistedProbeBytes {
		return nil
	}
	info, ok := decodePersistedProbe(raw)
	if !ok {
		return nil
	}
	facts, ok := persistedProbeFacts(info)
	if !ok || facts.Size != size {
		return nil
	}
	// Artwork consumers only need stream facts, never chapters or external paths.
	facts.Chapters = nil
	return &ObservedMedia{Info: facts, At: time.UnixMilli(updated)}
}

func (s *resumeStore) observedMedia(ctx context.Context, userID int64, key core.RatingKey, now time.Time) (*ObservedMedia, error) {
	var raw string
	var size, updated int64
	err := s.db.QueryRowContext(ctx, `SELECT p.info_json,p.size,p.updated_at FROM playback_probe p
 JOIN playback_resume r ON r.user_id=p.user_id AND r.rating_key=p.rating_key
 AND r.release_id=p.release_id AND r.nzb_hash=p.nzb_hash AND r.size=p.size
 WHERE p.user_id=? AND p.rating_key=? AND p.nzb_hash<>'' AND p.updated_at BETWEEN ? AND ?`,
		userID, key, now.Add(-30*24*time.Hour).UnixMilli(), now.UnixMilli()).Scan(&raw, &size, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeObservedMediaRow(raw, size, updated), nil
}

func (s *resumeStore) observedMediaMany(ctx context.Context, userID int64, keys []core.RatingKey, now time.Time) (map[core.RatingKey]*ObservedMedia, error) {
	out := make(map[core.RatingKey]*ObservedMedia)
	if s == nil || s.db == nil || len(keys) == 0 {
		return out, nil
	}
	lo := now.Add(-30 * 24 * time.Hour).UnixMilli()
	hi := now.UnixMilli()
	const batch = 500
	for chunk := range store.Chunks(keys, batch) {
		args := make([]any, 0, 3+len(chunk))
		args = append(args, userID, lo, hi)
		for _, k := range chunk {
			args = append(args, int64(k))
		}
		rows, err := s.db.QueryContext(ctx, `SELECT p.rating_key,p.info_json,p.size,p.updated_at FROM playback_probe p
 JOIN playback_resume r ON r.user_id=p.user_id AND r.rating_key=p.rating_key
 AND r.release_id=p.release_id AND r.nzb_hash=p.nzb_hash AND r.size=p.size
 WHERE p.user_id=? AND p.nzb_hash<>'' AND p.updated_at BETWEEN ? AND ? AND p.rating_key IN (`+store.Placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var rk, size, updated int64
			var raw string
			if err := rows.Scan(&rk, &raw, &size, &updated); err != nil {
				rows.Close()
				return nil, err
			}
			if obs := decodeObservedMediaRow(raw, size, updated); obs != nil {
				k := core.RatingKey(rk)
				if prev := out[k]; prev == nil || obs.At.After(prev.At) {
					out[k] = obs
				}
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
