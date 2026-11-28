package alko

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("pse_alko", "pse_releases") // pse_state
	// Miroir local du catalogue Alkodiques de StreamFusion Reborn.
	//
	// url_sealed : lien source (jeton AllDebrid) chiffré, jamais en clair.
	// info_json et langs sont réservés aux pistes audio tirées de media_info ;
	// ils restent vides tant que cette lecture n'est pas branchée.
	// gen : génération de la lecture complète qui a vu la ligne (purge des
	// fichiers absents à la fin).
	// pub / pub_sig / rel_id / title_key : état de la publication dans
	// pse_releases (0 : à publier, 1 : publié, 2 : écarté pour pub_sig).
	store.Register("pse_alko", 1, `
CREATE TABLE pse_alko_files(
  playback_id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  tmdb INTEGER NOT NULL,
  season INTEGER NOT NULL DEFAULT 0,
  episode INTEGER NOT NULL DEFAULT 0,
  filename TEXT NOT NULL,
  filesize INTEGER NOT NULL,
  fingerprint TEXT NOT NULL DEFAULT '',
  synced_at TEXT NOT NULL DEFAULT '',
  langs TEXT NOT NULL DEFAULT '',
  info_json TEXT,
  url_sealed TEXT NOT NULL,
  content_hash TEXT NOT NULL DEFAULT '',
  gen INTEGER NOT NULL DEFAULT 0,
  pub INTEGER NOT NULL DEFAULT 0,
  pub_sig TEXT NOT NULL DEFAULT '',
  rel_id TEXT NOT NULL DEFAULT '',
  title_key INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX pse_alko_files_title ON pse_alko_files(kind, tmdb, season, episode);
CREATE INDEX pse_alko_files_gen ON pse_alko_files(gen);
CREATE TABLE pse_alko_checked(
  title_key INTEGER PRIMARY KEY,
  checked_at INTEGER NOT NULL DEFAULT 0
);
`)
}

// Valeurs de pub.
const (
	pubPending   = 0
	pubPublished = 1
	pubSkipped   = 2
)

// maxIn borne le nombre de paramètres d'une requête IN.
const maxIn = 500

// Store lit et écrit le miroir. Aucun lien n'y entre en clair.
type Store struct{ DB *sql.DB }

// row est une ligne du miroir (sans le lien).
type row struct {
	ID          string
	Kind        string
	TMDB        int64
	Season      int
	Episode     int
	Filename    string
	Size        int64
	Fingerprint string
	SyncedAt    string
	Sealed      string
	Langs       string // langues des pistes audio (langtag), séparées par des virgules
	Info        sql.NullString
	Hash        string
	Pub         int
	PubSig      string
	RelID       string
	TitleKey    int64
}

// contentHash résume ce qui change la publication ou la lecture d'un fichier.
func contentHash(e Element, season, episode int) string {
	media := ""
	if e.MediaInfo != nil {
		sum := sha256.Sum256([]byte(*e.MediaInfo))
		media = hex.EncodeToString(sum[:8])
	}
	h := sha256.New()
	for _, p := range []string{e.PlaybackID, e.Kind, strconv.FormatInt(e.TMDB, 10), strconv.Itoa(season), strconv.Itoa(episode),
		e.Filename, strconv.FormatInt(e.Filesize, 10), e.Fingerprint, e.SourceURL, media} {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

func placeholders(n int) string { return "?" + strings.Repeat(",?", n-1) }

// state : pse_state (clé/valeur).
func (s Store) get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT v FROM pse_state WHERE k=?`, "alko_"+key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s Store) set(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO pse_state(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, "alko_"+key, value)
	return err
}

func (s Store) int(ctx context.Context, key string) (int64, error) {
	v, err := s.get(ctx, key)
	if err != nil || v == "" {
		return 0, err
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n, nil
}

func (s Store) setInt(ctx context.Context, key string, n int64) error {
	return s.set(ctx, key, strconv.FormatInt(n, 10))
}

// prior lit l'état déjà enregistré de ces fichiers.
func (s Store) prior(ctx context.Context, ids []string) (map[string]row, error) {
	out := make(map[string]row, len(ids))
	for i := 0; i < len(ids); i += maxIn {
		part := ids[i:min(i+maxIn, len(ids))]
		args := make([]any, len(part))
		for j, id := range part {
			args[j] = id
		}
		rows, err := s.DB.QueryContext(ctx, `SELECT playback_id,content_hash,pub,pub_sig,rel_id,title_key FROM pse_alko_files WHERE playback_id IN (`+placeholders(len(part))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.ID, &r.Hash, &r.Pub, &r.PubSig, &r.RelID, &r.TitleKey); err != nil {
				rows.Close()
				return nil, err
			}
			out[r.ID] = r
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// upsert écrit des fichiers nouveaux ou modifiés, en une transaction. La
// publication repart de « à publier » ; rel_id et title_key restent pour
// retirer l'ancienne publication si le fichier a changé de titre.
func (s Store) upsert(ctx context.Context, rows []row, gen int64) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO pse_alko_files(playback_id,kind,tmdb,season,episode,filename,filesize,fingerprint,synced_at,langs,info_json,url_sealed,content_hash,gen,pub)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,0)
ON CONFLICT(playback_id) DO UPDATE SET kind=excluded.kind,tmdb=excluded.tmdb,season=excluded.season,episode=excluded.episode,filename=excluded.filename,filesize=excluded.filesize,fingerprint=excluded.fingerprint,synced_at=excluded.synced_at,langs=excluded.langs,info_json=excluded.info_json,url_sealed=excluded.url_sealed,content_hash=excluded.content_hash,gen=excluded.gen,pub=0`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.ID, r.Kind, r.TMDB, r.Season, r.Episode, r.Filename, r.Size, r.Fingerprint, r.SyncedAt, r.Langs, r.Info, r.Sealed, r.Hash, gen); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// seen marque des fichiers inchangés comme vus par la génération gen.
func (s Store) seen(ctx context.Context, ids []string, gen int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `UPDATE pse_alko_files SET gen=? WHERE playback_id=?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, id := range ids {
		if _, err := stmt.ExecContext(ctx, gen, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// published enregistre l'état de publication de ces fichiers.
type published struct {
	ID       string
	Pub      int
	RelID    string
	TitleKey int64
}

func (s Store) setPublished(ctx context.Context, sig string, list []published) error {
	if len(list) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `UPDATE pse_alko_files SET pub=?,pub_sig=?,rel_id=?,title_key=? WHERE playback_id=?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range list {
		if _, err := stmt.ExecContext(ctx, p.Pub, sig, p.RelID, p.TitleKey, p.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// delete retire des fichiers du miroir.
func (s Store) delete(ctx context.Context, ids []string) error {
	for i := 0; i < len(ids); i += maxIn {
		part := ids[i:min(i+maxIn, len(ids))]
		args := make([]any, len(part))
		for j, id := range part {
			args[j] = id
		}
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM pse_alko_files WHERE playback_id IN (`+placeholders(len(part))+`)`, args...); err != nil {
			return err
		}
	}
	return nil
}

// stale liste des fichiers jamais revus par la génération gen (absents du
// catalogue), avec leur publication.
func (s Store) stale(ctx context.Context, gen int64, limit int) ([]row, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT playback_id,rel_id,title_key FROM pse_alko_files WHERE gen<? ORDER BY gen,playback_id LIMIT ?`, gen, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.RelID, &r.TitleKey); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// forPublish relit des fichiers (avec le lien chiffré) à publier : ceux de ces
// titres jamais jugés ou jugés selon une autre signature.
func (s Store) forPublish(ctx context.Context, kind string, tmdb []int64, sig string, after string, limit int) ([]row, error) {
	args := []any{kind}
	for _, id := range tmdb {
		args = append(args, id)
	}
	args = append(args, sig, after, limit)
	rows, err := s.DB.QueryContext(ctx, `SELECT playback_id,kind,tmdb,season,episode,filename,filesize,url_sealed,pub,rel_id,title_key
FROM pse_alko_files WHERE kind=? AND tmdb IN (`+placeholders(len(tmdb))+`) AND (pub=0 OR pub_sig<>?) AND playback_id>? ORDER BY playback_id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.Kind, &r.TMDB, &r.Season, &r.Episode, &r.Filename, &r.Size, &r.Sealed, &r.Pub, &r.RelID, &r.TitleKey); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// forSearch lit les fichiers d'un film ou d'un épisode.
func (s Store) forSearch(ctx context.Context, kind string, tmdb int64, season, episode, limit int) ([]row, error) {
	q := `SELECT playback_id,filename,filesize,tmdb,url_sealed FROM pse_alko_files WHERE kind=? AND tmdb=?`
	args := []any{kind, tmdb}
	if kind == "episode" {
		q += ` AND season=? AND episode=?`
		args = append(args, season, episode)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY playback_id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ID, &r.Filename, &r.Size, &r.TMDB, &r.Sealed); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Counts résume le miroir.
type Counts struct {
	Files     int `json:"files"`
	Movies    int `json:"movies"`
	Episodes  int `json:"episodes"`
	Published int `json:"published"`
}

func (s Store) counts(ctx context.Context) (Counts, error) {
	var c Counts
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN kind='movie' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN kind='episode' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN pub=1 THEN 1 ELSE 0 END),0) FROM pse_alko_files`).
		Scan(&c.Files, &c.Movies, &c.Episodes, &c.Published)
	return c, err
}

// reset vide le miroir et l'état de synchronisation (autre serveur).
func (s Store) reset(ctx context.Context) error {
	for _, q := range []string{`DELETE FROM pse_alko_files`, `DELETE FROM pse_alko_checked`, `DELETE FROM pse_state WHERE k LIKE 'alko_%'`} {
		if _, err := s.DB.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}
