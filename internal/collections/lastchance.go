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
	if user == nil || key == 0 {
		return time.Time{}, nil
	}
	now := s.now()
	kind := key.Kind()
	memoKey := fmt.Sprintf("collections-lastchance:%p:%d:%t:%s:%v:%d:%d",
		s, user.ID, user.IsOwner, user.MaxContentRating,
		user.AllowedLibraries, int(kind), now.Truncate(time.Minute).Unix())
	if reqmemo.Active(ctx) {
		m, err := reqmemo.Do(ctx, memoKey, func() (map[core.RatingKey]time.Time, error) {
			return s.computeLastChanceMap(ctx, user, kind, 0, now)
		})
		if err != nil {
			return time.Time{}, err
		}
		return m[key], nil
	}
	m, err := s.computeLastChanceMap(ctx, user, kind, key, now)
	if err != nil {
		return time.Time{}, err
	}
	return m[key], nil
}

func (s *Service) computeLastChanceMap(ctx context.Context, user *core.User, wantKind core.KeyKind, singleKey core.RatingKey, now time.Time) (map[core.RatingKey]time.Time, error) {
	snap := s.idx.current()
	if snap == nil {
		return nil, nil
	}
	rows, err := s.loadRows(ctx)
	if err != nil {
		return nil, err
	}

	type candidate struct {
		row *specRow
		ms  *memberSet
	}
	var cands []candidate
	var missingRows []*specRow
	var missingIDs []int64
	for _, row := range rows {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		sp := row.spec
		if !visible(row, user) || sp.Rotate == nil || sp.Rotate.Size <= 0 || (!sp.Targets.CollectionsTab && !sp.Targets.HomeRow && !sp.Targets.SectionRow) || !sp.Targets.Schedule.Allows("", now) {
			continue
		}
		if (wantKind == core.KindMovie && sp.MediaType == MediaShow) || (wantKind == core.KindShow && sp.MediaType == MediaMovie) {
			continue
		}
		ms := s.cached(row.id)
		if ms != nil && row.buildHash != "" && ms.hash != row.buildHash {
			ms = nil
		}
		if ms == nil {
			if row.lastBuilt.IsZero() || row.buildHash == "" {
				continue
			}
			missingRows = append(missingRows, row)
			missingIDs = append(missingIDs, row.id)
			continue
		}
		cands = append(cands, candidate{row: row, ms: ms})
	}

	if len(missingIDs) > 0 {
		keysByID, unsharedByID, err := s.loadMembersMany(ctx, missingIDs)
		if err != nil {
			return nil, err
		}
		loaded := make([]*memberSet, len(missingRows))
		for i, row := range missingRows {
			ms := s.newMemberSet(row.spec, keysByID[row.id], unsharedByID[row.id], row.lastBuilt, row.buildHash)
			// Preserve lastBuilt as loadedAt so RefreshLive collections older
			// than liveTTL still refresh on their next membersFor call.
			ms.loadedAt = row.lastBuilt
			loaded[i] = ms
			cands = append(cands, candidate{row: row, ms: ms})
		}
		s.mu.Lock()
		for i, row := range missingRows {
			if s.members[row.id] == nil {
				s.members[row.id] = loaded[i]
			}
		}
		s.mu.Unlock()
	}

	if singleKey != 0 {
		filtered := cands[:0]
		for _, c := range cands {
			if c.ms.has(singleKey) {
				filtered = append(filtered, c)
			}
		}
		cands = filtered
	}
	if len(cands) == 0 {
		return nil, nil
	}

	var statuses map[core.RatingKey]core.WatchStatus
	statusesLoaded := false
	if st := s.stateDep(); st != nil {
		var personalKeys []core.RatingKey
		var seen map[core.RatingKey]struct{}
		for _, c := range cands {
			pr := c.ms.personalRules()
			if len(pr.include)+len(pr.exclude) == 0 {
				continue
			}
			if seen == nil {
				seen = make(map[core.RatingKey]struct{}, len(c.ms.keys))
			}
			for _, k := range c.ms.keys {
				if _, ok := seen[k]; ok {
					continue
				}
				seen[k] = struct{}{}
				personalKeys = append(personalKeys, k)
			}
		}
		if len(personalKeys) > 0 {
			if m, err := st.GetMany(ctx, user.ID, personalKeys); err == nil {
				statuses = m
				statusesLoaded = true
			} else {
				s.log.Debug("collections: watch state", "err", err)
			}
		}
	}

	expiries := make(map[core.RatingKey]time.Time)
	for _, c := range cands {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		row, ms := c.row, c.ms
		sp := row.spec
		// Retain serving's exact filtering/order/limit before comparing rotations.
		keys := s.filterForUserWithStatuses(ctx, ms.keys, ms.personalRules(), user, snap, statuses, statusesLoaded)
		if sp.Sort == "personal" {
			keys = s.personalOrder(ctx, row.id, ms.hash, user, keys)
		}
		if sp.Limit > 0 && len(keys) > sp.Limit {
			keys = keys[:sp.Limit]
		}
		if len(keys) <= sp.Rotate.Size {
			continue
		}
		current := rotate(keys, sp.Rotate.Size, sp.Rotate.Every, row.publicID(), now)
		boundary := nextRotationBoundary(sp.Rotate.Every, now)
		next := rotate(keys, sp.Rotate.Size, sp.Rotate.Every, row.publicID(), boundary)
		nextSet := make(map[core.RatingKey]struct{}, len(next))
		for _, k := range next {
			nextSet[k] = struct{}{}
		}
		for _, k := range current {
			if singleKey != 0 && k != singleKey {
				continue
			}
			if _, stays := nextSet[k]; stays {
				continue
			}
			if prev, ok := expiries[k]; !ok || boundary.Before(prev) {
				expiries[k] = boundary
			}
		}
	}
	return expiries, nil
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
