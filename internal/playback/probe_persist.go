package playback

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	// Probed media facts for a resume record's release, so a restart does
	// not put a container probe back on the first Play of a title already
	// played. Keyed like playback_index; a row is only ever read for the
	// release a live resume record names.
	store.Register("playback_probe", 1, `CREATE TABLE playback_probe (
	user_id INTEGER NOT NULL, rating_key INTEGER NOT NULL, release_id TEXT NOT NULL,
	nzb_hash TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL DEFAULT 0,
	info_json TEXT NOT NULL, updated_at INTEGER NOT NULL,
	PRIMARY KEY (user_id, rating_key, release_id)
);`)
	store.Register("playback_probe", 2, `ALTER TABLE playback_probe ADD COLUMN file_identity TEXT NOT NULL DEFAULT '';`)
}

// probeFactsRecord is the on-disk shape of playback_probe.info_json.
// Rows written before facts were versioned do not carry FactsVersion and
// are treated as misses so the next open probes again.
type probeFactsRecord struct {
	FactsVersion int            `json:"factsVersion"`
	Info         core.MediaInfo `json:"info"`
}

func decodePersistedProbe(raw string) (core.MediaInfo, bool) {
	var rec probeFactsRecord
	if json.Unmarshal([]byte(raw), &rec) != nil || rec.FactsVersion != core.ProbeFactsVersion {
		return core.MediaInfo{}, false
	}
	return rec.Info, true
}

// Bounds on a persisted probe. Stream/chapter counts of real containers
// are far below these; anything larger is not a probe worth trusting.
const (
	maxPersistedProbeBytes    = 64 << 10
	maxPersistedProbeStreams  = 256
	maxPersistedProbeChapters = 1024
)

// persistedProbeFacts returns the sanitized, persistable form of a probed
// MediaInfo, or ok=false when the info is not an exact probe. External
// (sidecar subtitle) streams are dropped entirely: they are per-viewer
// enrichment, and StreamInfo.ExternalURL can carry an expiring or
// credential-bearing URL that must never reach disk. Keyframes travel in
// playback_index, not here (MediaInfo.Keyframes is `json:"-"` regardless).
func persistedProbeFacts(info core.MediaInfo) (core.MediaInfo, bool) {
	if info.Estimated || info.Size <= 0 || info.Duration <= 0 || len(info.Streams) == 0 {
		return core.MediaInfo{}, false
	}
	if len(info.Streams) > maxPersistedProbeStreams || len(info.Chapters) > maxPersistedProbeChapters {
		return core.MediaInfo{}, false
	}
	out := info
	out.Keyframes = nil
	out.Streams = make([]core.StreamInfo, 0, len(info.Streams))
	for _, s := range info.Streams {
		if s.External {
			continue
		}
		s.ExternalURL = ""
		out.Streams = append(out.Streams, s)
	}
	if len(out.Streams) == 0 || !out.HasPlayableMedia() {
		return core.MediaInfo{}, false
	}
	out.Chapters = append([]core.Chapter(nil), info.Chapters...)
	return out, true
}

func probeInfoMemoKey(userID int64, key core.RatingKey, releaseID, hash string) string {
	return resumeMemoKey(userID, key) + ":probe:" + releaseID + ":" + hash
}

func decodeStoredProbeRow(releaseID, hash string, size int64, raw string) *probeCache {
	if hash == "" || len(raw) > maxPersistedProbeBytes {
		return nil
	}
	info, ok := decodePersistedProbe(raw)
	if !ok {
		return nil // a corrupt or old-generation row is a cache miss
	}
	facts, ok := persistedProbeFacts(info)
	if !ok || facts.Size != size {
		return nil
	}
	return &probeCache{
		releaseID:    releaseID,
		info:         facts,
		needsAugment: true,
		nzbHash:      hash,
	}
}

func cloneProbeCache(src *probeCache) *probeCache {
	if src == nil {
		return nil
	}
	return &probeCache{
		warmReads:    src.warmReads,
		releaseID:    src.releaseID,
		info:         core.CloneMediaInfo(src.info),
		points:       append([]IndexPoint(nil), src.points...),
		needsAugment: src.needsAugment,
		nzbHash:      src.nzbHash,
	}
}

func saveProbeWith(ctx context.Context, exec resumeExecer, userID int64, key core.RatingKey, releaseID string, nzb []byte, info core.MediaInfo, at time.Time) error {
	reqmemo.ForgetPrefix(ctx, resumeMemoKey(userID, key)+":probe:")
	facts, ok := persistedProbeFacts(info)
	if !ok || releaseID == "" || len(nzb) == 0 {
		return nil
	}
	b, err := json.Marshal(probeFactsRecord{FactsVersion: core.ProbeFactsVersion, Info: facts})
	if err != nil {
		return err
	}
	if len(b) > maxPersistedProbeBytes {
		return nil
	}
	_, err = exec.ExecContext(ctx, `INSERT INTO playback_probe(user_id,rating_key,release_id,nzb_hash,size,info_json,updated_at,file_identity) VALUES(?,?,?,?,?,?,?,?)
ON CONFLICT(user_id,rating_key,release_id) DO UPDATE SET nzb_hash=excluded.nzb_hash, size=excluded.size, info_json=excluded.info_json, updated_at=excluded.updated_at, file_identity=excluded.file_identity`,
		userID, int64(key), releaseID, nzbHash(nzb), facts.Size, string(b), at.UnixMilli(), torrentProbeIdentity(nzb))
	if err != nil {
		return err
	}
	// Keep a small per-title cache across Play Version switches. A resume
	// still owns the title's lifetime; these facts own no source or payload.
	_, err = exec.ExecContext(ctx, `DELETE FROM playback_probe WHERE user_id=? AND rating_key=? AND release_id NOT IN
 (SELECT release_id FROM playback_probe WHERE user_id=? AND rating_key=? ORDER BY updated_at DESC, release_id DESC LIMIT 4)`,
		userID, int64(key), userID, int64(key))
	return err
}

// loadProbeInfoHash loads persisted stream/container facts without querying
// playback_index keyframes. Callers that only check codec compatibility or
// render track lists avoid reading keyframe blobs.
func (s *resumeStore) loadProbeInfoHash(ctx context.Context, userID int64, key core.RatingKey, releaseID, hash string) (*probeCache, error) {
	if hash == "" {
		return nil, nil
	}
	cached, err := reqmemo.Do(ctx, probeInfoMemoKey(userID, key, releaseID, hash), func() (*probeCache, error) {
		var (
			raw  string
			size int64
		)
		err := s.db.QueryRowContext(ctx, `SELECT size, info_json FROM playback_probe WHERE user_id=? AND rating_key=? AND release_id=? AND nzb_hash=?`,
			userID, int64(key), releaseID, hash).Scan(&size, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return decodeStoredProbeRow(releaseID, hash, size, raw), nil
	})
	if err != nil || cached == nil {
		return nil, err
	}
	return cloneProbeCache(cached), nil
}

// loadProbeHash is loadProbe for the payload with this hash.
func (s *resumeStore) loadProbeHash(ctx context.Context, userID int64, key core.RatingKey, releaseID, hash string) (*probeCache, error) {
	facts, err := s.loadProbeInfoHash(ctx, userID, key, releaseID, hash)
	if err != nil || facts == nil {
		return nil, err
	}
	points, err := s.loadIndex(ctx, userID, key, releaseID)
	if err != nil {
		return nil, err
	}
	facts.points = indexPointsOf(points)
	return facts, nil
}

// persistedProbe is loadProbe as a cache lookup: any failure is a miss.
func (m *Manager) persistedProbe(ctx context.Context, userID int64, key core.RatingKey, releaseID string, nzb []byte) *probeCache {
	if len(nzb) == 0 {
		if m.resume == nil || releaseID == "" {
			return nil
		}
		return m.resume.liveProbeHash(userID, key, releaseID, "")
	}
	hash := nzbHash(nzb)
	if facts := m.persistedProbeHash(ctx, userID, key, releaseID, hash); facts != nil {
		return facts
	}
	// A renewed signed link changes the descriptor hash, not the torrent's
	// immutable file. Keep payload/revision hashes byte-exact everywhere else.
	identity := torrentProbeIdentity(nzb)
	if m.resume == nil || releaseID == "" || identity == "" {
		return nil
	}
	var storedHash string
	if err := m.resume.db.QueryRowContext(ctx, `SELECT nzb_hash FROM playback_probe WHERE user_id=? AND rating_key=? AND release_id=? AND file_identity=?`, userID, int64(key), releaseID, identity).Scan(&storedHash); err != nil {
		return nil
	}
	facts, err := m.resume.loadProbeHash(ctx, userID, key, releaseID, storedHash)
	if err != nil || facts == nil {
		return nil
	}
	facts.nzbHash = hash // probeGeneration still binds reuse to this opened payload and size
	m.log.Debug("reusing torrent file probe after link renewal", "key", key, "release", releaseID)
	return facts
}

// persistedProbeInfoHash returns persisted stream/container facts without
// loading playback_index keyframes.
func (m *Manager) persistedProbeInfoHash(ctx context.Context, userID int64, key core.RatingKey, releaseID, hash string) *probeCache {
	if m.resume == nil || releaseID == "" {
		return nil
	}
	if live := m.resume.liveProbeHash(userID, key, releaseID, hash); live != nil {
		return live
	}
	facts, err := m.resume.loadProbeInfoHash(ctx, userID, key, releaseID, hash)
	if err != nil {
		m.log.Debug("persisted probe info lookup failed", "key", key, "release", releaseID, "err", err)
		return nil
	}
	return facts
}

// persistedProbeHash is persistedProbe for the payload with this hash.
func (m *Manager) persistedProbeHash(ctx context.Context, userID int64, key core.RatingKey, releaseID, hash string) *probeCache {
	if m.resume == nil || releaseID == "" {
		return nil
	}
	if live := m.resume.liveProbeHash(userID, key, releaseID, hash); live != nil {
		return live
	}
	facts, err := m.resume.loadProbeHash(ctx, userID, key, releaseID, hash)
	if err != nil {
		m.log.Debug("persisted probe lookup failed", "key", key, "release", releaseID, "err", err)
		return nil
	}
	if facts != nil {
		m.log.Debug("reusing persisted probe", "key", key, "release", releaseID, "keyframes", len(facts.points))
	}
	return facts
}

// CachedVariantInfo reads only local probe facts for a release already offered
// to this viewer. It never resolves or opens media. Opening still validates the
// payload hash and size before reusing a persisted probe.
func (m *Manager) CachedVariantInfo(ctx context.Context, userID int64, key core.RatingKey, releaseID string) (core.MediaInfo, bool) {
	if m.resume == nil || userID <= 0 || releaseID == "" {
		return core.MediaInfo{}, false
	}
	if live := m.resume.liveProbeHash(userID, key, releaseID, ""); live != nil {
		return core.CloneMediaInfo(live.info), true
	}
	var raw string
	if err := m.resume.db.QueryRowContext(ctx, "SELECT info_json FROM playback_probe WHERE user_id=? AND rating_key=? AND release_id=?", userID, int64(key), releaseID).Scan(&raw); err != nil || len(raw) > maxPersistedProbeBytes {
		return core.MediaInfo{}, false
	}
	info, ok := decodePersistedProbe(raw)
	if !ok {
		return core.MediaInfo{}, false
	}
	return persistedProbeFacts(info)
}

// cachedVariantProbes loads the bounded per-viewer playback_probe set (up to 4
// releases per title) in a single request-memoized query.
func (m *Manager) cachedVariantProbes(ctx context.Context, userID int64, key core.RatingKey) map[string]core.MediaInfo {
	if m.resume == nil || userID <= 0 || key == 0 {
		return nil
	}
	got, err := reqmemo.Do(ctx, resumeMemoKey(userID, key)+":probe:all", func() (map[string]core.MediaInfo, error) {
		rows, err := m.resume.db.QueryContext(ctx, `SELECT release_id, info_json FROM playback_probe WHERE user_id=? AND rating_key=?`, userID, int64(key))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := map[string]core.MediaInfo{}
		for rows.Next() {
			var relID, raw string
			if err := rows.Scan(&relID, &raw); err != nil || relID == "" || len(raw) > maxPersistedProbeBytes {
				continue
			}
			if info, ok := decodePersistedProbe(raw); ok {
				if facts, ok := persistedProbeFacts(info); ok {
					out[relID] = facts
				}
			}
		}
		return out, rows.Err()
	})
	if err != nil {
		return nil
	}
	return got
}

func probeReleaseForTitle(rel core.Release, title *core.Title) core.Release {
	if title == nil || title.Type != core.TypeEpisode || title.Episode <= 0 {
		return rel
	}
	if rel.Protocol != core.ProtocolTorrent && strings.TrimSpace(rel.InfoHash) == "" {
		return rel
	}
	if rel.HasFileOrEpisodeScope() {
		return rel
	}
	t := core.ReleaseTarget{Season: title.Season, Episode: title.Episode}
	if rel.Target != nil {
		t = *rel.Target
		t.Season, t.Episode = title.Season, title.Episode
	}
	rel.Target = &t
	return rel
}

// WithCachedVariantInfo enriches only the bounded list of offered versions.
// It checks what Ether already knows first (durable content-ID probes, NZB
// vault tracks, per-viewer playback probes, and already-fetched debrid file
// metadata) without performing any indexer or network calls.
func (m *Manager) WithCachedVariantInfo(ctx context.Context, user *core.User, title *core.Title, variants []core.ReleaseVariant) []core.ReleaseVariant {
	if title == nil {
		return variants
	}
	out := append([]core.ReleaseVariant(nil), variants...)
	var viewerProbes map[string]core.MediaInfo
	viewerProbesLoaded := false
	for i := range out {
		// Menu identity may be a logical slot; probes belong to its concrete source.
		release := out[i].Release.ID
		if release == "" {
			release = out[i].ID
		}
		placeholder := release == "" || strings.HasPrefix(release, core.VersionSelectorPrefix)
		if !placeholder {
			probeRel := probeReleaseForTitle(out[i].Release, title)
			// Content-ID probe cache and vault tracks work across viewers and
			// indexers in O(1) memory without touching the database.
			if info, ok := m.CachedReleaseProbe(probeRel); ok {
				out[i].Info = info
			} else if user != nil && user.ID > 0 {
				var found bool
				if m.resume != nil {
					if live := m.resume.liveProbeHash(user.ID, title.Key, release, ""); live != nil {
						out[i].Info = core.CloneMediaInfo(live.info)
						found = true
					}
				}
				if !found {
					if !viewerProbesLoaded {
						viewerProbes = m.cachedVariantProbes(ctx, user.ID, title.Key)
						viewerProbesLoaded = true
					}
					if info, ok := viewerProbes[release]; ok {
						out[i].Info = core.CloneMediaInfo(info)
						found = true
					}
				}
				if !found && m.deps.DebridFileMeta != nil {
					if filePath, size, ok := m.deps.DebridFileMeta(probeRel); ok {
						enrichEstimatedFromDebridMeta(&out[i], filePath, size)
					}
				}
			} else if m.deps.DebridFileMeta != nil {
				if filePath, size, ok := m.deps.DebridFileMeta(probeRel); ok {
					enrichEstimatedFromDebridMeta(&out[i], filePath, size)
				}
			}
			if !out[i].Info.Estimated && out[i].Info.Size > 0 && out[i].Release.Size <= 0 {
				out[i].Release.Size = out[i].Info.Size
			}
		}
		if out[i].Availability != core.VersionUnavailable {
			relID := out[i].Release.ID
			var uid int64
			if user != nil {
				uid = user.ID
			}
			if relID != "" && relID != out[i].ID && m.hasLiveOrCachedRelease(uid, title.Key, relID) {
				out[i].Availability = core.VersionInstant
				out[i].AvailabilityNote = ""
			} else if !out[i].Info.Estimated && len(out[i].Info.Streams) > 0 && (out[i].Availability == "" || out[i].Availability == core.VersionFetch) {
				out[i].Availability = core.VersionCached
				out[i].AvailabilityNote = ""
			}
		}
	}
	return out
}

// torrentProbeIdentity deliberately excludes transport URLs, expiry and account
// torrent IDs. Direct HTTP/addon links have no immutable content identity and
// continue to require the complete descriptor hash. File paths and sizes remain
// exact, and facts remain scoped to the viewer, title and selected release.
func torrentProbeIdentity(payload []byte) string {
	if !bytes.HasPrefix(payload, []byte(debridPayloadPrefix)) {
		return ""
	}
	var p struct {
		InfoHash string `json:"infoHash"`
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		Kind     string `json:"kind"`
		Provider string `json:"provider"`
	}
	if json.Unmarshal(payload[len(debridPayloadPrefix):], &p) != nil || p.Kind == "http" || p.Kind == "" || len(p.InfoHash) != 40 || p.Path == "" || p.Size <= 0 {
		return ""
	}
	p.InfoHash = strings.ToLower(p.InfoHash)
	for _, r := range p.InfoHash {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return ""
		}
	}
	encoded, _ := json.Marshal(p)
	return nzbHash(encoded)
}
