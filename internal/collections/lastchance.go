package collections

import (
	"context"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/reqmemo"
)

// LastChance reports the next configured rotation boundary only when a title
// belongs to this viewer's current subset and is absent from the next subset.
// It means leaving a rotating collection row, never disappearing from Ether.
// The method uses already-built members and the current local index. It never
// triggers a build, fetches a source, or invents a media-availability deadline.
func (s *Service) LastChance(ctx context.Context, user *core.User, key core.RatingKey) (time.Time, error) {
	if user == nil {
		return time.Time{}, nil
	}
	deadlines, err := reqmemo.Do(ctx, fmt.Sprintf("collections:lastchance:%p:%d", s, user.ID), func() (map[core.RatingKey]time.Time, error) {
		return s.lastChanceDeadlines(ctx, user)
	})
	return deadlines[key], err
}

// Compute each rotation once per viewer/request, including negative results.
func (s *Service) lastChanceDeadlines(ctx context.Context, user *core.User) (map[core.RatingKey]time.Time, error) {
	deadlines := make(map[core.RatingKey]time.Time)
	snap := s.idx.current()
	if snap == nil {
		return deadlines, nil
	}
	rows, err := s.loadRows(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	for _, row := range rows {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sp := row.spec
		if !visible(row, user) || sp.Rotate == nil || sp.Rotate.Size <= 0 || (!sp.Targets.CollectionsTab && !sp.Targets.HomeRow && !sp.Targets.SectionRow) || !sp.Targets.Schedule.Allows("", now) {
			continue
		}
		ms := s.cached(row.id)
		if ms == nil {
			if row.lastBuilt.IsZero() || row.buildHash == "" {
				continue
			}
			keys, unshared, e := s.loadMembers(ctx, row.id)
			if e != nil {
				return nil, e
			}
			ms = s.newMemberSet(row.spec, keys, unshared, row.lastBuilt, row.buildHash)
		}
		// Retain serving's exact filtering/order/limit before comparing rotations.
		keys := s.filterForUser(ctx, ms.keys, ms.personalRules(), user, snap)
		if sp.Sort == "personal" {
			keys = s.personalOrder(ctx, row.id, ms.hash, user, keys)
		}
		if sp.Limit > 0 && len(keys) > sp.Limit {
			keys = keys[:sp.Limit]
		}
		if len(keys) <= sp.Rotate.Size {
			continue
		}
		boundary := nextRotationBoundary(sp.Rotate.Every, now)
		next := make(map[core.RatingKey]bool)
		for _, k := range rotate(keys, sp.Rotate.Size, sp.Rotate.Every, row.publicID(), boundary) {
			next[k] = true
		}
		for _, k := range rotate(keys, sp.Rotate.Size, sp.Rotate.Every, row.publicID(), now) {
			if !next[k] && (deadlines[k].IsZero() || boundary.Before(deadlines[k])) {
				deadlines[k] = boundary
			}
		}
	}
	return deadlines, nil
}

// Use calendar arithmetic in the collection clock's zone, including DST.
func nextRotationBoundary(every string, now time.Time) time.Time {
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if every != "weekly" {
		return midnight.AddDate(0, 0, 1)
	}
	days := (8 - int(now.Weekday())) % 7
	if days == 0 {
		days = 7
	}
	return midnight.AddDate(0, 0, days)
}
