package releasedb

import (
	"context"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// ConsoleStats résume l'état de l'indexation pour la console PSE.
type ConsoleStats struct {
	Titles      int            `json:"titles"`      // titres suivis
	Indexed     int            `json:"indexed"`     // titres déjà interrogés
	Found       int            `json:"found"`       // titres avec au moins une release
	Releases    int            `json:"releases"`    // releases enregistrées
	Playable    int            `json:"playable"`    // titres au-dessus du seuil « lisible »
	LastIndexed int64          `json:"lastIndexed"` // dernier passage (Unix), 0 si aucun
	Today       map[string]int `json:"today"`       // requêtes du jour par source
}

// ConsoleStats compte les titres, releases et requêtes du jour.
func (s *Store) ConsoleStats(ctx context.Context, min int) (ConsoleStats, error) {
	var st ConsoleStats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN last_indexed>0 THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN found>0 THEN 1 ELSE 0 END),0), COALESCE(MAX(last_indexed),0) FROM pse_index_titles`).
		Scan(&st.Titles, &st.Indexed, &st.Found, &st.LastIndexed); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_releases`).Scan(&st.Releases); err != nil {
		return st, err
	}
	if min < 1 {
		min = 1
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_playable WHERE releases>=?`, min).Scan(&st.Playable); err != nil {
		return st, err
	}
	st.Today = map[string]int{}
	rows, err := s.db.QueryContext(ctx, `SELECT source_id, hits FROM pse_index_budget WHERE day=?`, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return st, err
		}
		st.Today[id] = n
	}
	return st, rows.Err()
}

// Counts détaille l'avancement de l'indexation d'un ensemble de titres.
type Counts struct {
	Titles   int `json:"titles"`   // titres de l'ensemble
	Indexed  int `json:"indexed"`  // déjà cherchés au moins une fois
	Found    int `json:"found"`    // au moins une release trouvée
	Playable int `json:"playable"` // assez de releases pour être affichés
	Recent   int `json:"recent"`   // cherchés ces dernières 24 h
	// Stale : affichés, mais dernière recherche plus ancienne que Stale :
	// la disponibilité repose sur une observation ancienne.
	Stale int `json:"stale"`
	// Failing : dernière recherche en panne ou partielle (au moins une
	// source n'a pas répondu) ; nouvel essai sous 24 h.
	Failing int `json:"failing"`
}

// TypeCounts compte l'avancement par type de média (movie, show), sur
// tout le catalogue navigable d'Ether.
func (s *Store) TypeCounts(ctx context.Context, min int) (map[string]Counts, error) {
	if min < 1 {
		min = 1
	}
	since := s.now().Add(-24 * time.Hour).Unix()
	stale := s.now().Add(-Stale).Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT c.type, COUNT(*),
  COALESCE(SUM(CASE WHEN t.last_indexed>0 THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN t.found>0 THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN p.releases>=? THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN t.last_indexed>=? THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN p.releases>=? AND t.last_indexed<? THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN t.status IN ('failed','partial') THEN 1 ELSE 0 END),0)
FROM catalog_titles c
LEFT JOIN pse_index_titles t ON t.title_key=c.key
LEFT JOIN pse_playable p ON p.title_key=c.key
WHERE c.type IN ('movie','show') AND c.browsable=1
GROUP BY c.type`, min, since, min, stale)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Counts{}
	for rows.Next() {
		var typ string
		var c Counts
		if err := rows.Scan(&typ, &c.Titles, &c.Indexed, &c.Found, &c.Playable, &c.Recent, &c.Stale, &c.Failing); err != nil {
			return nil, err
		}
		out[typ] = c
	}
	return out, rows.Err()
}

// KeyCounts compte l'avancement d'une liste de titres (collection,
// bibliothèque). Un titre absent de l'index compte comme pas encore cherché.
func (s *Store) KeyCounts(ctx context.Context, keys []core.RatingKey, min int) (Counts, error) {
	if min < 1 {
		min = 1
	}
	seen := make(map[core.RatingKey]bool, len(keys))
	uniq := make([]any, 0, len(keys))
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			uniq = append(uniq, int64(k))
		}
	}
	c := Counts{Titles: len(uniq)}
	since := s.now().Add(-24 * time.Hour).Unix()
	stale := s.now().Add(-Stale).Unix()
	const chunk = 500
	for i := 0; i < len(uniq); i += chunk {
		part := uniq[i:min2(i+chunk, len(uniq))]
		args := append([]any{min, since, min, stale}, part...)
		var p Counts
		if err := s.db.QueryRowContext(ctx, `SELECT
  COALESCE(SUM(CASE WHEN t.last_indexed>0 THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN t.found>0 THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN p.releases>=? THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN t.last_indexed>=? THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN p.releases>=? AND t.last_indexed<? THEN 1 ELSE 0 END),0),
  COALESCE(SUM(CASE WHEN t.status IN ('failed','partial') THEN 1 ELSE 0 END),0)
FROM pse_index_titles t LEFT JOIN pse_playable p ON p.title_key=t.title_key
WHERE t.title_key IN (?`+strings.Repeat(",?", len(part)-1)+`)`, args...).
			Scan(&p.Indexed, &p.Found, &p.Playable, &p.Recent, &p.Stale, &p.Failing); err != nil {
			return c, err
		}
		c.Indexed += p.Indexed
		c.Found += p.Found
		c.Playable += p.Playable
		c.Recent += p.Recent
		c.Stale += p.Stale
		c.Failing += p.Failing
	}
	return c, nil
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}
