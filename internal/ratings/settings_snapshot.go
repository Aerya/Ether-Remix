package ratings

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

type settingsSnapshotKey struct{ service *Service }
type settingsSnapshot struct {
	value Settings
	err   error
}

type pageSnapshotKey struct{ service *Service }
type pageSnapshot struct {
	provider string
	checked  map[core.RatingKey]bool
	badges   map[core.RatingKey][]Badge
}

// WithSettingsSnapshot reads settings once for a bounded render. It avoids a
// database round trip per tile without keeping settings stale across requests.
func (s *Service) WithSettingsSnapshot(ctx context.Context) context.Context {
	if _, ok := ctx.Value(settingsSnapshotKey{s}).(settingsSnapshot); ok {
		return ctx
	}
	value, err := s.settings(ctx)
	return context.WithValue(ctx, settingsSnapshotKey{s}, settingsSnapshot{value, err})
}

// WithPage snapshots settings and preloads cached badges for titles so a list
// or poster grid performs at most one ratings_cache query for the page.
func (s *Service) WithPage(ctx context.Context, titles []core.Title) context.Context {
	if s == nil {
		return ctx
	}
	ctx = s.WithSettingsSnapshot(ctx)
	cfg, err := s.settings(ctx)
	if err != nil || !cfg.Enabled || len(titles) == 0 {
		return ctx
	}
	provider := cacheProvider(cfg)
	prev, hasPrev := ctx.Value(pageSnapshotKey{s}).(pageSnapshot)
	if hasPrev && prev.provider != provider {
		hasPrev = false
	}
	var missing []core.Title
	seen := make(map[core.RatingKey]bool, len(titles))
	for i := range titles {
		k := titles[i].Key
		if k == 0 || seen[k] || (hasPrev && prev.checked[k]) {
			continue
		}
		seen[k] = true
		missing = append(missing, titles[i])
	}
	if len(missing) == 0 {
		return ctx
	}
	batch, err := s.CachedMany(ctx, missing)
	if err != nil {
		return ctx
	}
	checkedCap := len(missing)
	badgesCap := len(batch)
	if hasPrev {
		checkedCap += len(prev.checked)
		badgesCap += len(prev.badges)
	}
	checked := make(map[core.RatingKey]bool, checkedCap)
	badges := make(map[core.RatingKey][]Badge, badgesCap)
	if hasPrev {
		for k, v := range prev.checked {
			checked[k] = v
		}
		for k, v := range prev.badges {
			badges[k] = v
		}
	}
	for i := range missing {
		checked[missing[i].Key] = true
	}
	for k, v := range batch {
		badges[k] = v
	}
	return context.WithValue(ctx, pageSnapshotKey{s}, pageSnapshot{
		provider: provider,
		checked:  checked,
		badges:   badges,
	})
}
