package continuewatch

// Ajout Remix — voir docs/pse/FORK.fr.md. Table « à suivre » de « Continuer à
// regarder » : pour chaque spectateur et chaque série commencée, l'épisode
// suivant est calculé une fois (et recalculé quand le spectateur regarde ou
// qu'un épisode sort), au lieu d'être recherché à chaque affichage dans
// l'historique et dans tous les épisodes de la série.

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_continue_next", 1, `
CREATE TABLE pse_continue_next(
  user_id INTEGER NOT NULL,
  show_key INTEGER NOT NULL,
  last_key INTEGER NOT NULL,
  last_watched_at INTEGER NOT NULL,
  next_key INTEGER NOT NULL DEFAULT 0,
  next_released_ts INTEGER NOT NULL DEFAULT 0,
  rank_ts INTEGER NOT NULL,
  updated_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(user_id, show_key)
);
CREATE INDEX pse_continue_next_rank ON pse_continue_next(user_id, rank_ts DESC);
CREATE TABLE pse_continue_next_state(
  id INTEGER PRIMARY KEY CHECK(id=1),
  watermark INTEGER NOT NULL DEFAULT 0,
  checked_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE pse_continue_next_filled(
  user_id INTEGER PRIMARY KEY,
  filled_at INTEGER NOT NULL
);
`)
	// Séries dont les épisodes ont été complétés depuis TMDB (rattrapage).
	store.Register("pse_continue_next_completed", 1, `
CREATE TABLE pse_continue_next_completed(
  show_key INTEGER PRIMARY KEY,
  completed_at INTEGER NOT NULL
);
`)
	// Données dérivées de l'historique : recalculables, donc jamais fusionnées.
	tables := []string{"pse_continue_next", "pse_continue_next_filled"}
	store.RegisterViewerPurge("pse_continue_next", tables, func(ctx context.Context, tx *sql.Tx, id int64) error {
		for _, t := range tables {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE user_id=?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	// La cible reçoit l'historique de la source : ses lignes sont à refaire,
	// donc elle perd aussi sa marque « rempli » et sera remplie de nouveau.
	store.RegisterViewerMerge("pse_continue_next", tables, func(ctx context.Context, tx *sql.Tx, from, into int64) error {
		for _, t := range tables {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE user_id IN (?,?)`, from, into); err != nil {
				return err
			}
		}
		return nil
	})
}

// Row est la ligne « à suivre » d'une série pour un spectateur.
type Row struct {
	ShowKey       core.RatingKey
	LastKey       core.RatingKey
	LastWatchedAt time.Time
	NextKey       core.RatingKey
	NextReleased  time.Time
	RankAt        time.Time
}

// Store calcule et lit la table. Now et Pause sont réglables pour les tests.
type Store struct {
	DB    *sql.DB
	Now   func() time.Time
	Pause time.Duration // entre deux lots de remplissage
	// Refresh charge dans le catalogue la saison suivante des épisodes donnés
	// (Catalog.ContinueEpisodes). Appelé seulement en arrière-plan.
	Refresh func(context.Context, []core.RatingKey)
	// Busy suspend le rattrapage pendant une lecture.
	Busy func() bool
}

var current atomic.Pointer[Store]

// SetDefault enregistre la table utilisée par la rangée (nil = désactivée).
func SetDefault(s *Store) { current.Store(s) }

// Default renvoie la table de la rangée, ou nil.
func Default() *Store { return current.Load() }

const (
	// IncrementalEvery : passe incrémentale sur l'historique.
	IncrementalEvery = 30 * time.Second
	// NewEpisodesEvery : détection des épisodes sortis pour les séries à jour.
	NewEpisodesEvery = 30 * time.Minute
	// fillBatch : séries calculées par lot au remplissage initial.
	fillBatch = 50
	// CompleteEvery, completePerPass : rattrapage des épisodes manquants.
	CompleteEvery   = 5 * time.Minute
	completePerPass = 20
	// completeAgain : toute série sans suivant est revérifiée après ce délai
	// (4 fois par jour) ; les séries diffusées le sont aussi toutes les 30 min.
	completeAgain = 6 * time.Hour
	// releaseOverlap : une fiche ajoutée au catalogue après sa date est quand même vue.
	releaseOverlap = 24 * time.Hour
)

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func episodeRange(show int64) (int64, int64) {
	return int64(core.EpisodeKey(show, 0, 0)), int64(core.EpisodeKey(show+1, 0, 0))
}

// Compute recalcule la ligne d'une série pour un spectateur et l'enregistre.
// Seuls les épisodes déjà en base sont lus : ni TMDB ni écriture du catalogue.
// Sans épisode terminé, la ligne est supprimée.
func (s *Store) Compute(ctx context.Context, uid int64, show core.RatingKey) error {
	row, ok, err := s.calc(ctx, uid, show)
	if err != nil {
		return err
	}
	return s.write(ctx, uid, show, row, ok)
}

// calc applique la règle d'appendNext : le premier épisode, par saison puis
// épisode (saison 0 exclue), après le dernier terminé, non vu, sorti.
func (s *Store) calc(ctx context.Context, uid int64, show core.RatingKey) (Row, bool, error) {
	lo, hi := episodeRange(show.TMDB())
	var lastKey, viewed int64
	err := s.DB.QueryRowContext(ctx, `SELECT rating_key, viewed_at FROM state_history
WHERE user_id=? AND completed=1 AND rating_key>=? AND rating_key<? ORDER BY viewed_at DESC, id DESC LIMIT 1`, uid, lo, hi).Scan(&lastKey, &viewed)
	if err == sql.ErrNoRows {
		return Row{}, false, nil
	}
	if err != nil {
		return Row{}, false, err
	}
	last := core.RatingKey(lastKey)
	row := Row{ShowKey: show, LastKey: last, LastWatchedAt: time.Unix(0, viewed).UTC()}
	season, ep := last.Season(), last.Episode()
	cond, args := `season>0`, []any{int64(show)}
	if season > 0 {
		cond += ` AND (season>? OR (season=? AND episode>?))`
		args = append(args, season, season, ep)
	}
	args = append(args, s.now().Unix(), uid)
	var next, released int64
	err = s.DB.QueryRowContext(ctx, `SELECT key, released_ts FROM catalog_titles
WHERE type='episode' AND show_key=? AND `+cond+` AND adult=0 AND released_ts<=?
AND NOT EXISTS(SELECT 1 FROM state_watch w WHERE w.user_id=? AND w.rating_key=catalog_titles.key AND w.view_count>0 AND w.view_offset_ns<=0)
ORDER BY season ASC, episode ASC LIMIT 1`, args...).Scan(&next, &released)
	if err != nil && err != sql.ErrNoRows {
		return Row{}, false, err
	}
	if err == nil {
		row.NextKey, row.NextReleased = core.RatingKey(next), time.Unix(released, 0).UTC()
	}
	row.RankAt = row.LastWatchedAt
	if row.NextReleased.After(row.RankAt) {
		row.RankAt = row.NextReleased
	}
	return row, true, nil
}

const upsertSQL = `INSERT INTO pse_continue_next(user_id, show_key, last_key, last_watched_at, next_key, next_released_ts, rank_ts, updated_at)
VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(user_id, show_key) DO UPDATE SET last_key=excluded.last_key,
last_watched_at=excluded.last_watched_at, next_key=excluded.next_key, next_released_ts=excluded.next_released_ts,
rank_ts=excluded.rank_ts, updated_at=excluded.updated_at`

type pending struct {
	uid  int64
	show core.RatingKey
	row  Row
	ok   bool
}

func (s *Store) write(ctx context.Context, uid int64, show core.RatingKey, row Row, ok bool) error {
	return s.writeAll(ctx, []pending{{uid, show, row, ok}})
}

// writeAll enregistre un lot dans une transaction courte.
func (s *Store) writeAll(ctx context.Context, batch []pending) error {
	if len(batch) == 0 {
		return nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().Unix()
	for _, p := range batch {
		if !p.ok {
			_, err = tx.ExecContext(ctx, `DELETE FROM pse_continue_next WHERE user_id=? AND show_key=?`, p.uid, int64(p.show))
		} else {
			r := p.row
			_, err = tx.ExecContext(ctx, upsertSQL, p.uid, int64(p.show), int64(r.LastKey), r.LastWatchedAt.UnixNano(),
				int64(r.NextKey), r.NextReleased.Unix(), r.RankAt.UnixNano(), now)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Next lit les séries à suivre d'un spectateur (épisode suivant connu), les
// plus récentes d'abord. ok est faux tant que la table n'est pas remplie pour
// lui : l'appelant garde alors l'ancien calcul.
func (s *Store) Next(ctx context.Context, uid int64) (rows []Row, ok bool, err error) {
	if s == nil || s.DB == nil || uid <= 0 {
		return nil, false, nil
	}
	var filled int
	err = s.DB.QueryRowContext(ctx, `SELECT 1 FROM pse_continue_next_filled WHERE user_id=?`, uid).Scan(&filled)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	q, err := s.DB.QueryContext(ctx, `SELECT show_key, last_key, last_watched_at, next_key, next_released_ts, rank_ts
FROM pse_continue_next WHERE user_id=? AND next_key<>0 ORDER BY rank_ts DESC, show_key DESC`, uid)
	if err != nil {
		return nil, false, err
	}
	defer q.Close()
	for q.Next() {
		var r Row
		var show, last, lastAt, next, rel, rank int64
		if err := q.Scan(&show, &last, &lastAt, &next, &rel, &rank); err != nil {
			return nil, false, err
		}
		r = Row{ShowKey: core.RatingKey(show), LastKey: core.RatingKey(last), LastWatchedAt: time.Unix(0, lastAt).UTC(),
			NextKey: core.RatingKey(next), NextReleased: time.Unix(rel, 0).UTC(), RankAt: time.Unix(0, rank).UTC()}
		rows = append(rows, r)
	}
	return rows, true, q.Err()
}

// Row lit la ligne d'une série, même « à jour » (NextKey 0).
func (s *Store) Row(ctx context.Context, uid int64, show core.RatingKey) (Row, bool, error) {
	var last, lastAt, next, rel, rank int64
	err := s.DB.QueryRowContext(ctx, `SELECT last_key, last_watched_at, next_key, next_released_ts, rank_ts FROM pse_continue_next WHERE user_id=? AND show_key=?`,
		uid, int64(show)).Scan(&last, &lastAt, &next, &rel, &rank)
	if err == sql.ErrNoRows {
		return Row{}, false, nil
	}
	if err != nil {
		return Row{}, false, err
	}
	return Row{ShowKey: show, LastKey: core.RatingKey(last), LastWatchedAt: time.Unix(0, lastAt).UTC(), NextKey: core.RatingKey(next),
		NextReleased: time.Unix(rel, 0).UTC(), RankAt: time.Unix(0, rank).UTC()}, true, nil
}

// historyRows lit les événements d'historique au-delà du filigrane.
func (s *Store) historyRows(ctx context.Context, after int64, limit int) (maxID int64, pairs []pending, n int, err error) {
	q, err := s.DB.QueryContext(ctx, `SELECT id, user_id, rating_key, completed FROM state_history WHERE id>? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return 0, nil, 0, err
	}
	defer q.Close()
	seen := map[[2]int64]bool{}
	maxID = after
	for q.Next() {
		var id, uid, key int64
		var done bool
		if err := q.Scan(&id, &uid, &key, &done); err != nil {
			return 0, nil, 0, err
		}
		n++
		maxID = id
		rk := core.RatingKey(key)
		if !done || rk.Kind() != core.KindEpisode {
			continue
		}
		show := core.ShowKey(rk.TMDB())
		if k := [2]int64{uid, int64(show)}; !seen[k] {
			seen[k] = true
			pairs = append(pairs, pending{uid: uid, show: show})
		}
	}
	return maxID, pairs, n, q.Err()
}

func (s *Store) loadState(ctx context.Context) (watermark, checkedAt int64, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT watermark, checked_at FROM pse_continue_next_state WHERE id=1`).Scan(&watermark, &checkedAt)
	if err == sql.ErrNoRows {
		// Premier démarrage : l'historique existant est couvert par le
		// remplissage ; la passe incrémentale ne suit que la suite.
		if err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM state_history`).Scan(&watermark); err != nil {
			return 0, 0, err
		}
		checkedAt = s.now().Unix()
		_, err = s.DB.ExecContext(ctx, `INSERT INTO pse_continue_next_state(id, watermark, checked_at) VALUES(1,?,?) ON CONFLICT(id) DO NOTHING`, watermark, checkedAt)
	}
	return watermark, checkedAt, err
}

// Incremental recalcule les séries touchées depuis le filigrane. Renvoie le
// nombre de séries recalculées.
func (s *Store) Incremental(ctx context.Context) (int, error) {
	mark, _, err := s.loadState(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for {
		maxID, pairs, n, err := s.historyRows(ctx, mark, 500)
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, nil
		}
		for i := range pairs {
			p := &pairs[i]
			if p.row, p.ok, err = s.calc(ctx, p.uid, p.show); err != nil {
				return total, err
			}
		}
		if err := s.writeAll(ctx, pairs); err != nil {
			return total, err
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE pse_continue_next_state SET watermark=? WHERE id=1`, maxID); err != nil {
			return total, err
		}
		mark, total = maxID, total+len(pairs)
		if n < 500 {
			return total, nil
		}
	}
}

// NewEpisodes recalcule les séries à jour (aucun épisode suivant) dont un
// épisode est sorti depuis le dernier passage.
func (s *Store) NewEpisodes(ctx context.Context) (int, error) {
	_, checked, err := s.loadState(ctx)
	if err != nil {
		return 0, err
	}
	now := s.now()
	total, err := s.refreshAiring(ctx)
	if err != nil {
		return total, err
	}
	q, err := s.DB.QueryContext(ctx, `SELECT DISTINCT show_key FROM catalog_titles
WHERE type='episode' AND release_known=1 AND season>0 AND adult=0 AND released_ts>? AND released_ts<=?`,
		checked-int64(releaseOverlap/time.Second), now.Unix())
	if err != nil {
		return total, err
	}
	var shows []int64
	for q.Next() {
		var k int64
		if err := q.Scan(&k); err != nil {
			q.Close()
			return 0, err
		}
		shows = append(shows, k)
	}
	q.Close()
	if err := q.Err(); err != nil {
		return 0, err
	}
	for start := 0; start < len(shows); start += 200 {
		part := shows[start:min(start+200, len(shows))]
		args := make([]any, len(part))
		for i, k := range part {
			args[i] = k
		}
		rs, err := s.DB.QueryContext(ctx, `SELECT user_id, show_key FROM pse_continue_next WHERE next_key=0 AND show_key IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")+`)`, args...)
		if err != nil {
			return total, err
		}
		var batch []pending
		for rs.Next() {
			var p pending
			var show int64
			if err := rs.Scan(&p.uid, &show); err != nil {
				rs.Close()
				return total, err
			}
			p.show = core.RatingKey(show)
			batch = append(batch, p)
		}
		rs.Close()
		if err := rs.Err(); err != nil {
			return total, err
		}
		for i := range batch {
			if batch[i].row, batch[i].ok, err = s.calc(ctx, batch[i].uid, batch[i].show); err != nil {
				return total, err
			}
		}
		if err := s.writeAll(ctx, batch); err != nil {
			return total, err
		}
		total += len(batch)
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE pse_continue_next_state SET checked_at=? WHERE id=1`, now.Unix())
	return total, err
}

// Fill remplit la table des comptes qui n'ont pas encore la marque « rempli », par lots de séries et avec une pause entre les lots.
func (s *Store) Fill(ctx context.Context) error {
	q, err := s.DB.QueryContext(ctx, `SELECT id FROM auth_users u WHERE NOT EXISTS(SELECT 1 FROM pse_continue_next_filled f WHERE f.user_id=u.id)`)
	if err != nil {
		return err
	}
	var users []int64
	for q.Next() {
		var u int64
		if err := q.Scan(&u); err != nil {
			q.Close()
			return err
		}
		users = append(users, u)
	}
	q.Close()
	if err := q.Err(); err != nil {
		return err
	}
	sort.Slice(users, func(i, j int) bool { return users[i] < users[j] })
	lo := int64(core.EpisodeKey(0, 0, 0))
	for _, uid := range users {
		// Sans épisode terminé, le compte n'est pas marqué rempli : la rangée
		// garde l'ancien calcul jusqu'à son premier visionnage.
		var has int
		err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM state_history WHERE user_id=? AND completed=1 AND rating_key>=? AND rating_key<? LIMIT 1`,
			uid, lo, lo+1_000_000_000_000_000).Scan(&has)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if err := s.fillViewer(ctx, uid); err != nil {
			return fmt.Errorf("remplissage du spectateur %d : %w", uid, err)
		}
	}
	return nil
}

// fillViewer calcule toutes les séries d'un spectateur (même regroupement que
// ContinueSeriesHistory, sans limite), puis pose la marque « rempli ».
func (s *Store) fillViewer(ctx context.Context, uid int64) error {
	lo := int64(core.EpisodeKey(0, 0, 0))
	q, err := s.DB.QueryContext(ctx, `SELECT DISTINCT rating_key/10000000 FROM state_history
WHERE user_id=? AND completed=1 AND rating_key>=? AND rating_key<?`, uid, lo, lo+1_000_000_000_000_000)
	if err != nil {
		return err
	}
	var shows []core.RatingKey
	for q.Next() {
		var g int64
		if err := q.Scan(&g); err != nil {
			q.Close()
			return err
		}
		shows = append(shows, core.ShowKey(core.RatingKey(g*10_000_000).TMDB()))
	}
	q.Close()
	if err := q.Err(); err != nil {
		return err
	}
	for start := 0; start < len(shows); start += fillBatch {
		batch := make([]pending, 0, fillBatch)
		for _, show := range shows[start:min(start+fillBatch, len(shows))] {
			row, ok, err := s.calc(ctx, uid, show)
			if err != nil {
				return err
			}
			batch = append(batch, pending{uid, show, row, ok})
		}
		if err := s.writeAll(ctx, batch); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.Pause):
		}
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO pse_continue_next_filled(user_id, filled_at) VALUES(?,?) ON CONFLICT(user_id) DO NOTHING`, uid, s.now().Unix())
	return err
}

// Run : remplissage initial, puis passe incrémentale (30 s) et passe des
// nouveaux épisodes (30 min), jusqu'à l'annulation du contexte. onError reçoit
// les erreurs de chaque passe (elles sont réessayées à la suivante).
func (s *Store) Run(ctx context.Context, onError func(string, error)) {
	report := func(what string, err error) {
		if err != nil && ctx.Err() == nil && onError != nil {
			onError(what, err)
		}
	}
	// Le filigrane est posé avant le remplissage : un visionnage fait pendant
	// celui-ci sera rattrapé par la passe incrémentale.
	_, _, err := s.loadState(ctx)
	report("filigrane", err)
	report("remplissage", s.Fill(ctx))
	inc, slow, comp := time.NewTicker(IncrementalEvery), time.NewTicker(NewEpisodesEvery), time.NewTicker(CompleteEvery)
	defer inc.Stop()
	defer slow.Stop()
	defer comp.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-inc.C:
			_, err := s.Incremental(ctx)
			report("passe incrémentale", err)
		case <-comp.C:
			_, err := s.Complete(ctx)
			report("rattrapage des épisodes", err)
		case <-slow.C:
			_, err := s.NewEpisodes(ctx)
			report("nouveaux épisodes", err)
			report("remplissage", s.Fill(ctx))
		}
	}
}

// airingPerPass borne les séries dont la saison suivante est chargée à chaque passe.
const airingPerPass = 100

// refreshAiring charge la saison suivante des séries ni terminées ni annulées
// (en cours, en production, prévues) dont un
// spectateur est à jour, puis recalcule leurs lignes. Jamais pendant un affichage.
func (s *Store) refreshAiring(ctx context.Context) (int, error) {
	if s.Refresh == nil {
		return 0, nil
	}
	q, err := s.DB.QueryContext(ctx, `SELECT p.show_key, MAX(p.last_key) FROM pse_continue_next p
JOIN catalog_titles sh ON sh.key=p.show_key AND sh.type='show'
WHERE p.next_key=0 AND LOWER(COALESCE(sh.status,'')) NOT IN ('ended','canceled')
GROUP BY p.show_key ORDER BY MIN(p.updated_at) LIMIT ?`, airingPerPass)
	if err != nil {
		return 0, err
	}
	type target struct{ show, last int64 }
	var targets []target
	for q.Next() {
		var t target
		if err := q.Scan(&t.show, &t.last); err != nil {
			q.Close()
			return 0, err
		}
		targets = append(targets, t)
	}
	q.Close()
	if err := q.Err(); err != nil {
		return 0, err
	}
	total := 0
	for _, t := range targets {
		if s.Busy != nil && s.Busy() {
			break // reprise à la passe suivante, après la lecture
		}
		s.Refresh(ctx, []core.RatingKey{core.RatingKey(t.last)})
		rs, err := s.DB.QueryContext(ctx, `SELECT user_id FROM pse_continue_next WHERE show_key=? AND next_key=0`, t.show)
		if err != nil {
			return total, err
		}
		var batch []pending
		for rs.Next() {
			p := pending{show: core.RatingKey(t.show)}
			if err := rs.Scan(&p.uid); err != nil {
				rs.Close()
				return total, err
			}
			batch = append(batch, p)
		}
		rs.Close()
		if err := rs.Err(); err != nil {
			return total, err
		}
		for i := range batch {
			if batch[i].row, batch[i].ok, err = s.calc(ctx, batch[i].uid, batch[i].show); err != nil {
				return total, err
			}
		}
		if err := s.writeAll(ctx, batch); err != nil {
			return total, err
		}
		total += len(batch)
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(s.Pause):
		}
	}
	return total, nil
}

// Complete charge depuis TMDB les épisodes des séries de l'historique dont
// aucun suivant n'est connu (catalogue incomplet : Ether n'enregistre les
// épisodes qu'à l'ouverture d'une fiche), puis recalcule leurs lignes. Les
// séries vues le plus récemment passent d'abord ; chacune est marquée et
// revérifiée après completeAgain (6 h). Les séries ni terminées ni annulées
// le sont en plus toutes les 30 minutes par refreshAiring. Rien pendant une lecture ni un affichage.
func (s *Store) Complete(ctx context.Context) (int, error) {
	if s.Refresh == nil || (s.Busy != nil && s.Busy()) {
		return 0, nil
	}
	now := s.now()
	q, err := s.DB.QueryContext(ctx, `SELECT p.show_key, MAX(p.last_key) FROM pse_continue_next p
LEFT JOIN pse_continue_next_completed c ON c.show_key=p.show_key
WHERE p.next_key=0 AND (c.show_key IS NULL OR c.completed_at<?)
GROUP BY p.show_key ORDER BY MAX(p.last_watched_at) DESC LIMIT ?`, now.Add(-completeAgain).Unix(), completePerPass)
	if err != nil {
		return 0, err
	}
	type target struct{ show, last int64 }
	var targets []target
	for q.Next() {
		var t target
		if err := q.Scan(&t.show, &t.last); err != nil {
			q.Close()
			return 0, err
		}
		targets = append(targets, t)
	}
	q.Close()
	if err := q.Err(); err != nil {
		return 0, err
	}
	total := 0
	for _, t := range targets {
		if ctx.Err() != nil || (s.Busy != nil && s.Busy()) {
			break
		}
		s.Refresh(ctx, []core.RatingKey{core.RatingKey(t.last)})
		n, err := s.recalcShow(ctx, t.show)
		total += n
		if err != nil {
			return total, err
		}
		if _, err := s.DB.ExecContext(ctx, `INSERT INTO pse_continue_next_completed(show_key, completed_at) VALUES(?,?)
ON CONFLICT(show_key) DO UPDATE SET completed_at=excluded.completed_at`, t.show, now.Unix()); err != nil {
			return total, err
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(s.Pause):
		}
	}
	return total, nil
}

// recalcShow recalcule les lignes sans suivant d'une série, pour tous les spectateurs.
func (s *Store) recalcShow(ctx context.Context, show int64) (int, error) {
	rs, err := s.DB.QueryContext(ctx, `SELECT user_id FROM pse_continue_next WHERE show_key=? AND next_key=0`, show)
	if err != nil {
		return 0, err
	}
	var batch []pending
	for rs.Next() {
		p := pending{show: core.RatingKey(show)}
		if err := rs.Scan(&p.uid); err != nil {
			rs.Close()
			return 0, err
		}
		batch = append(batch, p)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return 0, err
	}
	for i := range batch {
		if batch[i].row, batch[i].ok, err = s.calc(ctx, batch[i].uid, batch[i].show); err != nil {
			return 0, err
		}
	}
	return len(batch), s.writeAll(ctx, batch)
}
