package experience

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// ResolveExternal resolves a provider ID to a visible catalogue item.
// Series suffixes address coordinates in the catalogue's effective order.
func (s *Service) ResolveExternal(ctx context.Context, viewer *core.Viewer, _ core.Device, req frontend.ExternalRequest) (core.RatingKey, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	ctx = core.WithCatalogViewer(ctx, viewer)
	var tmdbShow core.RatingKey
	switch req.Source {
	case "imdb":
		digits, ok := strings.CutPrefix(req.ID, "tt")
		if !ok || digits == "" || len(digits) > 16 {
			return 0, core.ErrNotFound
		}
		for _, ch := range digits {
			if ch < '0' || ch > '9' {
				return 0, core.ErrNotFound
			}
		}
	case "tmdb":
		id, err := strconv.ParseInt(req.ID, 10, 64)
		if err != nil || id <= 0 || id >= 100_000_000 || strconv.FormatInt(id, 10) != req.ID || req.Type != core.TypeShow || req.Season == nil {
			return 0, core.ErrNotFound
		}
		tmdbShow = core.ShowKey(id)
	default:
		return 0, core.ErrNotFound
	}
	if (req.Season == nil) != (req.Episode == nil) {
		return 0, core.ErrNotFound
	}
	if req.Type != "" && req.Type != core.TypeMovie && req.Type != core.TypeShow && req.Type != core.TypeEpisode {
		return 0, core.ErrNotFound
	}
	if req.Season != nil && (req.Type != core.TypeShow || *req.Season < 0 || *req.Season > 9999 || *req.Episode < 0 || *req.Episode > 9999) {
		return 0, core.ErrNotFound
	}
	active, err := s.viewer(ctx, viewer)
	if err != nil {
		return 0, core.ErrNotFound
	}
	var t *core.Title
	if tmdbShow != 0 {
		if s.d.KeysByProviderID != nil {
			keys, lookupErr := s.d.KeysByProviderID(ctx, "tmdb", req.ID, []core.MediaType{core.TypeShow})
			if lookupErr != nil {
				return 0, lookupErr
			}
			if len(keys) > 1 {
				return 0, core.ErrNotFound
			}
			if len(keys) == 1 {
				tmdbShow = keys[0]
			}
		}
		t, err = s.item(ctx, active, tmdbShow)
	} else {
		if s.d.ByExternalID == nil {
			return 0, core.ErrNotAvailable
		}
		t, err = s.d.ByExternalID(ctx, "imdb", req.ID)
	}
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			return 0, core.ErrNotFound
		}
		return 0, err
	}
	if t == nil || !t.Key.Valid() {
		return 0, core.ErrNotFound
	}
	switch t.Type {
	case core.TypeMovie:
		if t.Key.Kind() != core.KindMovie {
			return 0, core.ErrNotFound
		}
	case core.TypeShow:
		if t.Key.Kind() != core.KindShow {
			return 0, core.ErrNotFound
		}
	case core.TypeEpisode:
		if t.Key.Kind() != core.KindEpisode {
			return 0, core.ErrNotFound
		}
	default:
		return 0, core.ErrNotFound
	}
	if req.Type != "" && t.Type != req.Type {
		return 0, core.ErrNotFound
	}
	key := t.Key
	if req.Season != nil {
		if t.Type != core.TypeShow || s.d.ResolveEpisode == nil || !s.visible(ctx, active, t) {
			return 0, core.ErrNotFound
		}
		key, err = s.d.ResolveEpisode(ctx, t.Key, *req.Season, *req.Episode)
		if err != nil {
			return 0, err
		}
		if key.Kind() != core.KindEpisode {
			return 0, core.ErrNotFound
		}
	}
	result, err := s.item(ctx, active, key)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, core.ErrNotFound
	}
	if req.Season != nil && (result.Type != core.TypeEpisode || result.ShowKey != t.Key || result.Season != *req.Season || result.Episode != *req.Episode) {
		return 0, core.ErrNotFound
	}
	if req.Season == nil && result.Type != t.Type {
		return 0, core.ErrNotFound
	}
	return key, nil
}
