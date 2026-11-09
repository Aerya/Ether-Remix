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
)

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
}

type sfrReader interface {
	page(ctx context.Context, after string, since int64, private bool, limit int) ([]sfrRow, error)
	close()
}

const sfrPageSQL = `SELECT id, raw_title, COALESCE(size,0), info_hash, tmdb_id, tmdb_media_type,
  COALESCE(GREATEST(updated_at,created_at),0), COALESCE(parsed_data->>'seasons',''), COALESCE(parsed_data->>'episodes','')
FROM public.torrent_items
WHERE id > $1 AND COALESCE(GREATEST(updated_at,created_at),0) >= $2
  AND tmdb_id IS NOT NULL AND tmdb_media_type IN ('movie','tv') AND COALESCE(info_hash,'') <> ''
  AND languages::text[] && ARRAY['fr','multi','vostfr']::text[]
  AND ($3::boolean OR privacy IS DISTINCT FROM 'private')
  AND COALESCE(parsed_data->>'trash','false') <> 'true' AND COALESCE(parsed_data->>'adult','false') <> 'true'
ORDER BY id LIMIT $4`

type pgxSFR struct{ conn *pgx.Conn }

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
	return &pgxSFR{conn: conn}, nil
}

func (p *pgxSFR) close() { _ = p.conn.Close(context.Background()) }

func (p *pgxSFR) page(ctx context.Context, after string, since int64, private bool, limit int) ([]sfrRow, error) {
	rows, err := p.conn.Query(ctx, sfrPageSQL, after, since, private, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]sfrRow, 0, limit)
	for rows.Next() {
		var r sfrRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Size, &r.Hash, &r.TMDB, &r.Media, &r.Stamp, &r.Seasons, &r.Episodes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
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
	return strconv.FormatBool(s.Private) + "|" + hex.EncodeToString(sum[:8])
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

// sfrMark évite à l'indexeur de chercher tout de suite un titre importé, sans
// écraser un titre où il a déjà trouvé des releases.
func (s *Store) sfrMark(ctx context.Context, key core.RatingKey, typ core.MediaType, found int, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO pse_index_titles(title_key,media_type,last_indexed,next_due,found,status) VALUES(?,?,?,?,?,'found')
ON CONFLICT(title_key) DO UPDATE SET last_indexed=excluded.last_indexed,next_due=excluded.next_due,found=excluded.found,status='found' WHERE pse_index_titles.found=0`,
		int64(key), string(typ), now.Unix(), now.Add(refreshFound).Unix(), found)
	return err
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
		batch = append(batch, FromCore(core.Title{Key: key, Type: typ}, season, episode, SFRSourceID, rel, []string{"alldebrid"}))
		found[target{key, typ}]++
	}
	if err := s.st.Save(ctx, batch, now); err != nil {
		return 0, err
	}
	p := Playable()
	for t, n := range found {
		if err := s.st.sfrMark(ctx, t.key, t.typ, n, now); err != nil {
			return 0, err
		}
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
		for s.d.Active != nil && s.d.Active() {
			if !sleepCtx(ctx, sfrActiveWait) {
				return ctx.Err()
			}
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
	for k, v := range map[string]string{"sfr_cursor": strconv.FormatInt(top, 10), "sfr_after": "", "sfr_since": "", "sfr_max": "", "sfr_done": "", "sfr_last": strconv.FormatInt(now.Unix(), 10)} {
		if err := s.st.sfrSet(ctx, k, v); err != nil {
			return err
		}
	}
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
