package vault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("vault", 1, `
CREATE TABLE vault_releases(key TEXT PRIMARY KEY,kind TEXT NOT NULL,name TEXT NOT NULL,title_key TEXT NOT NULL,tmdb BIGINT NOT NULL,year INTEGER NOT NULL,season INTEGER NOT NULL,episode INTEGER NOT NULL,size BIGINT NOT NULL,segments INTEGER NOT NULL,pack TEXT NOT NULL,off BIGINT NOT NULL,len INTEGER NOT NULL,crc BIGINT NOT NULL,holders TEXT NOT NULL,source TEXT NOT NULL,added BIGINT NOT NULL,last_used BIGINT NOT NULL,uses INTEGER NOT NULL DEFAULT 1,health INTEGER NOT NULL DEFAULT 0,health_at BIGINT NOT NULL DEFAULT 0,dead INTEGER NOT NULL DEFAULT 0,private INTEGER NOT NULL,profile_fp TEXT NOT NULL,avoided BIGINT NOT NULL DEFAULT 0,searches_avoided BIGINT NOT NULL DEFAULT 0,info TEXT NOT NULL);
CREATE TABLE vault_aliases(indexer_id TEXT NOT NULL,release_id TEXT NOT NULL,key TEXT NOT NULL,PRIMARY KEY(indexer_id,release_id));
CREATE TABLE vault_members(key TEXT NOT NULL,member INTEGER NOT NULL,season INTEGER NOT NULL,episode INTEGER NOT NULL,size BIGINT NOT NULL,PRIMARY KEY(key,member));
CREATE TABLE vault_copies(key TEXT NOT NULL,node TEXT NOT NULL,pack TEXT NOT NULL,off BIGINT NOT NULL,len INTEGER NOT NULL,crc BIGINT NOT NULL,PRIMARY KEY(key,node));
CREATE INDEX vault_title ON vault_releases(title_key,season,episode);
CREATE INDEX vault_tmdb ON vault_releases(tmdb);
CREATE INDEX vault_members_ep ON vault_members(season,episode);
`)
	store.Register("vault", 2, `
ALTER TABLE vault_releases ADD COLUMN cache_id TEXT NOT NULL DEFAULT '';
CREATE INDEX vault_cache_id ON vault_releases(cache_id);
CREATE INDEX vault_alias_key ON vault_aliases(key,indexer_id,release_id);
UPDATE vault_releases SET cache_id=COALESCE((SELECT MIN(a.release_id) FROM vault_aliases a WHERE a.key=vault_releases.key AND a.indexer_id=vault_releases.source AND a.indexer_id<>'legacy:nzb'),'') WHERE kind='usenet' AND info NOT LIKE '%"fullSeason":true%';
`)
	store.Register("vault", 3, `
CREATE TABLE vault_tracks(
	key TEXT NOT NULL,
	season INTEGER NOT NULL DEFAULT 0,
	episode INTEGER NOT NULL DEFAULT 0,
	info_json TEXT NOT NULL,
	updated_at BIGINT NOT NULL,
	PRIMARY KEY(key,season,episode)
);
CREATE INDEX vault_tracks_updated ON vault_tracks(updated_at);
`)
}

const columns = "key,kind,name,title_key,tmdb,year,season,episode,size,segments,source,added,last_used,uses,health,health_at,private,profile_fp,info,cache_id"

func scan(row interface{ Scan(...any) error }) (Entry, error) {
	var x Entry
	var added, used, health int64
	var raw string
	var private int
	e := row.Scan(&x.Key, &x.Kind, &x.Name, &x.TitleKey, &x.TMDB, &x.Year, &x.Season, &x.Episode, &x.Size, &x.Segments, &x.Source, &added, &used, &x.Uses, &x.Health, &health, &private, &x.ProfileFP, &raw, &x.CacheID)
	if errors.Is(e, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	if e != nil {
		return x, e
	}
	x.Private = private != 0
	x.Added = time.Unix(added, 0)
	x.LastUsed = time.Unix(used, 0)
	if health != 0 {
		x.HealthAt = time.Unix(health, 0)
	}
	e = json.Unmarshal([]byte(raw), &x.Info)
	return x, e
}
func (v *library) Entry(ctx context.Context, k Key) (Entry, error) {
	return scan(v.DB.QueryRowContext(ctx, "SELECT "+columns+" FROM vault_releases WHERE key=? AND dead=0", k))
}
func (v *library) entries(ctx context.Context, where string, args ...any) ([]Entry, error) {
	rows, e := v.DB.QueryContext(ctx, "SELECT "+columns+" FROM vault_releases r WHERE dead=0 AND "+where, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		x, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (v *library) Search(ctx context.Context, r core.SearchRequest) ([]Entry, error) {
	title := releaseparse.NormalizeTitle(r.Title)
	q := "((tmdb=? AND tmdb<>0) OR (title_key=? AND title_key<>'' AND (tmdb=0 OR ?=0)))"
	a := []any{r.IDs.TMDB, title, r.IDs.TMDB}
	if r.Episode > 0 {
		q += " AND ((season=? AND episode=?) OR EXISTS(SELECT 1 FROM vault_members m WHERE m.key=r.key AND m.season=? AND m.episode=?))"
		a = append(a, r.Season, r.Episode, r.Season, r.Episode)
	} else {
		q += " AND season=0 AND episode=0 AND (?=0 OR year=? OR (tmdb<>0 AND tmdb=?))"
		a = append(a, r.Year, r.Year, r.IDs.TMDB)
	}
	return v.entries(ctx, q+" ORDER BY last_used DESC,key", a...)
}
func (v *library) List(ctx context.Context, q string, k Kind, h *Health, limit int, cursor string) ([]Entry, error) {
	where := "key>?"
	a := []any{cursor}
	if q != "" {
		where += " AND lower(name) LIKE ?"
		a = append(a, "%"+strings.ToLower(q)+"%")
	}
	if k != "" {
		where += " AND kind=?"
		a = append(a, k)
	}
	if h != nil {
		where += " AND health=?"
		a = append(a, *h)
	}
	if limit <= 0 {
		limit = 100
	}
	return v.entries(ctx, where+" ORDER BY key LIMIT ?", append(a, limit)...)
}
func (v *library) ByAlias(ctx context.Context, i, id string) (Key, bool, error) {
	var k Key
	e := v.DB.QueryRowContext(ctx, `SELECT a.key FROM vault_aliases a JOIN vault_releases r ON a.key=r.key WHERE a.indexer_id=? AND a.release_id=? AND r.dead=0`, i, id).Scan(&k)
	if errors.Is(e, sql.ErrNoRows) {
		return "", false, nil
	}
	return k, e == nil, e
}
func (v *library) Kept(ctx context.Context, rels []core.Release) (map[string]bool, error) {
	keys, err := v.KeysFor(ctx, rels)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(keys))
	for id := range keys {
		out[id] = true
	}
	return out, nil
}

func (v *library) KeysFor(ctx context.Context, rels []core.Release) (map[string]Key, error) {
	out := map[string]Key{}
	if len(rels) == 0 {
		return out, nil
	}
	var vals []string
	var a []any
	for _, r := range rels {
		i := r.IndexerID
		if i == "" {
			i = LegacyAliasIndexer
		}
		base := r.ID
		if r.Target != nil && r.Target.PackID != "" {
			base = r.Target.PackID
		}
		k := KeyOf(r)
		if k == "" && strings.HasPrefix(r.ContentID, "nzb:") {
			k = Key(r.ContentID)
		}
		if k == "" && r.Protocol == core.ProtocolTorrent && strings.TrimSpace(r.InfoHash) != "" {
			k = TorrentKey(r.InfoHash)
		}
		vals = append(vals, "(CAST(? AS TEXT),CAST(? AS TEXT),CAST(? AS TEXT),CAST(? AS TEXT))")
		a = append(a, r.ID, i, k, base)
	}
	rows, e := v.DB.QueryContext(ctx, "WITH candidates(id,idx,k,base) AS (VALUES "+strings.Join(vals, ",")+`) SELECT c.id, r.key FROM candidates c LEFT JOIN vault_aliases a ON a.indexer_id=c.idx AND (a.release_id=c.id OR (c.base<>'' AND a.release_id=c.base)) JOIN vault_releases r ON r.key=CASE WHEN c.k<>'' THEN c.k ELSE a.key END OR (c.idx='vault' AND c.k='' AND r.cache_id=c.base) WHERE r.dead=0`, a...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var k Key
		if e = rows.Scan(&id, &k); e != nil {
			return nil, e
		}
		if k != "" {
			out[id] = k
		}
	}
	return out, rows.Err()
}

type vaultTracksRecord struct {
	FactsVersion int            `json:"factsVersion"`
	Info         core.MediaInfo `json:"info"`
}

func sanitizeVaultMedia(info core.MediaInfo) core.MediaInfo {
	c := core.CloneMediaInfo(info)
	c.Estimated = false
	c.Keyframes = nil
	if len(c.Streams) > 0 {
		embedded := make([]core.StreamInfo, 0, len(c.Streams))
		for _, st := range c.Streams {
			if st.External {
				continue
			}
			embedded = append(embedded, st)
		}
		c.Streams = embedded
	}
	return c
}

func decodeVaultMedia(raw string) (core.MediaInfo, bool) {
	if raw == "" {
		return core.MediaInfo{}, false
	}
	var rec vaultTracksRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return core.MediaInfo{}, false
	}
	if rec.FactsVersion != core.ProbeFactsVersion || rec.Info.Estimated || rec.Info.Video() == nil {
		return core.MediaInfo{}, false
	}
	return rec.Info, true
}

func (v *library) preloadTracks(ctx context.Context) {
	if v.DB == nil {
		return
	}
	rows, err := v.DB.QueryContext(ctx, `SELECT key, season, episode, info_json FROM vault_tracks ORDER BY updated_at DESC LIMIT ?`, maxVaultTrackCache+1)
	if err != nil {
		return
	}
	defer rows.Close()
	loaded := make(map[trackKey]core.MediaInfo)
	count := 0
	for rows.Next() {
		var k Key
		var season, episode int
		var raw string
		if err := rows.Scan(&k, &season, &episode, &raw); err != nil {
			continue
		}
		count++
		if count > maxVaultTrackCache {
			break
		}
		if info, ok := decodeVaultMedia(raw); ok {
			loaded[trackKey{key: k, season: season, episode: episode}] = info
		}
	}
	if rows.Err() != nil {
		return
	}
	v.trackMu.Lock()
	v.tracks = loaded
	v.tracksLoaded = count <= maxVaultTrackCache
	v.trackMu.Unlock()
}

func (v *library) Tracks(ctx context.Context, key Key, season, episode int) (core.MediaInfo, bool, error) {
	if key == "" {
		return core.MediaInfo{}, false, nil
	}
	tk := trackKey{key: key, season: season, episode: episode}
	v.trackMu.RLock()
	if v.tracks != nil {
		if info, ok := v.tracks[tk]; ok {
			v.trackMu.RUnlock()
			return core.CloneMediaInfo(info), true, nil
		}
	}
	complete := v.tracksLoaded
	v.trackMu.RUnlock()
	if complete || v.DB == nil {
		return core.MediaInfo{}, false, nil
	}
	var raw string
	err := v.DB.QueryRowContext(ctx, `SELECT info_json FROM vault_tracks WHERE key=? AND season=? AND episode=?`, key, season, episode).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return core.MediaInfo{}, false, nil
	}
	if err != nil {
		return core.MediaInfo{}, false, err
	}
	info, ok := decodeVaultMedia(raw)
	if !ok {
		return core.MediaInfo{}, false, nil
	}
	v.trackMu.Lock()
	if v.tracks == nil {
		v.tracks = make(map[trackKey]core.MediaInfo)
	}
	if len(v.tracks) < maxVaultTrackCache {
		v.tracks[tk] = core.CloneMediaInfo(info)
	}
	v.trackMu.Unlock()
	return info, true, nil
}

func (v *library) SetTracks(ctx context.Context, key Key, season, episode int, info core.MediaInfo) error {
	if key == "" || info.Estimated || info.Video() == nil {
		return nil
	}
	clean := sanitizeVaultMedia(info)
	raw, err := json.Marshal(vaultTracksRecord{FactsVersion: core.ProbeFactsVersion, Info: clean})
	if err != nil {
		return err
	}
	if _, err := v.DB.ExecContext(ctx, `INSERT INTO vault_tracks(key,season,episode,info_json,updated_at) VALUES(?,?,?,?,?)
		ON CONFLICT(key,season,episode) DO UPDATE SET info_json=excluded.info_json,updated_at=excluded.updated_at`,
		key, season, episode, string(raw), v.Now().Unix()); err != nil {
		return err
	}
	tk := trackKey{key: key, season: season, episode: episode}
	v.trackMu.Lock()
	if v.tracks == nil {
		v.tracks = make(map[trackKey]core.MediaInfo)
	}
	if _, exists := v.tracks[tk]; !exists && len(v.tracks) >= maxVaultTrackCache {
		v.tracksLoaded = false
	} else {
		v.tracks[tk] = clean
	}
	v.trackMu.Unlock()
	return nil
}
func (v *library) update(ctx context.Context, k Key, set string, a ...any) error {
	r, e := v.DB.ExecContext(ctx, "UPDATE vault_releases SET "+set+" WHERE key=? AND dead=0", append(a, k)...)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if n == 0 && e == nil {
		return ErrNotFound
	}
	return e
}
func (v *library) MarkHealth(ctx context.Context, k Key, h Health) error {
	if err := v.update(ctx, k, "health=?,health_at=?", h, v.Now().Unix()); err != nil {
		return err
	}
	if h == HealthDead && v.Events != nil {
		var kind, titleKey, releaseName string
		if v.DB != nil {
			_ = v.DB.QueryRowContext(ctx, `SELECT kind, title_key, name FROM vault_releases WHERE key=?`, k).Scan(&kind, &titleKey, &releaseName)
		}
		v.publishEvent(events.EventVaultRecordDead, events.VaultRecordDeadPayload{
			VaultKey:    string(k),
			Kind:        kind,
			TitleKey:    titleKey,
			ReleaseName: releaseName,
			Reason:      "health_check_failed",
		}, events.WithCorrelation(events.Correlation{TitleID: titleKey}))
	}
	return nil
}
func (v *library) MarkChosen(ctx context.Context, k Key, fp string) error {
	return v.update(ctx, k, "profile_fp=?", fp)
}
func flag(b bool) int {
	if b {
		return 1
	}
	return 0
}
func (v *library) Touch(ctx context.Context, k Key, avoided bool) error {
	return v.update(ctx, k, "uses=uses+1,last_used=?,avoided=avoided+?", v.Now().Unix(), flag(avoided))
}
func (v *library) Purge(ctx context.Context, k Key) error {
	if err := v.update(ctx, k, "dead=1"); err != nil {
		return err
	}
	_, _ = v.DB.ExecContext(ctx, `DELETE FROM vault_tracks WHERE key=?`, k)
	v.trackMu.Lock()
	for tk := range v.tracks {
		if tk.key == k {
			delete(v.tracks, tk)
		}
	}
	v.trackMu.Unlock()
	return nil
}
func (v *library) Stats(ctx context.Context) (Stats, error) {
	s := Stats{CapBytes: v.capBytes.Load()}
	e := v.DB.QueryRowContext(ctx, `SELECT count(CASE WHEN dead=0 THEN 1 END),COALESCE(sum(CASE WHEN dead=0 THEN len ELSE 0 END),0),COALESCE(sum(avoided),0),COALESCE(sum(searches_avoided),0) FROM vault_releases`).Scan(&s.Releases, &s.Bytes, &s.GrabsAvoided, &s.SearchesAvoided)
	return s, e
}

func (v *library) SearchAvoided(ctx context.Context, k Key) error {
	return v.update(ctx, k, "searches_avoided=searches_avoided+1")
}
