package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/continuewatch"
)

// Ajout Remix — voir docs/pse/FORK.fr.md. Lecture de « Continuer à regarder »
// depuis la table « à suivre » (continuewatch.Store) : toutes les séries du
// spectateur et leur épisode suivant, sans lire l'historique ni les épisodes.
// Les filtres d'Ether (droits, sections, contrôle parental, masquages,
// favoris) et les réglages PSE_CONTINUE_* s'appliquent ensuite, comme avant.

// pseAppendFromTable ajoute l'épisode suivant de chaque série de la table.
// Il renvoie faux quand la table n'est pas prête pour ce spectateur : l'appelant
// garde alors le calcul d'Ether.
func (c Continue) pseAppendFromTable(ctx context.Context, viewer *core.User, run *pseContinue, titles []core.Title, state map[core.RatingKey]core.WatchStatus) ([]core.Title, bool) {
	if run == nil {
		return titles, false
	}
	rows, ok, err := continuewatch.Default().Next(ctx, viewer.ID)
	if err != nil || !ok {
		return titles, false
	}
	seen := map[int64]bool{}
	for _, t := range titles {
		if t.Type == core.TypeEpisode {
			seen[t.Key.TMDB()] = true
		}
	}
	var keep []continuewatch.Row
	var keys, shows []core.RatingKey
	for _, r := range rows {
		if seen[r.ShowKey.TMDB()] {
			continue
		}
		keep = append(keep, r)
		keys = append(keys, r.LastKey, r.NextKey)
		shows = append(shows, r.ShowKey)
	}
	if len(keep) == 0 {
		return titles, true
	}
	// Séries à ordre d'épisodes personnalisé : leur suivant vient de cet ordre.
	grouped := map[core.RatingKey]bool{}
	if c.EpisodeOrder != nil {
		g, err := c.EpisodeOrder.Grouped(ctx, viewer, shows)
		if err != nil {
			return titles, false
		}
		grouped = g
	}
	if c.PrefetchTitles != nil {
		c.PrefetchTitles(ctx, shows)
	}
	statuses, err := c.State.GetMany(ctx, viewer.ID, keys)
	if err != nil {
		return titles, false
	}
	byKey := map[core.RatingKey]core.Title{}
	for _, t := range c.GetTitles(ctx, nextKeys(keep, grouped)) {
		byKey[t.Key] = t
	}
	recomputed := 0
	for _, r := range keep {
		if ctx.Err() != nil {
			break
		}
		if statuses[r.LastKey].ViewCount == 0 {
			continue // remis à « non vu » depuis
		}
		var ep *core.Title
		if grouped[r.ShowKey] {
			ep = c.pseGroupedNext(ctx, viewer, r, run, statuses)
		} else if t, ok := byKey[r.NextKey]; ok {
			ep = &t
		}
		if ep == nil || ep.Season == 0 || (!ep.Released.IsZero() && ep.Released.After(run.now)) || c.SameDayUnavailable(ctx, ep) {
			continue
		}
		st := statuses[ep.Key]
		if st.ViewCount > 0 && st.ViewOffset <= 0 {
			// Vu sans événement d'historique (import d'état) : on recalcule la série.
			if ep, st = c.pseRecompute(ctx, viewer, r, statuses, &recomputed); ep == nil {
				continue
			}
		}
		st.Key = ep.Key
		if st.LastViewedAt.Before(r.LastWatchedAt) {
			st.LastViewedAt = r.LastWatchedAt
		}
		if !run.admit(r.LastWatchedAt, ep, st) {
			continue
		}
		state[ep.Key] = st
		titles = append(titles, *ep)
	}
	return titles, true
}

func nextKeys(rows []continuewatch.Row, grouped map[core.RatingKey]bool) []core.RatingKey {
	out := make([]core.RatingKey, 0, len(rows))
	for _, r := range rows {
		if !grouped[r.ShowKey] {
			out = append(out, r.NextKey)
		}
	}
	return out
}

// pseGroupedNext : premier épisode non vu, sorti, après le dernier dans l'ordre personnalisé.
func (c Continue) pseGroupedNext(ctx context.Context, viewer *core.User, r continuewatch.Row, run *pseContinue, statuses map[core.RatingKey]core.WatchStatus) *core.Title {
	eps, err := c.Grandchildren(ctx, r.ShowKey)
	if err != nil {
		return nil
	}
	for i := range eps {
		if eps[i].Key != r.LastKey {
			continue
		}
		after := eps[i+1:]
		keys := make([]core.RatingKey, 0, len(after))
		for _, e := range after {
			keys = append(keys, e.Key)
		}
		got, err := c.State.GetMany(ctx, viewer.ID, keys)
		if err != nil {
			return nil
		}
		for j := range after {
			e := &after[j]
			if e.Season == 0 {
				continue
			}
			if !e.Released.IsZero() && e.Released.After(run.now) {
				return nil
			}
			if st := got[e.Key]; st.ViewCount > 0 && st.ViewOffset <= 0 {
				continue
			}
			statuses[e.Key] = got[e.Key]
			return e
		}
	}
	return nil
}

// pseMaxRecompute borne les séries recalculées pendant un affichage.
const pseMaxRecompute = 20

// pseRecompute recalcule la ligne d'une série dont l'épisode suivant est déjà
// vu, relit la ligne et renvoie le nouvel épisode suivant (nil s'il n'y en a pas).
func (c Continue) pseRecompute(ctx context.Context, viewer *core.User, r continuewatch.Row, statuses map[core.RatingKey]core.WatchStatus, n *int) (*core.Title, core.WatchStatus) {
	if *n >= pseMaxRecompute {
		return nil, core.WatchStatus{}
	}
	*n++
	s := continuewatch.Default()
	if s == nil || s.Compute(ctx, viewer.ID, r.ShowKey) != nil {
		return nil, core.WatchStatus{}
	}
	row, ok, err := s.Row(ctx, viewer.ID, r.ShowKey)
	if err != nil || !ok || row.NextKey == 0 || row.NextKey == r.NextKey {
		return nil, core.WatchStatus{}
	}
	titles := c.GetTitles(ctx, []core.RatingKey{row.NextKey})
	got, err := c.State.GetMany(ctx, viewer.ID, []core.RatingKey{row.NextKey})
	if len(titles) == 0 || err != nil {
		return nil, core.WatchStatus{}
	}
	st := got[row.NextKey]
	if st.ViewCount > 0 && st.ViewOffset <= 0 {
		return nil, core.WatchStatus{}
	}
	statuses[row.NextKey] = st
	return &titles[0], st
}
