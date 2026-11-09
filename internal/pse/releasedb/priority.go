package releasedb

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Priorité de recherche choisie dans la console, par collection ou
// bibliothèque (internal/pse/themes/priority.go). Un titre sans release
// d'un niveau élevé est retenté plus tôt que le délai normal ; les titres
// avec releases gardent le leur. Les budgets par source restent appliqués.

// Niveaux de priorité.
const (
	LevelNormal = 0
	LevelHigh   = 1
	LevelMax    = 2
)

const (
	retryHigh = 24 * time.Hour
	retryMax  = 6 * time.Hour
)

// PriorityMax borne le nombre de titres prioritaires pris en compte.
const PriorityMax = priorityMax

// ErrIndexerOff signale une recherche demandée alors que l'indexation est
// désactivée.
var ErrIndexerOff = errors.New("releasedb: indexation désactivée")

// PriorityKey est un titre à indexer avant le reste du catalogue.
type PriorityKey struct {
	Key   core.RatingKey
	Level int
}

func retryFor(level int) time.Duration {
	switch {
	case level >= LevelMax:
		return retryMax
	case level == LevelHigh:
		return retryHigh
	}
	return 0
}

// orderPriority retire les doublons (le niveau le plus haut l'emporte),
// place les niveaux élevés d'abord et applique la limite.
func orderPriority(in []PriorityKey) ([]core.RatingKey, map[core.RatingKey]int) {
	levels := make(map[core.RatingKey]int, len(in))
	uniq := make([]PriorityKey, 0, len(in))
	for _, p := range in {
		if old, seen := levels[p.Key]; seen {
			levels[p.Key] = max(old, p.Level)
			continue
		}
		levels[p.Key] = p.Level
		uniq = append(uniq, p)
	}
	sort.SliceStable(uniq, func(i, j int) bool { return levels[uniq[i].Key] > levels[uniq[j].Key] })
	if len(uniq) > priorityMax {
		uniq = uniq[:priorityMax]
	}
	keys := make([]core.RatingKey, len(uniq))
	for i, p := range uniq {
		keys[i] = p.Key
	}
	return keys, levels
}

// dueFor renvoie l'échéance d'un titre selon son niveau (zéro : jamais
// indexé).
func (s *Store) dueFor(ctx context.Context, key core.RatingKey, level int) (time.Time, error) {
	var due, last int64
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT next_due,last_indexed,found FROM pse_index_titles WHERE title_key=?`, int64(key)).Scan(&due, &last, &found)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	at := time.Unix(due, 0)
	if retry := retryFor(level); retry > 0 && found == 0 {
		if early := time.Unix(last, 0).Add(retry); early.Before(at) {
			at = early
		}
	}
	return at, nil
}

// clearDue rend des titres immédiatement à échéance.
func (s *Store) clearDue(ctx context.Context, keys []core.RatingKey) error {
	for len(keys) > 0 {
		n := min(len(keys), 500)
		args := make([]any, n)
		for i, k := range keys[:n] {
			args[i] = int64(k)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE pse_index_titles SET next_due=0 WHERE title_key IN (?`+strings.Repeat(",?", n-1)+`)`, args...); err != nil {
			return err
		}
		keys = keys[n:]
	}
	return nil
}

// InvalidatePriority fait relire la liste des titres prioritaires au
// prochain passage, après un changement dans la console.
func (s *Service) InvalidatePriority() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prio = map[core.MediaType][]core.Title{}
	s.prioAt = map[core.MediaType]time.Time{}
}

// SearchNow place des titres en tête de file, sans attendre leur délai.
// Renvoie le nombre de titres ajoutés. La file est en mémoire ; les titres
// restent à échéance en base si l'indexeur redémarre entre-temps.
func (s *Service) SearchNow(ctx context.Context, keys []core.RatingKey) (int, error) {
	if !s.d.Settings.Enabled || len(s.d.Settings.Types) == 0 {
		return 0, ErrIndexerOff
	}
	if len(keys) > priorityMax {
		keys = keys[:priorityMax]
	}
	if err := s.st.clearDue(ctx, keys); err != nil {
		return 0, err
	}
	added := 0
	for _, typ := range s.d.Settings.Types {
		var want []core.RatingKey
		for _, k := range keys {
			if (typ == core.TypeMovie && k.Kind() == core.KindMovie) || (typ == core.TypeShow && k.Kind() == core.KindShow) {
				want = append(want, k)
			}
		}
		if len(want) == 0 {
			continue
		}
		page, err := s.d.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: typ, IncludeKeys: want}, Sort: core.SortPopularity, Desc: true, Limit: len(want), NoTotal: true})
		if err != nil {
			return added, err
		}
		s.mu.Lock()
		queued := make(map[core.RatingKey]bool, len(s.urgent[typ]))
		for _, t := range s.urgent[typ] {
			queued[t.Key] = true
		}
		for _, t := range page.Items {
			if !queued[t.Key] {
				queued[t.Key] = true
				s.urgent[typ] = append(s.urgent[typ], t)
				added++
			}
		}
		s.mu.Unlock()
	}
	return added, nil
}
