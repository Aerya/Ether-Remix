package experience

import (
	"context"
	"regexp"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/state"
)

type playContextStore interface {
	RecordPlayContext(ctx context.Context, userID int64, kind, ref string) error
	RecentPlayContexts(ctx context.Context, userID int64, limit int) ([]state.PlayContext, error)
}

var likedRole = regexp.MustCompile(`^[a-z][a-zA-Z]{0,31}$`)

var _ frontend.PlayContexts = (*Service)(nil)

func (s *Service) playContextStore() (playContextStore, bool) {
	store, ok := s.d.State.(playContextStore)
	return store, ok
}

// RecordPlayContext remembers that the viewer started playing from ref. The
// viewer must be able to see the title or own the playlist.
func (s *Service) RecordPlayContext(ctx context.Context, viewer *core.Viewer, ref frontend.PlayContextRef) error {
	ctx = core.WithCatalogViewer(ctx, viewer)
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return err
	}
	store, ok := s.playContextStore()
	if !ok {
		return core.ErrNotAvailable
	}
	switch ref.Kind {
	case "title":
		if ref.Key == 0 {
			return core.ErrNotFound
		}
		if ref.Key.IsPluginKey() {
			if _, err := s.pluginItem(core.WithNoContainerExpand(ctx), v, ref.Key); err != nil {
				return err
			}
		} else if t, err := s.item(ctx, v, ref.Key); err != nil || t == nil {
			return core.ErrNotFound
		}
		return store.RecordPlayContext(ctx, v.ID, "title", ref.Key.String())
	case "playlist":
		if _, err := s.ownedPlaylist(ctx, v, ref.Key); err != nil {
			return err
		}
		return store.RecordPlayContext(ctx, v.ID, "playlist", ref.Key.String())
	case "favorites":
		if !likedRole.MatchString(ref.Role) {
			return frontend.ErrBadPlaylist
		}
		return store.RecordPlayContext(ctx, v.ID, "favorites", ref.Role)
	}
	return core.ErrNotFound
}

// RecentPlayContexts resolves the viewer's newest play contexts, dropping any
// they can no longer see. Bounded by state.MaxPlayContexts.
func (s *Service) RecentPlayContexts(ctx context.Context, viewer *core.Viewer, limit int) ([]frontend.RecentPlayContext, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return nil, err
	}
	store, ok := s.playContextStore()
	if !ok {
		return nil, core.ErrNotAvailable
	}
	recent, err := store.RecentPlayContexts(ctx, v.ID, limit)
	if err != nil {
		return nil, err
	}
	var titleKeys []core.RatingKey
	for _, c := range recent {
		if c.Kind == "title" {
			if k, err := core.ParseRatingKey(c.Ref); err == nil {
				titleKeys = append(titleKeys, k)
			}
		}
	}
	titles, err := s.loadVisiblePlaylistTitles(ctx, v, titleKeys)
	if err != nil {
		return nil, err
	}
	var playlistKeys []core.RatingKey
	for _, c := range recent {
		if c.Kind == "playlist" {
			if k, err := core.ParseRatingKey(c.Ref); err == nil && k.Kind() == core.KindPlaylist {
				playlistKeys = append(playlistKeys, k)
			}
		}
	}
	playlists := map[core.RatingKey]core.Playlist{}
	if len(playlistKeys) > 0 && s.d.Playlists != nil {
		if batch, ok := s.d.Playlists.(playlistHeadersBatchSource); ok {
			if headers, err := batch.GetHeaders(ctx, v.ID, playlistKeys); err == nil {
				playlists = headers
			}
		} else {
			for _, k := range playlistKeys {
				if p, err := s.d.Playlists.Get(ctx, v.ID, k); err == nil && p != nil {
					playlists[k] = *p
				}
			}
		}
	}
	out := make([]frontend.RecentPlayContext, 0, len(recent))
	for _, c := range recent {
		switch c.Kind {
		case "title":
			k, err := core.ParseRatingKey(c.Ref)
			if t, ok := titles[k]; err == nil && ok {
				out = append(out, frontend.RecentPlayContext{Kind: "title", Title: &t, PlayedAt: c.PlayedAt})
			}
		case "playlist":
			k, err := core.ParseRatingKey(c.Ref)
			if header, ok := playlists[k]; err == nil && ok && header.UserID == v.ID {
				out = append(out, frontend.RecentPlayContext{Kind: "playlist", Playlist: &header, PlayedAt: c.PlayedAt})
			}
		case "favorites":
			out = append(out, frontend.RecentPlayContext{Kind: "favorites", Role: c.Ref, PlayedAt: c.PlayedAt})
		}
	}
	return out, nil
}
