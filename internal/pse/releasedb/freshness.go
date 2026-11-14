package releasedb

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Fraîcheur des observations de releases.
//
// Une release est une observation : « telle source proposait telle release
// pour tel titre (et épisode) à telle date ». Trois règles la tiennent à
// jour :
//
//   - Recherche complète réussie (la source a répondu sans erreur) : les
//     releases de cette source pour ce titre et cet épisode qui n'ont pas
//     été revues sont effacées (Replace). Les autres sources et les autres
//     épisodes ne sont pas touchés.
//   - Recherche partielle (erreur avec quelques résultats), panne ou budget
//     épuisé : rien n'est effacé, les observations précédentes restent.
//   - Expiration : une observation qui n'a pas été revue depuis MaxAge ne
//     compte plus et est purgée (Maintain), même si la source est en panne
//     depuis. Les releases d'une source supprimée ou désactivée dans Ether
//     ne comptent plus dès que la maintenance l'a constaté ; elles restent
//     en base jusqu'à leur expiration, au cas où la source revient.

// MaxAge : au-delà, une observation non revue ne compte plus. Plus de
// quatre cycles de réindexation (14 jours pour un titre trouvé), pour
// qu'une panne de quelques jours ne vide pas le catalogue.
const MaxAge = 60 * 24 * time.Hour

// Stale : une observation plus ancienne est signalée comme « ancienne »
// dans la console (elle compte encore jusqu'à MaxAge).
const Stale = 21 * 24 * time.Hour

// activeSources liste les sources (identifiants d'indexeurs d'Ether)
// actives ; nil tant que la maintenance ne l'a pas renseignée : toutes
// les sources comptent alors.
var activeSources atomic.Pointer[map[string]bool]

func sourceActive(id string) bool {
	m := activeSources.Load()
	return m == nil || (*m)[id]
}

// XDCCSourceID est la source XDCC d'Ether-Remix (internal/pse/xdcc).
const XDCCSourceID = "remix-xdcc"

// XDCCMaxAge : un paquet XDCC ne compte pour le catalogue lisible que s'il
// a été revu récemment. La recherche XDCC ne rend que les bots en ligne et
// sans échec récent ; un bot hors ligne ou en échec fait donc sortir ses
// paquets du compte au bout de ce délai.
const XDCCMaxAge = 48 * time.Hour

// counts indique si une release compte pour le catalogue lisible.
func counts(r Release, now time.Time) bool {
	if r.SourceID == XDCCSourceID && now.Sub(r.LastSeen) > XDCCMaxAge {
		return false
	}
	return sourceActive(r.SourceID) && now.Sub(r.LastSeen) <= MaxAge
}

// Scope désigne le périmètre d'une recherche : une source, un titre et un
// épisode (0/0 pour un film).
type Scope struct {
	SourceID        string
	TitleKey        core.RatingKey
	Season, Episode int
}

// Replace enregistre le résultat d'une recherche complète réussie : les
// releases trouvées sont ajoutées ou rafraîchies, celles du même
// périmètre qui n'ont pas été revues sont effacées. Renvoie le nombre de
// releases effacées.
func (s *Store) Replace(ctx context.Context, sc Scope, rs []Release, now time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := saveTx(ctx, tx, rs, now); err != nil {
		return 0, err
	}
	q := `DELETE FROM pse_releases WHERE source_id=? AND title_key=? AND season=? AND episode=?`
	args := []any{sc.SourceID, int64(sc.TitleKey), sc.Season, sc.Episode}
	if len(rs) > 0 {
		q += ` AND id NOT IN (?` + strings.Repeat(",?", len(rs)-1) + `)`
		for _, r := range rs {
			args = append(args, r.ID)
		}
	}
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), tx.Commit()
}

// MaintainResult résume une maintenance.
type MaintainResult struct {
	Expired    int // releases purgées (non revues depuis MaxAge)
	Recomputed int // titres dont le compte lisible a été recalculé
}

// Maintain purge les observations expirées, applique la liste des sources
// actives et recalcule le compte lisible des titres concernés. active liste
// les identifiants d'indexeurs activés dans Ether ; nil laisse toutes les
// sources compter.
func (s *Store) Maintain(ctx context.Context, now time.Time, active []string, p PlayableSettings) (MaintainResult, error) {
	var res MaintainResult
	affected := map[core.RatingKey]bool{}

	// Sources : titres touchés par une source qui change d'état.
	if active != nil {
		set := make(map[string]bool, len(active))
		for _, id := range active {
			set[id] = true
		}
		ids := append([]string(nil), active...)
		sort.Strings(ids)
		sig := strings.Join(ids, ",")
		var prev string
		err := s.db.QueryRowContext(ctx, `SELECT v FROM pse_state WHERE k='active_sources'`).Scan(&prev)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return res, err
		}
		known := err == nil
		activeSources.Store(&set)
		if !known || prev != sig {
			was := map[string]bool{}
			if prev != "" {
				for _, id := range strings.Split(prev, ",") {
					was[id] = true
				}
			}
			rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT source_id, title_key FROM pse_releases`)
			if err != nil {
				return res, err
			}
			for rows.Next() {
				var src string
				var k int64
				if rows.Scan(&src, &k) != nil {
					continue
				}
				// Sans liste précédente, toutes les sources comptaient.
				changed := !set[src]
				if known {
					changed = was[src] != set[src]
				}
				if changed {
					affected[core.RatingKey(k)] = true
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return res, err
			}
			if _, err := s.db.ExecContext(ctx, `INSERT INTO pse_state(k,v) VALUES('active_sources',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, sig); err != nil {
				return res, err
			}
		}
	}

	// Expiration.
	before := now.Add(-MaxAge).Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT title_key FROM pse_releases WHERE last_seen<?`, before)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var k int64
		if rows.Scan(&k) == nil {
			affected[core.RatingKey(k)] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}
	r, err := s.db.ExecContext(ctx, `DELETE FROM pse_releases WHERE last_seen<?`, before)
	if err != nil {
		return res, err
	}
	n, _ := r.RowsAffected()
	res.Expired = int(n)

	if !p.Enabled {
		return res, nil
	}
	keys := make([]core.RatingKey, 0, len(affected))
	for k := range affected {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if err := s.RefreshPlayable(ctx, k, p); err != nil {
			return res, err
		}
		res.Recomputed++
	}
	return res, nil
}
