// Package releasedb indexe en arrière-plan les releases disponibles pour les
// titres du catalogue et les conserve en base (ajout Private Streaming
// Enhanced).
//
// Les recherches passent par le gestionnaire d'indexeurs d'Ether : addons
// Stremio (AIOStreams, Torrentio, Comet… configurés avec AllDebrid) et, sur
// demande explicite, indexeurs Newznab/Torznab (Prowlarr, Jackett,
// NZBHydra2). Aucun appel n'est fait à l'API AllDebrid : rien n'est ajouté
// au compte debrid. Toutes les releases trouvées sont conservées ; les
// filtres de langue, résolution et codec s'appliquent à la lecture.
package releasedb

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_releases", 1, `
CREATE TABLE pse_releases(
  id TEXT PRIMARY KEY,
  title_key INTEGER NOT NULL,
  media_type TEXT NOT NULL,
  season INTEGER NOT NULL DEFAULT 0,
  episode INTEGER NOT NULL DEFAULT 0,
  source_id TEXT NOT NULL,
  source_name TEXT NOT NULL,
  protocol TEXT NOT NULL DEFAULT '',
  name TEXT NOT NULL,
  size INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  video_codec TEXT NOT NULL DEFAULT '',
  audio_codec TEXT NOT NULL DEFAULT '',
  hdr INTEGER NOT NULL DEFAULT 0,
  dolby_vision INTEGER NOT NULL DEFAULT 0,
  languages TEXT NOT NULL DEFAULT '',
  subtitle_languages TEXT NOT NULL DEFAULT '',
  debrid_cached TEXT NOT NULL DEFAULT '',
  first_seen INTEGER NOT NULL,
  last_seen INTEGER NOT NULL
);
CREATE INDEX pse_releases_title ON pse_releases(title_key);
CREATE TABLE pse_index_titles(
  title_key INTEGER PRIMARY KEY,
  media_type TEXT NOT NULL,
  last_indexed INTEGER NOT NULL DEFAULT 0,
  next_due INTEGER NOT NULL DEFAULT 0,
  found INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT ''
);
CREATE TABLE pse_index_budget(
  day TEXT NOT NULL,
  source_id TEXT NOT NULL,
  hits INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(day, source_id)
);
CREATE TABLE pse_index_cursor(
  media_type TEXT PRIMARY KEY,
  next_offset INTEGER NOT NULL DEFAULT 0
);
`)
}

// Store lit et écrit les tables pse_*.
type Store struct {
	db  *sql.DB
	now func() time.Time // horloge des calculs de fraîcheur
}

// NewStore enveloppe la base d'Ether (migrations déjà appliquées).
func NewStore(db *sql.DB) *Store { return &Store{db: db, now: time.Now} }

// Release est une release indexée.
type Release struct {
	ID                string
	TitleKey          core.RatingKey
	Type              core.MediaType
	Season, Episode   int
	SourceID          string
	SourceName        string
	Protocol          string
	Name              string
	Size              int64
	Height            int
	VideoCodec        string
	AudioCodec        string
	HDR, DolbyVision  bool
	Languages         []string
	SubtitleLanguages []string
	DebridCached      []string
	FirstSeen         time.Time
	LastSeen          time.Time
}

// FromCore convertit une release d'Ether, trouvée sur source pour le titre t.
func FromCore(t core.Title, season, episode int, sourceID string, r core.Release, cached []string) Release {
	// Une même release (même lien) peut être renvoyée pour plusieurs titres
	// par un addon mal configuré : l'identifiant inclut donc le titre.
	ref := r.ID
	if ref == "" {
		ref = r.GUID
	}
	if ref == "" {
		ref = r.Name
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("%s\x00%d\x00%d\x00%d\x00%s", sourceID, t.Key, season, episode, ref)))
	id := hex.EncodeToString(sum[:])
	return Release{
		ID: id, TitleKey: t.Key, Type: t.Type, Season: season, Episode: episode,
		SourceID: sourceID, SourceName: r.Indexer, Protocol: protocolName(r.Protocol), Name: r.Name, Size: r.Size,
		Height: r.Info.Height, VideoCodec: r.Info.VideoCodec, AudioCodec: r.Info.AudioCodec,
		HDR: r.Info.HDR10, DolbyVision: r.Info.DolbyVision,
		Languages: releaseLanguages(r.Name, r.Info.Languages), SubtitleLanguages: lower(r.Info.SubtitleLanguages), DebridCached: cached,
	}
}

func protocolName(p string) string {
	if p == "" {
		return "usenet"
	}
	return p
}

func lower(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func join(in []string) string { return strings.Join(in, ",") }
func split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Save enregistre ou rafraîchit des releases (première observation conservée).
func (s *Store) Save(ctx context.Context, rs []Release, now time.Time) error {
	if len(rs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveTx(ctx, tx, rs, now); err != nil {
		return err
	}
	return tx.Commit()
}

func saveTx(ctx context.Context, tx *sql.Tx, rs []Release, now time.Time) error {
	for _, r := range rs {
		_, err := tx.ExecContext(ctx, `INSERT INTO pse_releases(id,title_key,media_type,season,episode,source_id,source_name,protocol,name,size,height,video_codec,audio_codec,hdr,dolby_vision,languages,subtitle_languages,debrid_cached,first_seen,last_seen)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET title_key=excluded.title_key,season=excluded.season,episode=excluded.episode,source_name=excluded.source_name,protocol=excluded.protocol,name=excluded.name,size=excluded.size,height=excluded.height,video_codec=excluded.video_codec,audio_codec=excluded.audio_codec,hdr=excluded.hdr,dolby_vision=excluded.dolby_vision,languages=excluded.languages,subtitle_languages=excluded.subtitle_languages,debrid_cached=excluded.debrid_cached,last_seen=excluded.last_seen`,
			r.ID, int64(r.TitleKey), string(r.Type), r.Season, r.Episode, r.SourceID, r.SourceName, r.Protocol, r.Name, r.Size, r.Height,
			r.VideoCodec, r.AudioCodec, boolInt(r.HDR), boolInt(r.DolbyVision), join(r.Languages), join(r.SubtitleLanguages), join(r.DebridCached),
			now.Unix(), now.Unix())
		if err != nil {
			return err
		}
	}
	return nil
}

// Releases renvoie les releases indexées d'un titre qui respectent f, les
// plus récemment vues en premier.
func (s *Store) Releases(ctx context.Context, key core.RatingKey, f Filter) ([]Release, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,title_key,media_type,season,episode,source_id,source_name,protocol,name,size,height,video_codec,audio_codec,hdr,dolby_vision,languages,subtitle_languages,debrid_cached,first_seen,last_seen
FROM pse_releases WHERE title_key=? ORDER BY last_seen DESC, name`, int64(key))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Release
	for rows.Next() {
		var r Release
		var key int64
		var typ, langs, subs, cached string
		var hdr, dv int
		var first, last int64
		if err := rows.Scan(&r.ID, &key, &typ, &r.Season, &r.Episode, &r.SourceID, &r.SourceName, &r.Protocol, &r.Name, &r.Size, &r.Height,
			&r.VideoCodec, &r.AudioCodec, &hdr, &dv, &langs, &subs, &cached, &first, &last); err != nil {
			return nil, err
		}
		r.TitleKey, r.Type = core.RatingKey(key), core.MediaType(typ)
		r.HDR, r.DolbyVision = hdr != 0, dv != 0
		r.Languages, r.SubtitleLanguages, r.DebridCached = split(langs), split(subs), split(cached)
		r.FirstSeen, r.LastSeen = time.Unix(first, 0), time.Unix(last, 0)
		if f.Match(r) {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// Count renvoie le nombre de releases distinctes d'un titre qui respectent f.
func (s *Store) Count(ctx context.Context, key core.RatingKey, f Filter) (int, error) {
	rs, err := s.Releases(ctx, key, f)
	return len(rs), err
}

// Stats résume l'indexation.
type Stats struct {
	Titles, TitlesWithReleases, Releases int
	Due                                  int
}

// Stats compte titres et releases indexés.
func (s *Store) Stats(ctx context.Context, now time.Time) (Stats, error) {
	var st Stats
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN found>0 THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN next_due<=? THEN 1 ELSE 0 END),0) FROM pse_index_titles`, now.Unix()).Scan(&st.Titles, &st.TitlesWithReleases, &st.Due)
	if err != nil {
		return st, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_releases`).Scan(&st.Releases)
	return st, err
}

// dueAt renvoie l'échéance de réindexation d'un titre (zéro : jamais indexé).
func (s *Store) dueAt(ctx context.Context, key core.RatingKey) (time.Time, error) {
	var due int64
	err := s.db.QueryRowContext(ctx, `SELECT next_due FROM pse_index_titles WHERE title_key=?`, int64(key)).Scan(&due)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(due, 0), nil
}

func (s *Store) markIndexed(ctx context.Context, t core.Title, found int, status string, now, next time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO pse_index_titles(title_key,media_type,last_indexed,next_due,found,status) VALUES(?,?,?,?,?,?)
ON CONFLICT(title_key) DO UPDATE SET media_type=excluded.media_type,last_indexed=excluded.last_indexed,next_due=excluded.next_due,found=excluded.found,status=excluded.status`,
		int64(t.Key), string(t.Type), now.Unix(), next.Unix(), found, status)
	return err
}

func (s *Store) cursor(ctx context.Context, typ core.MediaType) (int, error) {
	var off int
	err := s.db.QueryRowContext(ctx, `SELECT next_offset FROM pse_index_cursor WHERE media_type=?`, string(typ)).Scan(&off)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return off, err
}

func (s *Store) setCursor(ctx context.Context, typ core.MediaType, off int) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO pse_index_cursor(media_type,next_offset) VALUES(?,?) ON CONFLICT(media_type) DO UPDATE SET next_offset=excluded.next_offset`, string(typ), off)
	return err
}

// spend réserve n requêtes du jour (UTC) pour une source ; false si la
// limite quotidienne serait dépassée.
func (s *Store) spend(ctx context.Context, source string, n, limit int, now time.Time) (bool, error) {
	day := now.UTC().Format("2006-01-02")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var used int
	err = tx.QueryRowContext(ctx, `SELECT hits FROM pse_index_budget WHERE day=? AND source_id=?`, day, source).Scan(&used)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if used+n > limit {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pse_index_budget(day,source_id,hits) VALUES(?,?,?) ON CONFLICT(day,source_id) DO UPDATE SET hits=pse_index_budget.hits+excluded.hits`, day, source, n); err != nil {
		return false, err
	}
	// Les journées anciennes ne servent plus.
	if _, err := tx.ExecContext(ctx, `DELETE FROM pse_index_budget WHERE day<?`, now.UTC().AddDate(0, 0, -7).Format("2006-01-02")); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
