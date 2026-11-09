package themes

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// Exceptions de langue : le catalogue de PSE peut ne garder que les titres
// disponibles en VF (PSE_PLAYABLE_FILTER, PSE_FRENCH). Les titres des
// bibliothèques « toutes langues » (concerts, spectacles…) et des
// collections d'exception (une sélection en VOSTFR…) y échappent : l'app
// publie ExemptKeys dans releasedb (SetLangExempt).

// MaxExceptions borne le nombre de collections d'exception.
const MaxExceptions = 60

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Service) loadFree(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT collection_id FROM pse_theme_langfree ORDER BY position, collection_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Exceptions renvoie les collections exemptées du filtre de langue.
func (s *Service) Exceptions() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string{}, s.free...)
}

// SetExceptions remplace les collections exemptées du filtre de langue.
func (s *Service) SetExceptions(ctx context.Context, ids []string) error {
	seen := map[string]bool{}
	var cols []string
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			cols = append(cols, id)
		}
	}
	if len(cols) > MaxExceptions {
		return invalid("%d collections d'exception au plus", MaxExceptions)
	}
	if s.cols != nil && len(cols) > 0 {
		specs, err := s.cols.ListSpecs(ctx)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for _, sp := range specs {
			known[sp.ID] = true
		}
		for _, c := range cols {
			if !known[c] {
				return invalid("collection %s introuvable", c)
			}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM pse_theme_langfree`); err != nil {
		return err
	}
	for i, c := range cols {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pse_theme_langfree(collection_id, position) VALUES(?, ?)`, c, i); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.Reload(ctx)
}

// ExemptKeys renvoie les titres qui échappent au filtre de langue : ceux
// des bibliothèques « toutes langues » et des collections d'exception.
func (s *Service) ExemptKeys(ctx context.Context) []core.RatingKey {
	if s.cols == nil {
		return nil
	}
	seen := map[string]bool{}
	var cols []string
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			cols = append(cols, id)
		}
	}
	for _, id := range s.Exceptions() {
		add(id)
	}
	for _, l := range s.Libraries() {
		if l.AllLanguages {
			for _, id := range l.Collections {
				add(id)
			}
		}
	}
	keys := map[core.RatingKey]bool{}
	out := []core.RatingKey{}
	for _, id := range cols {
		for _, k := range s.cols.PSEKeys(ctx, id, nil) {
			if !keys[k] {
				keys[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}
