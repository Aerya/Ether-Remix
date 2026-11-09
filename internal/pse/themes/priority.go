package themes

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Priorité de recherche de releases, choisie par collection ou bibliothèque
// dans la console : 0 normale, 1 prioritaire, 2 maximale. Le titre garde le
// niveau le plus haut parmi ses collections et bibliothèques.

func init() {
	store.Register("pse_themes", 3, `
CREATE TABLE pse_theme_priority (
  kind TEXT NOT NULL,
  target TEXT NOT NULL,
  level INTEGER NOT NULL,
  PRIMARY KEY (kind, target)
);`)
}

// Cibles d'une priorité.
const (
	PriorityCollection = "collection"
	PriorityLibrary    = "library"
)

// MaxPriorityLevel est le niveau le plus haut.
const MaxPriorityLevel = 2

// Priority est une priorité enregistrée.
type Priority struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Level  int    `json:"level"`
	Titles int    `json:"titles"`
}

// targetKeys renvoie les titres d'une cible.
func (s *Service) targetKeys(ctx context.Context, kind, id string) []core.RatingKey {
	if s.cols == nil {
		return nil
	}
	if kind == PriorityCollection {
		return s.cols.PSEKeys(ctx, id, nil)
	}
	var out []core.RatingKey
	for _, l := range s.Libraries() {
		if strconv.FormatInt(l.ID, 10) != id {
			continue
		}
		for _, c := range l.Collections {
			out = append(out, s.cols.PSEKeys(ctx, c, nil)...)
		}
	}
	return out
}

func (s *Service) loadPriorities(ctx context.Context) ([]Priority, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT kind,target,level FROM pse_theme_priority ORDER BY level DESC, kind, target`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Priority{}
	for rows.Next() {
		var p Priority
		if err := rows.Scan(&p.Kind, &p.ID, &p.Level); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Priorities renvoie les priorités avec leur nombre de titres, et le nombre
// total de titres prioritaires distincts.
func (s *Service) Priorities(ctx context.Context) ([]Priority, int, error) {
	ps, err := s.loadPriorities(ctx)
	if err != nil {
		return nil, 0, err
	}
	seen := map[core.RatingKey]bool{}
	for i := range ps {
		keys := s.targetKeys(ctx, ps[i].Kind, ps[i].ID)
		ps[i].Titles = len(keys)
		for _, k := range keys {
			seen[k] = true
		}
	}
	return ps, len(seen), nil
}

// PriorityLevels renvoie le niveau de chaque titre priorisé d'un type.
func (s *Service) PriorityLevels(ctx context.Context, typ core.MediaType) map[core.RatingKey]int {
	ps, err := s.loadPriorities(ctx)
	if err != nil {
		return nil
	}
	want := core.KindMovie
	if typ == core.TypeShow {
		want = core.KindShow
	}
	out := map[core.RatingKey]int{}
	for _, p := range ps {
		for _, k := range s.targetKeys(ctx, p.Kind, p.ID) {
			if k.Kind() == want && p.Level > out[k] {
				out[k] = p.Level
			}
		}
	}
	return out
}

func (s *Service) checkTarget(ctx context.Context, kind, id string) error {
	switch kind {
	case PriorityCollection:
		if s.cols == nil {
			return invalid("collections indisponibles")
		}
		specs, err := s.cols.ListSpecs(ctx)
		if err != nil {
			return err
		}
		for _, sp := range specs {
			if sp.ID == id && sp.OwnerUserID == 0 {
				return nil
			}
		}
		return invalid("collection %s introuvable", id)
	case PriorityLibrary:
		for _, l := range s.Libraries() {
			if strconv.FormatInt(l.ID, 10) == id {
				return nil
			}
		}
		return invalid("bibliothèque %s introuvable", id)
	}
	return invalid("cible « collection » ou « library » attendue")
}

// SetPriority enregistre le niveau d'une cible ; le niveau 0 l'efface.
func (s *Service) SetPriority(ctx context.Context, kind, id string, level int) error {
	if level < 0 || level > MaxPriorityLevel {
		return invalid("niveau de 0 à %d attendu", MaxPriorityLevel)
	}
	if err := s.checkTarget(ctx, kind, id); err != nil {
		return err
	}
	var err error
	if level == 0 {
		_, err = s.db.ExecContext(ctx, `DELETE FROM pse_theme_priority WHERE kind=? AND target=?`, kind, id)
	} else {
		_, err = s.db.ExecContext(ctx, `INSERT INTO pse_theme_priority(kind,target,level) VALUES(?,?,?) ON CONFLICT(kind,target) DO UPDATE SET level=excluded.level`, kind, id, level)
	}
	return err
}

// SearchKeys renvoie les titres d'une cible, pour « Chercher maintenant ».
func (s *Service) SearchKeys(ctx context.Context, kind, id string) ([]core.RatingKey, error) {
	if err := s.checkTarget(ctx, kind, id); err != nil {
		return nil, err
	}
	return s.targetKeys(ctx, kind, id), nil
}

func forgetLibraryPriority(ctx context.Context, db *sql.DB, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM pse_theme_priority WHERE kind=? AND target=?`, PriorityLibrary, strconv.FormatInt(id, 10))
	return err
}

// PriorityHooks relie l'API à l'indexeur.
type PriorityHooks struct {
	Search  func(context.Context, []core.RatingKey) (int, error)
	Changed func()
	Max     int
}

// SetPriorityHooks enregistre les liens vers l'indexeur.
func (s *Service) SetPriorityHooks(h PriorityHooks) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hooks = h
}

func (s *Service) priorityHooks() PriorityHooks {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hooks
}
