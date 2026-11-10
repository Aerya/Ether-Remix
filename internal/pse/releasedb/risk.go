package releasedb

import (
	"context"
	"strconv"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/seerrcatalog"
)

// Titres risqués : avec PSE_PLAYABLE_SINGLE=on, un titre qui n'a qu'une
// partie des releases demandées (PSE_PLAYABLE_MIN) reste affiché, mais
// signalé aux spectateurs dans Plex et Jellyfin : la lecture peut échouer
// si cette release disparaît. Le signalement se fait au début du résumé
// (MarkSummary) et/ou par un badge sur les affiches Ether (PosterRisk).
//
// La liste est tenue en mémoire et relue par l'indexeur (riskEvery) : les
// clients la consultent à chaque affichage sans requête SQL.

// riskSet associe un titre risqué à son nombre de releases lisibles.
type riskSet struct {
	keys    map[core.RatingKey]int
	summary bool
	poster  bool
}

var riskCur atomic.Pointer[riskSet]

// SetRisk remplace la liste des titres risqués (tests et indexeur).
func SetRisk(keys map[core.RatingKey]int, p PlayableSettings) {
	riskCur.Store(&riskSet{keys: keys, summary: p.Mark != "poster", poster: p.Mark != "summary"})
}

func riskOf(key core.RatingKey) (int, *riskSet) {
	r := riskCur.Load()
	if r == nil {
		return 0, nil
	}
	return r.keys[key], r
}

// MarkSummary ajoute l'avertissement au début du résumé d'un titre risqué.
func MarkSummary(key core.RatingKey, summary string) string {
	n, r := riskOf(key)
	if n == 0 || !r.summary {
		return summary
	}
	mark := "⚠ " + sourcesLabel(n) + " : lecture incertaine"
	if summary == "" {
		return mark
	}
	return mark + "\n\n" + summary
}

// PosterRisk renvoie le badge d'affiche d'un titre risqué ("" sinon).
func PosterRisk(key core.RatingKey) string {
	n, r := riskOf(key)
	if n == 0 || !r.poster {
		return ""
	}
	if n == 1 {
		return "1 SOURCE"
	}
	return strconv.Itoa(n) + " SOURCES"
}

func sourcesLabel(n int) string {
	if n == 1 {
		return "1 source"
	}
	return strconv.Itoa(n) + " sources"
}

// riskKeys lit les titres affichés grâce à PSE_PLAYABLE_SINGLE : au moins
// une release lisible, moins que Min, et pas de disponibilité confirmée
// par SeerrCatalog.
func (s *Store) riskKeys(ctx context.Context, p PlayableSettings) (map[core.RatingKey]int, error) {
	out := map[core.RatingKey]int{}
	if !p.Enabled || p.ShowMin() >= p.Min {
		return out, nil
	}
	exempt := LangExemptSQL("pp.title_key")
	trusted, targs := seerrcatalog.TrustedSQL("pp.title_key")
	q := `SELECT pp.title_key, CASE WHEN ` + exempt + ` AND pp.any_lang>pp.releases THEN pp.any_lang ELSE pp.releases END
FROM pse_playable pp
WHERE (pp.releases>=1 OR (pp.any_lang>=1 AND ` + exempt + `))
  AND NOT (pp.releases>=? OR (pp.any_lang>=? AND ` + exempt + `))
  AND NOT ` + trusted
	rows, err := s.db.QueryContext(ctx, q, append([]any{p.Min, p.Min}, targs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k int64
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		if n > 0 {
			out[core.RatingKey(k)] = n
		}
	}
	return out, rows.Err()
}

// refreshRisk relit la liste des titres risqués.
func (s *Service) refreshRisk(ctx context.Context) {
	p := Playable()
	keys, err := s.st.riskKeys(ctx, p)
	if err != nil {
		if ctx.Err() == nil {
			s.d.Logger.Warn("pse risky titles refresh failed", "err", err)
		}
		return
	}
	SetRisk(keys, p)
}

// RiskMarker expose MarkSummary et PosterRisk aux clients
// (frontend.SetPSERisk).
type RiskMarker struct{}

func (RiskMarker) MarkSummary(key core.RatingKey, summary string) string {
	return MarkSummary(key, summary)
}
func (RiskMarker) PosterRisk(key core.RatingKey) string { return PosterRisk(key) }
