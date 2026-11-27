package state

import (
	"context"
	"math"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// RateExact preserves the distinction between clearing a rating and writing
// another negative number. Equal values do not refresh the user's timestamps.
func (s *State) RateExact(ctx context.Context, user int64, key core.RatingKey, rating float64) error {
	return s.update(ctx, user, []core.RatingKey{key}, func(cur *row, now time.Time) (bool, bool, []Event) {
		value := math.Max(0, math.Min(10, rating))
		if (rating >= 0 || rating == -1) && cur.ws.UserRating == value {
			return false, false, nil
		}
		cur.ws.UserRating = value
		cur.ws.UpdatedAt = now
		cur.ratingAt = now
		if rating == -1 {
			cur.ratingAt = time.Time{}
		}
		cur.ws.LastRatedAt = cur.ratingAt
		return false, false, []Event{{Kind: EventRated, UserID: user, Key: key, Rating: value, At: now}}
	})
}

// ScrobbleTree keeps parent counters independent of the watched-leaf totals.
// Descendant expansion is supplied by the catalogue, never a state-table scan.
func (s *State) ScrobbleTree(ctx context.Context, user int64, key core.RatingKey, leaves []core.RatingKey, watched bool) error {
	if watched && (key.Kind() == core.KindCollection || key.Kind() == core.KindSmart) {
		return nil
	}
	keys := append([]core.RatingKey{}, leaves...)
	parentOnly := map[core.RatingKey]bool{}
	seen := map[core.RatingKey]bool{}
	for _, k := range keys {
		seen[k] = true
	}
	add := func(k core.RatingKey) {
		if k != 0 && !seen[k] {
			keys = append(keys, k)
			seen[k] = true
			parentOnly[k] = true
		}
	}
	switch key.Kind() {
	case core.KindEpisode:
		if watched {
			ancestors, err := s.resolveAncestry(ctx, []core.RatingKey{key})
			if err != nil {
				return err
			}
			add(ancestors[key].ParentKey)
			add(ancestors[key].ShowKey)
		}
	case core.KindShow, core.KindSeason:
		add(key)
		if !watched && key.Kind() == core.KindShow {
			ancestors, err := s.resolveAncestry(ctx, leaves)
			if err != nil {
				return err
			}
			for _, leaf := range leaves {
				add(ancestors[leaf].ParentKey)
			}
		}
	}
	return s.update(ctx, user, keys, func(cur *row, now time.Time) (bool, bool, []Event) {
		k := cur.ws.Key
		if parentOnly[k] && watched && key.Kind() != core.KindEpisode {
			cur.ws.LastViewedAt = now
			cur.ws.UpdatedAt = now
			cur.watchAt = now
			return false, false, nil
		}
		cur.ws.ViewOffset = 0
		cur.ws.UpdatedAt = now
		cur.watchAt = now
		cur.counted = watched
		if watched {
			cur.ws.ViewCount++
			cur.ws.LastViewedAt = now
		} else {
			cur.ws.ViewCount = 0
			cur.ws.LastViewedAt = time.Time{}
		}
		kind := EventScrobble
		if !watched {
			kind = EventUnscrobble
		}
		// Later PMS measurements distinguish manual TV scrobbles from movie history.
		history := watched && !parentOnly[k] && k.Kind() == core.KindMovie
		return history, watched, []Event{{Kind: kind, UserID: user, Key: k, Completed: watched, At: now}}
	})
}

// TouchParents applies an episode playback's timestamps and independent
// completion counters to its two ancestors without manufacturing history.
func (s *State) TouchParents(ctx context.Context, user int64, key core.RatingKey, at time.Time, completed bool) error {
	if key.Kind() != core.KindEpisode {
		return nil
	}
	ancestors, err := s.resolveAncestry(ctx, []core.RatingKey{key})
	if err != nil {
		return err
	}
	episode := ancestors[key]
	parents := make([]core.RatingKey, 0, 2)
	if episode.ParentKey.Kind() == core.KindSeason {
		parents = append(parents, episode.ParentKey)
	}
	if episode.ShowKey.Kind() == core.KindShow {
		parents = append(parents, episode.ShowKey)
	}
	if len(parents) == 0 {
		return nil
	}
	return s.updateAt(ctx, user, parents, at, func(cur *row, now time.Time) (bool, bool, []Event) {
		cur.ws.LastViewedAt = now
		cur.ws.UpdatedAt = now
		cur.watchAt = now
		if completed {
			cur.ws.ViewCount++
		}
		return false, false, nil
	})
}

// resolveAncestry preserves the encoded hierarchy for callers that construct
// State without a catalogue, and never infers parents of allocated keys.
func (s *State) resolveAncestry(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]Ancestry, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	if s.ancestry != nil {
		return s.ancestry(ctx, keys)
	}
	out := make(map[core.RatingKey]Ancestry, len(keys))
	for _, key := range keys {
		if parent := key.Parent(); parent != 0 {
			switch key.Kind() {
			case core.KindEpisode:
				out[key] = Ancestry{ShowKey: parent.Parent(), ParentKey: parent}
			case core.KindSeason:
				out[key] = Ancestry{ShowKey: parent, ParentKey: parent}
			}
		}
	}
	return out, nil
}
