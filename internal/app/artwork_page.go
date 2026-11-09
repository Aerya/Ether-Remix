package app

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/overlays"
)

type episodeBadgePageKey struct{}
type episodeBadgePage struct {
	viewer int64
	facts  map[core.RatingKey]overlays.Facts
}

// A request-local snapshot: another viewer or page never inherits watch state.
func (a *App) prepareArtworkPage(ctx context.Context, user *core.User, titles []core.Title) context.Context {
	if user == nil || a.overlays == nil {
		return ctx
	}
	cfg, err := a.overlays.Settings(ctx)
	if err != nil || !cfg.Enabled || !cfg.Status {
		return ctx
	}
	shows := []core.RatingKey{}
	for _, t := range titles {
		if t.Type == core.TypeShow {
			shows = append(shows, t.Key)
		}
	}
	if len(shows) == 0 {
		return ctx
	}
	episodes, err := a.catalog.RecentEpisodesMany(ctx, shows, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		return ctx
	}
	keys := make([]core.RatingKey, 0, len(episodes))
	for _, ep := range episodes {
		keys = append(keys, ep.Key)
	}
	watched, err := a.state.GetMany(ctx, user.ID, keys)
	if err != nil {
		return ctx
	}
	page := episodeBadgePage{viewer: user.ID, facts: make(map[core.RatingKey]overlays.Facts)}
	for _, k := range shows {
		page.facts[k] = overlays.Facts{}
	}
	for _, ep := range episodes {
		if watched[ep.Key].ViewCount == 0 {
			f := page.facts[ep.ShowKey]
			f.NewEpisode = true
			f.NewSeason = f.NewSeason || ep.Episode == 1
			page.facts[ep.ShowKey] = f
		}
	}
	return context.WithValue(ctx, episodeBadgePageKey{}, page)
}

func (p episodeBadgePage) covers(key core.RatingKey) bool { _, ok := p.facts[key]; return ok }
