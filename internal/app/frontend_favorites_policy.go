package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/favorites"
)

// favoriteAllowed binds current catalogue, collection, and channel dependencies.
// Active-viewer validation stays with the caller. The decision itself is
// experience.FavoriteAllowed.
func (a *App) favoriteAllowed(ctx context.Context, u *core.User, subject favorites.Subject, visibility experience.Visibility) bool {
	if key, ok := pluginTitleSubject(subject); ok {
		return a.pluginFavoriteAllowed(ctx, u, key)
	}
	var sources experience.FavoriteSources
	if a.catalog != nil {
		sources.Title = a.catalog.Get
		sources.PersonPage = a.favoritePersonPage
	}
	if a.collections != nil {
		sources.Collection = a.collections.Get
	}
	if a.livetv != nil && subject.Kind == favorites.KindChannel {
		sources.ChannelAllowed = func(ctx context.Context, viewer *core.User, id string) (bool, error) {
			return a.livetv.ChannelPermitted(ctx, viewer, id)
		}
	}
	if subject.Kind == favorites.KindEntity {
		sources.EntityAllowed = a.favoriteEntityAllowed
	}
	return experience.FavoriteAllowed(ctx, u, experience.FavoriteSubject{Kind: experience.FavoriteKind(subject.Kind), ID: subject.ID}, visibility, sources)
}

// favoriteAllowedStored is the row-receipt admission check. It reads the
// catalogue mirror and does not refresh a season or show from a provider,
// so issuing home receipts cannot wait on the network.
func (a *App) favoriteAllowedStored(ctx context.Context, u *core.User, subject favorites.Subject, visibility experience.Visibility) bool {
	if key, ok := pluginTitleSubject(subject); ok {
		return a.pluginFavoriteAllowed(ctx, u, key)
	}
	var sources experience.FavoriteSources
	if a.catalog != nil {
		sources.Title = a.catalog.CachedGet
		sources.PersonPage = a.favoritePersonPage
		visibility.GetTitle = a.catalog.CachedGet
	}
	if a.collections != nil {
		sources.Collection = a.collections.Get
	}
	if a.livetv != nil && subject.Kind == favorites.KindChannel {
		sources.ChannelAllowed = func(ctx context.Context, viewer *core.User, id string) (bool, error) {
			return a.livetv.ChannelPermitted(ctx, viewer, id)
		}
	}
	if subject.Kind == favorites.KindEntity {
		sources.EntityAllowed = a.favoriteEntityAllowed
	}
	return experience.FavoriteAllowed(ctx, u, experience.FavoriteSubject{Kind: experience.FavoriteKind(subject.Kind), ID: subject.ID}, visibility, sources)
}

func (a *App) favoriteEntityAllowed(ctx context.Context, viewer *core.User, rawKey string) (bool, error) {
	if a == nil || viewer == nil || a.pluginsRT == nil {
		return false, nil
	}
	if a.pluginsRT.mgr == nil {
		return false, nil
	}
	key, ok := favorites.ParseEntityKey(rawKey)
	if !ok {
		return false, nil
	}
	return a.screenPluginViewerAllowed(ctx, a.pluginsRT.mgr, viewer, key.PluginID), nil
}

func (a *App) favoritePersonPage(ctx context.Context, person int64, offset, limit int) ([]core.Title, error) {
	page, err := a.catalog.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Person: person}, Offset: offset, Limit: limit})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// favoriteTitlesCached rechecks current policy through the local catalogue
// cache only. It never binds Get or Browse, so this path cannot refresh metadata.
func (a *App) favoriteTitlesCached(ctx context.Context, u *core.User, keys []core.RatingKey, visibility experience.Visibility) (bool, error) {
	if u == nil {
		return false, nil
	}
	catalogue := make([]core.RatingKey, 0, len(keys))
	for _, key := range keys {
		if !key.IsPluginKey() {
			catalogue = append(catalogue, key)
		} else if !a.pluginFavoriteAllowed(ctx, u, key) {
			return false, nil
		}
	}
	if len(catalogue) == 0 && len(keys) > 0 {
		return true, nil
	}
	if a.catalog == nil {
		return false, nil
	}
	return experience.FavoriteTitlesCached(ctx, u, catalogue, visibility, a.catalog.CachedGetMany)
}

// pluginTitleSubject is the plugin item a title favorite names, if it names one.
func pluginTitleSubject(subject favorites.Subject) (core.RatingKey, bool) {
	if subject.Kind != favorites.KindTitle {
		return 0, false
	}
	key, err := core.ParseRatingKey(subject.ID)
	return key, err == nil && key.IsPluginKey()
}

// pluginFavoriteAllowed admits a plugin library's item the viewer may see. It
// reads the stored plugin catalogue only, so it never waits on the plugin.
func (a *App) pluginFavoriteAllowed(ctx context.Context, u *core.User, key core.RatingKey) bool {
	_, _, err := a.sharedPluginArtworkItem(ctx, u, core.Device{}, key)
	return err == nil
}

// favoriteTitleRole names what a liked title is, for role lists (Liked Songs,
// followed artists): a plugin item's level role from the stored plugin
// catalogue, a catalogue title's type from its key. Never a provider call.
func (a *App) favoriteTitleRole(ctx context.Context, key core.RatingKey) string {
	if !key.IsPluginKey() {
		switch key.Kind() {
		case core.KindMovie:
			return string(core.TypeMovie)
		case core.KindShow:
			return string(core.TypeShow)
		case core.KindSeason:
			return string(core.TypeSeason)
		case core.KindEpisode:
			return string(core.TypeEpisode)
		}
		return ""
	}
	if a == nil || a.pluginsRT == nil || a.pluginsRT.kindCatalog == nil {
		return ""
	}
	piv, err := a.pluginsRT.kindCatalog.Item(ctx, key)
	if err != nil {
		return ""
	}
	if piv.Title.MediaRole != "" {
		return string(piv.Title.MediaRole)
	}
	return string(piv.Title.Type)
}
