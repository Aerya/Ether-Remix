package experience

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// titlesWatched reports which titles v has fully watched. Movies, episodes,
// and plugin items check their own watch row; shows and seasons follow the
// same aired-episode rule as catalog's SQL watched predicate, overlaying any
// unflushed in-memory watch writes.
func (s *Service) titlesWatched(ctx context.Context, v *core.User, titles []core.Title) (map[core.RatingKey]bool, error) {
	out := make(map[core.RatingKey]bool, len(titles))
	if len(titles) == 0 {
		return out, nil
	}
	var leafKeys []core.RatingKey
	var containers []core.Title
	for _, t := range titles {
		if t.Key.Kind() == core.KindShow || t.Key.Kind() == core.KindSeason {
			containers = append(containers, t)
		} else {
			leafKeys = append(leafKeys, t.Key)
		}
	}
	if len(leafKeys) > 0 {
		if s.d.State == nil {
			return nil, core.ErrNotAvailable
		}
		states, err := s.d.State.GetMany(ctx, v.ID, leafKeys)
		if err != nil {
			return nil, err
		}
		for _, k := range leafKeys {
			if states[k].ViewCount > 0 {
				out[k] = true
			}
		}
	}
	if len(containers) == 0 {
		return out, nil
	}
	var pendingKeys []core.RatingKey
	var dirtyShows map[core.RatingKey]bool
	if pending, ok := s.d.State.(pendingWatchKeys); ok {
		pendingKeys = pending.PendingKeys(v.ID)
		for _, pk := range pendingKeys {
			var showKey core.RatingKey
			switch pk.Kind() {
			case core.KindShow:
				showKey = pk
			case core.KindSeason, core.KindEpisode:
				showKey = core.ShowKey(pk.TMDB())
			default:
				continue
			}
			if dirtyShows == nil {
				dirtyShows = map[core.RatingKey]bool{}
			}
			dirtyShows[showKey] = true
		}
	}
	var cleanKeys []core.RatingKey
	var dirty []core.Title
	for _, t := range containers {
		showKey := t.Key
		if t.Key.Kind() == core.KindSeason {
			showKey = core.ShowKey(t.Key.TMDB())
		}
		if s.d.WatchedTitles != nil && !dirtyShows[showKey] {
			cleanKeys = append(cleanKeys, t.Key)
		} else {
			dirty = append(dirty, t)
		}
	}
	if len(cleanKeys) > 0 {
		flags, err := s.d.WatchedTitles(ctx, v.ID, cleanKeys)
		if err != nil {
			return nil, err
		}
		for k, yes := range flags {
			if yes {
				out[k] = true
			}
		}
	}
	if len(dirty) == 0 {
		return out, nil
	}
	if err := s.containersWatchedWithPending(ctx, v, dirty, pendingKeys, out); err != nil {
		return nil, err
	}
	return out, nil
}

// containersWatchedWithPending evaluates show and season watched state when a
// viewer has unflushed watch writes touching those shows (or when WatchedTitles
// is not wired).
func (s *Service) containersWatchedWithPending(ctx context.Context, v *core.User, containers []core.Title, pendingKeys []core.RatingKey, out map[core.RatingKey]bool) error {
	if s.d.State == nil {
		return core.ErrNotAvailable
	}
	if s.d.AiredEpisodeTallies == nil {
		keys := make([]core.RatingKey, len(containers))
		for i, t := range containers {
			keys[i] = t.Key
		}
		states, err := s.d.State.GetMany(ctx, v.ID, keys)
		if err != nil {
			return err
		}
		for _, k := range keys {
			if states[k].ViewCount > 0 {
				out[k] = true
			}
		}
		return nil
	}
	showSeen := make(map[core.RatingKey]bool, len(containers))
	shows := make([]core.RatingKey, 0, len(containers))
	for _, t := range containers {
		sk := t.Key
		if t.Key.Kind() == core.KindSeason {
			sk = core.ShowKey(t.Key.TMDB())
		}
		if !showSeen[sk] {
			showSeen[sk] = true
			shows = append(shows, sk)
		}
	}
	tallies, err := s.d.AiredEpisodeTallies(ctx, v.ID, shows, time.Time{}, pendingKeys)
	if err != nil {
		return err
	}
	var judgeKeys []core.RatingKey
	for _, sk := range shows {
		judgeKeys = append(judgeKeys, tallies[sk].Judge...)
	}
	var judgeState map[core.RatingKey]core.WatchStatus
	if len(judgeKeys) > 0 {
		judgeState, err = s.d.State.GetMany(ctx, v.ID, judgeKeys)
		if err != nil {
			return err
		}
	}
	var fallbackKeys []core.RatingKey
	for _, t := range containers {
		switch t.Key.Kind() {
		case core.KindShow:
			tally := tallies[t.Key]
			aired, played := 0, 0
			for season, c := range tally.Seasons {
				if season > 0 {
					aired += c.Aired
					played += c.Played
				}
			}
			for _, ek := range tally.Judge {
				if ek.Season() > 0 {
					aired++
					if judgeState[ek].ViewCount > 0 {
						played++
					}
				}
			}
			if aired == 0 {
				fallbackKeys = append(fallbackKeys, t.Key)
			} else if played == aired && (t.LeafCount <= 0 || aired >= t.LeafCount) {
				out[t.Key] = true
			}
		case core.KindSeason:
			sk := core.ShowKey(t.Key.TMDB())
			tally := tallies[sk]
			c := tally.Seasons[t.Key.Season()]
			aired, played := c.Aired, c.Played
			for _, ek := range tally.Judge {
				if ek.Season() == t.Key.Season() {
					aired++
					if judgeState[ek].ViewCount > 0 {
						played++
					}
				}
			}
			if aired == 0 {
				fallbackKeys = append(fallbackKeys, t.Key)
			} else if played == aired {
				out[t.Key] = true
			}
		}
	}
	if len(fallbackKeys) > 0 {
		states, err := s.d.State.GetMany(ctx, v.ID, fallbackKeys)
		if err != nil {
			return err
		}
		for _, k := range fallbackKeys {
			if states[k].ViewCount > 0 {
				out[k] = true
			}
		}
	}
	return nil
}
