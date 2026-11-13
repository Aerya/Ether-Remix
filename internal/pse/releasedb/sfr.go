package releasedb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

// Import des releases françaises depuis la base PostgreSQL d'une instance
// StreamFusion Reborn auto-hébergée, en lecture seule. Les releases servent
// au catalogue et à la détection VF ; la lecture passe par les sources.

const (
	// SFRSourceID identifie les releases importées dans pse_releases.
	SFRSourceID = "sfr-db"
	sfrName     = "StreamFusion Reborn"

	sfrPage        = 1000
	sfrPause       = 100 * time.Millisecond
	sfrActiveWait  = 5 * time.Second
	sfrStartDelay  = 30 * time.Second
	sfrRetry       = 15 * time.Minute
	sfrOverlap     = int64(48 * 60 * 60) // synchronisations SFR qui gardent leur date d'origine
	sfrDefaultStep = 6 * time.Hour
	sfrMinStep     = 30 * time.Minute

	// Rattrapage des titres arrivés au catalogue après leur import. Sans index
	// sur tmdb_id, chaque requête parcourt la table : le nombre de requêtes et
	// le temps de lecture sont donc bornés à chaque passage.
	sfrTitlesBatch   = 500
	sfrTitlesBatches = 20
	sfrTitlesRows    = 20000
	sfrTitlesBudget  = 2 * time.Minute
	sfrTitlesRecheck = 30 * 24 * time.Hour
)

func init() {
	store.Register("pse_releases", 9, `
CREATE TABLE pse_sfr_checked(
  title_key INTEGER PRIMARY KEY,
  checked_at INTEGER NOT NULL DEFAULT 0
);
`)
}

// SFRSettings règle l'import (variables PSE_SFR*).
type SFRSettings struct {
	Enabled bool
	URL     string
	Every   time.Duration
	Private bool
}

func isOn(name string) bool {
	switch strings.ToLower(strings.TrimSpace(pseenv.Get(name))) {
	case "on", "1", "true", "yes", "oui":
		return true
	}
	return false
}

// LoadSFRSettings lit la configuration.
func LoadSFRSettings() SFRSettings {
	s := SFRSettings{URL: strings.TrimSpace(pseenv.Get("PSE_SFR_URL")), Every: sfrDefaultStep, Private: isOn("PSE_SFR_PRIVATE")}
	s.Enabled = isOn("PSE_SFR") && s.URL != ""
	if d, err := time.ParseDuration(strings.TrimSpace(pseenv.Get("PSE_SFR_INTERVAL"))); err == nil {
		s.Every = d
	}
	if s.Every < sfrMinStep {
		s.Every = sfrMinStep
	}
	return s
}

type sfrRow struct {
	ID, Name, Hash, Media, Seasons, Episodes string
	Size, TMDB, Stamp                        int64
	Cached                                   []string
	CacheUntil                               int64
}

type sfrReader interface {
	page(ctx context.Context, after string, since int64, private bool, limit int) ([]sfrRow, error)
	titles(ctx context.Context, ids []int64, after string, private bool, limit int) ([]sfrRow, error)
	close()
}

// Les deux lectures partagent les mêmes filtres.
const (
	sfrSelectSQL = `SELECT id, raw_title, COALESCE(size,0), info_hash, tmdb_id, tmdb_media_type,
  COALESCE(GREATEST(updated_at,created_at),0), COALESCE(parsed_data->>'seasons',''), COALESCE(parsed_data->>'episodes','')
FROM public.torrent_items
WHERE id > $1`
	sfrFiltersSQL = `
  AND tmdb_id IS NOT NULL AND tmdb_media_type IN ('movie','tv') AND COALESCE(info_hash,'') <> ''
  AND languages::text[] && ARRAY['fr','multi','vostfr']::text[]
  AND ($3::boolean OR privacy IS DISTINCT FROM 'private')
  AND COALESCE(parsed_data->>'trash','false') <> 'true' AND COALESCE(parsed_data->>'adult','false') <> 'true'
ORDER BY id LIMIT $4`
	sfrPageSQL   = sfrSelectSQL + ` AND COALESCE(GREATEST(updated_at,created_at),0) >= $2` + sfrFiltersSQL
	sfrTitlesSQL = sfrSelectSQL + ` AND tmdb_id = ANY($2::bigint[])` + sfrFiltersSQL
)

type pgxSFR struct {
	conn           *pgx.Conn
	cacheAvailable bool
}

func openPgxSFR(ctx context.Context, url string) (sfrReader, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, errors.New("adresse PostgreSQL invalide")
	}
	cfg.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.RuntimeParams["statement_timeout"] = "300000"
	cfg.RuntimeParams["application_name"] = "ether-remix-import"
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	p := &pgxSFR{conn: conn}
	// A deployment without this optional table remains searchable; no cache is assumed.
	_ = conn.QueryRow(ctx, `SELECT COALESCE(has_table_privilege(to_regclass('public.debrid_cache'), 'SELECT'),false)`).Scan(&p.cacheAvailable)
	return p, nil
}

func (p *pgxSFR) close() { _ = p.conn.Close(context.Background()) }

func (p *pgxSFR) page(ctx context.Context, after string, since int64, private bool, limit int) ([]sfrRow, error) {
	return p.read(ctx, limit, sfrPageSQL, after, since, private, limit)
}

func (p *pgxSFR) titles(ctx context.Context, ids []int64, after string, private bool, limit int) ([]sfrRow, error) {
	return p.read(ctx, limit, sfrTitlesSQL, after, ids, private, limit)
}

func (p *pgxSFR) read(ctx context.Context, limit int, query string, args ...any) ([]sfrRow, error) {
	rows, err := p.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]sfrRow, 0, min(limit, sfrPage))
	for rows.Next() {
		var r sfrRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Size, &r.Hash, &r.TMDB, &r.Media, &r.Stamp, &r.Seasons, &r.Episodes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	err = rows.Err()
	rows.Close()
	if err == nil {
		p.attachCache(ctx, out)
	}
	return out, err
}

// sfrFirstEpisode : une série est représentée par S01E01, un pack de la
// saison 1 compris.
func sfrFirstEpisode(seasons, episodes string) bool {
	var s, e []int
	if seasons == "" || json.Unmarshal([]byte(seasons), &s) != nil || !slices.Contains(s, 1) {
		return false
	}
	if episodes == "" || episodes == "null" {
		return true
	}
	if json.Unmarshal([]byte(episodes), &e) != nil {
		return false
	}
	return len(e) == 0 || slices.Contains(e, 1)
}

func sfrSignature(s SFRSettings) string {
	sum := sha256.Sum256([]byte(s.URL))
	return "cache-proof-v2|" + strconv.FormatBool(s.Private) + "|" + hex.EncodeToString(sum[:8])
}

// État conservé dans pse_state, pour reprendre après un redémarrage.
func (s *Store) sfrGet(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT v FROM pse_state WHERE k=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) sfrSet(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO pse_state(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, key, value)
	return err
}

func (s *Store) sfrInt(ctx context.Context, key string) (int64, error) {
	v, err := s.sfrGet(ctx, key)
	if err != nil || v == "" {
		return 0, err
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n, nil
}

// SFRStatus est affiché dans la console.
type SFRStatus struct {
	State    string `json:"state"` // running, ok, error
	Last     int64  `json:"last"`
	Releases int    `json:"releases"`
	Titles   int    `json:"titles"`
	Error    string `json:"error,omitempty"`
}

func (s *Store) sfrSetStatus(ctx context.Context, st SFRStatus) error {
	b, _ := json.Marshal(st)
	return s.sfrSet(ctx, "sfr_status", string(b))
}

// LoadSFRStatus lit le dernier état de l'import.
func (s *Store) LoadSFRStatus(ctx context.Context) SFRStatus {
	var st SFRStatus
	if v, err := s.sfrGet(ctx, "sfr_status"); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &st)
	}
	return st
}

// sfrKeys relie des identifiants TMDB aux titres du catalogue.
func (s *Store) sfrKeys(ctx context.Context, typ core.MediaType, ids []int64) (map[int64]core.RatingKey, error) {
	out := make(map[int64]core.RatingKey, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{string(typ)}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key,tmdb_id FROM catalog_titles WHERE type=? AND tmdb_id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, tmdb int64
		if err := rows.Scan(&key, &tmdb); err != nil {
			return nil, err
		}
		out[tmdb] = core.RatingKey(key)
	}
	return out, rows.Err()
}

type sfrTitle struct {
	Key  core.RatingKey
	TMDB int64
}

// sfrUnchecked liste les titres sans release SFR à vérifier : ceux jamais
// vérifiés d'abord, puis les plus anciennement vérifiés.
func (s *Store) sfrUnchecked(ctx context.Context, before time.Time, limit int) ([]sfrTitle, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.key,c.tmdb_id FROM catalog_titles c
LEFT JOIN pse_sfr_checked k ON k.title_key=c.key
WHERE c.type IN ('movie','show') AND c.tmdb_id>0 AND (
 (COALESCE(k.checked_at,0)<? AND NOT EXISTS (SELECT 1 FROM pse_releases r WHERE r.source_id=? AND r.title_key=c.key))
 OR (COALESCE(k.checked_at,0)<? AND EXISTS (SELECT 1 FROM pse_releases r WHERE r.source_id=? AND r.title_key=c.key AND r.cache_until>0 AND r.cache_until<=?))
)
ORDER BY COALESCE(k.checked_at,0),c.popularity DESC,c.key LIMIT ?`, before.Unix(), SFRSourceID, s.now().Add(-sfrDefaultStep).Unix(), SFRSourceID, s.now().Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sfrTitle
	for rows.Next() {
		var key, tmdb int64
		if err := rows.Scan(&key, &tmdb); err != nil {
			return nil, err
		}
		out = append(out, sfrTitle{core.RatingKey(key), tmdb})
	}
	return out, rows.Err()
}

func (s *Store) sfrChecked(ctx context.Context, titles []sfrTitle, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range titles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pse_sfr_checked(title_key,checked_at) VALUES(?,?)
ON CONFLICT(title_key) DO UPDATE SET checked_at=excluded.checked_at`, int64(t.Key), now.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// sfrSave enregistre une page et renvoie le nombre de releases gardées.
func (s *Service) sfrSave(ctx context.Context, rows []sfrRow) (int, error) {
	ids := map[core.MediaType][]int64{}
	for _, r := range rows {
		typ := core.TypeMovie
		if r.Media == "tv" {
			if !sfrFirstEpisode(r.Seasons, r.Episodes) {
				continue
			}
			typ = core.TypeShow
		}
		if !slices.Contains(ids[typ], r.TMDB) {
			ids[typ] = append(ids[typ], r.TMDB)
		}
	}
	keys := map[core.MediaType]map[int64]core.RatingKey{}
	for typ, list := range ids {
		m, err := s.st.sfrKeys(ctx, typ, list)
		if err != nil {
			return 0, err
		}
		keys[typ] = m
	}
	now := s.d.Now()
	batch := make([]Release, 0, len(rows))
	type target struct {
		key core.RatingKey
		typ core.MediaType
	}
	found := map[target]int{}
	for _, r := range rows {
		typ, season, episode := core.TypeMovie, 0, 0
		if r.Media == "tv" {
			if !sfrFirstEpisode(r.Seasons, r.Episodes) {
				continue
			}
			typ, season, episode = core.TypeShow, 1, 1
		}
		key, ok := keys[typ][r.TMDB]
		if !ok {
			continue
		}
		rel := core.Release{ID: strings.ToLower(r.Hash), Name: r.Name, Size: r.Size, Indexer: sfrName,
			Protocol: core.ProtocolTorrent, Info: releaseparse.Parse(r.Name)}
		saved := FromCore(core.Title{Key: key, Type: typ}, season, episode, SFRSourceID, rel, r.Cached)
		saved.CacheUntil = r.CacheUntil
		batch = append(batch, saved)
		found[target{key, typ}]++
	}
	if err := s.st.Save(ctx, batch, now); err != nil {
		return 0, err
	}
	p := Playable()
	for t := range found {
		// Imported metadata alone must not postpone a real source search.
		if p.Enabled {
			if err := s.st.RefreshPlayable(ctx, t.key, p); err != nil {
				return 0, err
			}
		}
	}
	return len(batch), nil
}

// sfrForget retire les releases importées quand la base ou le choix des
// trackers privés change.
func (s *Service) sfrForget(ctx context.Context) error {
	rows, err := s.st.db.QueryContext(ctx, `SELECT DISTINCT title_key FROM pse_releases WHERE source_id=?`, SFRSourceID)
	if err != nil {
		return err
	}
	var keys []core.RatingKey
	for rows.Next() {
		var k int64
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, core.RatingKey(k))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := s.st.db.ExecContext(ctx, `DELETE FROM pse_releases WHERE source_id=?`, SFRSourceID); err != nil {
		return err
	}
	if p := Playable(); p.Enabled {
		for _, k := range keys {
			if err := s.st.RefreshPlayable(ctx, k, p); err != nil {
				return err
			}
		}
	}
	if _, err := s.st.db.ExecContext(ctx, `DELETE FROM pse_sfr_checked`); err != nil {
		return err
	}
	for _, k := range []string{"sfr_after", "sfr_since", "sfr_max", "sfr_cursor", "sfr_done"} {
		if err := s.st.sfrSet(ctx, k, ""); err != nil {
			return err
		}
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// sfrIdle attend la fin des lectures en cours.
func (s *Service) sfrIdle(ctx context.Context) error {
	for s.d.Active != nil && s.d.Active() {
		if !sleepCtx(ctx, sfrActiveWait) {
			return ctx.Err()
		}
	}
	return ctx.Err()
}

// sfrCatchUp cherche dans SFR les titres du catalogue sans release importée :
// un titre arrivé après l'import de ses releases n'est plus revu par la
// lecture incrémentale. Un lot n'est marqué vérifié qu'une fois lu en entier.
func (s *Service) sfrCatchUp(ctx context.Context, reader sfrReader, cfg SFRSettings) error {
	var spent time.Duration
	checked, saved := 0, 0
	for range sfrTitlesBatches {
		if spent >= sfrTitlesBudget {
			break
		}
		if err := s.sfrIdle(ctx); err != nil {
			return err
		}
		now := s.d.Now()
		list, err := s.st.sfrUnchecked(ctx, now.Add(-sfrTitlesRecheck), sfrTitlesBatch)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			break
		}
		ids := make([]int64, len(list))
		for i, t := range list {
			ids[i] = t.TMDB
		}
		after := ""
		for {
			start := s.d.Now()
			rows, err := reader.titles(ctx, ids, after, cfg.Private, sfrTitlesRows)
			spent += s.d.Now().Sub(start)
			if err != nil {
				return fmt.Errorf("lecture impossible: %w", err)
			}
			for chunk := range slices.Chunk(rows, sfrPage) {
				if err := s.sfrIdle(ctx); err != nil {
					return err
				}
				n, err := s.sfrSave(ctx, chunk)
				if err != nil {
					return err
				}
				saved += n
			}
			if len(rows) < sfrTitlesRows {
				break
			}
			after = rows[len(rows)-1].ID
		}
		if err := s.st.sfrChecked(ctx, list, now); err != nil {
			return err
		}
		checked += len(list)
		if len(list) < sfrTitlesBatch {
			break
		}
		if !sleepCtx(ctx, sfrPause) {
			return ctx.Err()
		}
	}
	if checked > 0 {
		s.d.Logger.Info("pse streamfusion catch-up done", "titles", checked, "releases", saved)
	}
	return nil
}

// sfrCycle lit ce qui a changé depuis le dernier passage. Le premier passage
// lit tout, par pages, et reprend où il s'était arrêté.
func (s *Service) sfrCycle(ctx context.Context, cfg SFRSettings) error {
	sig := sfrSignature(cfg)
	prev, err := s.st.sfrGet(ctx, "sfr_sig")
	if err != nil {
		return err
	}
	if prev != sig {
		if err := s.sfrForget(ctx); err != nil {
			return err
		}
		if err := s.st.sfrSet(ctx, "sfr_sig", sig); err != nil {
			return err
		}
	}
	open := s.openSFR
	if open == nil {
		open = openPgxSFR
	}
	reader, err := open(ctx, cfg.URL)
	if err != nil {
		return fmt.Errorf("connexion impossible: %w", err)
	}
	defer reader.close()

	after, err := s.st.sfrGet(ctx, "sfr_after")
	if err != nil {
		return err
	}
	cursor, err := s.st.sfrInt(ctx, "sfr_cursor")
	if err != nil {
		return err
	}
	since, top := max(cursor-sfrOverlap, 0), cursor
	if after != "" {
		if since, err = s.st.sfrInt(ctx, "sfr_since"); err != nil {
			return err
		}
		if top, err = s.st.sfrInt(ctx, "sfr_max"); err != nil {
			return err
		}
	}
	done, err := s.st.sfrInt(ctx, "sfr_done")
	if err != nil {
		return err
	}
	if after == "" {
		done = 0
	}
	_ = s.st.sfrSetStatus(ctx, SFRStatus{State: "running", Releases: int(done)})
	for {
		if err := s.sfrIdle(ctx); err != nil {
			return err
		}
		rows, err := reader.page(ctx, after, since, cfg.Private, sfrPage)
		if err != nil {
			return fmt.Errorf("lecture impossible: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		n, err := s.sfrSave(ctx, rows)
		if err != nil {
			return err
		}
		done += int64(n)
		after = rows[len(rows)-1].ID
		for _, r := range rows {
			top = max(top, r.Stamp)
		}
		for k, v := range map[string]string{"sfr_after": after, "sfr_since": strconv.FormatInt(since, 10),
			"sfr_max": strconv.FormatInt(top, 10), "sfr_done": strconv.FormatInt(done, 10)} {
			if err := s.st.sfrSet(ctx, k, v); err != nil {
				return err
			}
		}
		_ = s.st.sfrSetStatus(ctx, SFRStatus{State: "running", Releases: int(done)})
		if len(rows) < sfrPage {
			break
		}
		if !sleepCtx(ctx, sfrPause) {
			return ctx.Err()
		}
	}
	now := s.d.Now()
	for k, v := range map[string]string{"sfr_cursor": strconv.FormatInt(top, 10), "sfr_after": "", "sfr_since": "", "sfr_max": "", "sfr_done": "", "sfr_last": strconv.FormatInt(now.Unix(), 10)} {
		if err := s.st.sfrSet(ctx, k, v); err != nil {
			return err
		}
	}
	// Un rattrapage en échec reprend au passage suivant, sans relancer tout
	// l'import au rythme des nouvelles tentatives.
	if err := s.sfrCatchUp(ctx, reader, cfg); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		s.d.Logger.Warn("pse streamfusion catch-up failed", "err", err)
	}
	// Une release toujours présente dans SFR ne doit pas expirer entre deux
	// passages incrémentaux.
	if _, err := s.st.db.ExecContext(ctx, `UPDATE pse_releases SET last_seen=? WHERE source_id=?`, now.Unix(), SFRSourceID); err != nil {
		return err
	}
	var st SFRStatus
	if err := s.st.db.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(DISTINCT title_key) FROM pse_releases WHERE source_id=?`, SFRSourceID).Scan(&st.Releases, &st.Titles); err != nil {
		return err
	}
	st.State, st.Last = "ok", now.Unix()
	s.d.Logger.Info("pse streamfusion import done", "releases", st.Releases, "titles", st.Titles)
	return s.st.sfrSetStatus(ctx, st)
}

func (s *Service) runSFR(ctx context.Context, cfg SFRSettings) {
	wait := sfrStartDelay
	if last, err := s.st.sfrInt(ctx, "sfr_last"); err == nil && last > 0 {
		if left := time.Unix(last, 0).Add(cfg.Every).Sub(s.d.Now()); left > wait {
			if after, _ := s.st.sfrGet(ctx, "sfr_after"); after == "" {
				if sig, _ := s.st.sfrGet(ctx, "sfr_sig"); sig == sfrSignature(cfg) {
					wait = left
				}
			}
		}
	}
	for sleepCtx(ctx, wait) {
		wait = cfg.Every
		if err := s.sfrCycle(ctx, cfg); err != nil && ctx.Err() == nil {
			s.d.Logger.Warn("pse streamfusion import failed", "err", err)
			_ = s.st.sfrSetStatus(ctx, SFRStatus{State: "error", Error: sfrPublicError(err)})
			wait = min(cfg.Every, sfrRetry)
		}
	}
}

// sfrPublicError garde un message sans détail de connexion pour la console.
func sfrPublicError(err error) string {
	msg := err.Error()
	for _, p := range []string{"connexion impossible", "lecture impossible", "adresse PostgreSQL invalide"} {
		if strings.HasPrefix(msg, p) {
			return p
		}
	}
	return "import interrompu"
}
