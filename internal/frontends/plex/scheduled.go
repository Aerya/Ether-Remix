package plex

import (
	"context"
	"sort"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

func (s *Server) scheduledEpisodes(ctx context.Context, pr *Request, season frontend.RatingKey) []frontend.Title {
	if !s.cfg().Catalog.ShowUpcomingEpisodes || season.Kind() != frontend.KindSeason {
		return nil
	}
	schedule, ok := s.catalog().(frontend.CatalogSchedule)
	if !ok {
		return nil
	}
	parent, err := s.catalog().Get(ctx, season)
	if err != nil || parent == nil || parent.Season <= 0 || !s.visibleTitle(ctx, pr.User, parent) || s.viewerGroupHidesSchedule(ctx, pr, parent) {
		return nil
	}
	episodes, err := schedule.ScheduledEpisodes(ctx, season)
	if err != nil {
		return nil
	}
	out := make([]frontend.Title, 0, len(episodes))
	for _, ep := range episodes {
		if ep.Type == frontend.TypeEpisode && titleParent(&ep) == season && ep.Released.After(s.now()) && s.playableTitle(ctx, pr.User, &ep) {
			out = append(out, ep)
		}
	}
	return out
}

func (s *Server) viewerGroupHidesSchedule(ctx context.Context, pr *Request, t *frontend.Title) bool {
	if s.episodeOrder() == nil || !viewerEpisodeOrder(t) {
		return false
	}
	show := titleShow(t)
	grouped, err := s.episodeOrder().Grouped(ctx, pr.User, []frontend.RatingKey{show})
	return err != nil || grouped[show]
}

func (s *Server) scheduledEpisode(ctx context.Context, pr *Request, key frontend.RatingKey) (*frontend.Title, error) {
	if !s.cfg().Catalog.ShowUpcomingEpisodes || key.Kind() != frontend.KindEpisode {
		return nil, frontend.ErrNotFound
	}
	schedule, ok := s.catalog().(frontend.CatalogSchedule)
	if !ok {
		return nil, frontend.ErrNotFound
	}
	episodes, err := schedule.ScheduledEpisodes(ctx, key)
	if err != nil {
		return nil, err
	}
	for _, ep := range episodes {
		if ep.Key != key || ep.Type != frontend.TypeEpisode || ep.Season <= 0 || !ep.Released.After(s.now()) ||
			s.viewerGroupHidesSchedule(ctx, pr, &ep) || !s.playableTitle(ctx, pr.User, &ep) {
			continue
		}
		parent, err := s.catalog().Get(ctx, titleParent(&ep))
		if err == nil && parent != nil && parent.Type == frontend.TypeSeason && parent.Season == ep.Season &&
			titleShow(parent) == titleShow(&ep) && s.visibleTitle(ctx, pr.User, parent) {
			return &ep, nil
		}
	}
	return nil, frontend.ErrNotFound
}

func (s *Server) scheduledMetadata(ctx context.Context, pr *Request, t *frontend.Title) model.Metadata {
	md := s.metadata(ctx, pr, t, renderOpts{})
	md.Media = nil
	md.Title += " (Unaired)"
	md.Summary = "Scheduled air date: " + t.Released.Format("2006-01-02") + ". Not available to play yet. " + md.Summary
	// Base artwork uses the title-scoped handler, which repeats the schedule and
	// viewer checks. Do not invoke overlays or a custom provider for placeholders.
	md.Thumb = s.thumbURL(t)
	md.Art = s.artURL(t)
	md.Image = nil
	return md
}

func appendScheduled(aired, future []frontend.Title) []frontend.Title {
	if len(future) == 0 {
		return aired
	}
	out := append(append([]frontend.Title(nil), aired...), future...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Episode < out[j].Episode })
	return out
}
