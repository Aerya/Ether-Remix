package app

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/favorites"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/state"
)

// adapterViewerState implements plugins.KindViewerState on Ether's shared
// per-viewer stores: favorites (likes/stars), watch state (plays, ratings) and
// playlists, all keyed by the plugin item's rating key. A frontend adapter's
// stars, scrobbles and playlists are therefore the same ones Vela, Jellyfin
// and every other frontend show.
type adapterViewerState struct{ app *App }

var _ plugins.KindViewerState = adapterViewerState{}

// maxAdapterFavoriteScan bounds how many of the viewer's favorite titles one
// listing inspects (they span every library, not only this plugin's).
const maxAdapterFavoriteScan = 5000

// viewer resolves the adapter viewer and confirms they may see the kind's
// library. A viewer who cannot see it gets ErrNotFound, never a hint.
func (s adapterViewerState) viewer(ctx context.Context, pluginID string, viewerID int64, kindKey string) (*core.User, core.Library, error) {
	a := s.app
	if a.state == nil || a.favorites == nil || a.playlists == nil {
		return nil, core.Library{}, plugins.ErrUnavailable
	}
	viewer, lib, ok, err := a.adapterKindLibrary(ctx, pluginID, viewerID, kindKey)
	if err != nil {
		return nil, core.Library{}, err
	}
	if !ok {
		return nil, core.Library{}, core.ErrNotFound
	}
	return viewer, lib, nil
}

// keys maps visible external ids of the plugin's kind to their rating keys,
// preserving the requested order and dropping unknown or hidden ids.
func (s adapterViewerState) keys(ctx context.Context, pluginID string, viewerID int64, kindKey string, ids []string) ([]string, []core.RatingKey, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	visible, err := s.app.adapterKindVisible()(ctx, pluginID, viewerID, kindKey, ids)
	if err != nil {
		return nil, nil, err
	}
	if len(visible) == 0 {
		return nil, nil, nil
	}
	items, err := s.app.pluginsRT.pluginitems.KindItemsByID(ctx, pluginID, kindKey, visible)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]core.RatingKey, len(items))
	for _, it := range items {
		if it.Key != 0 {
			byID[it.ID] = it.Key
		}
	}
	outIDs := make([]string, 0, len(ids))
	outKeys := make([]core.RatingKey, 0, len(ids))
	for _, id := range ids {
		if k, ok := byID[id]; ok {
			outIDs = append(outIDs, id)
			outKeys = append(outKeys, k)
		}
	}
	return outIDs, outKeys, nil
}

// owned maps rating keys back to this plugin kind's items, dropping keys of
// other plugins, kinds or libraries.
func (s adapterViewerState) owned(ctx context.Context, pluginID, kindKey string, lib core.LibraryID, keys []core.RatingKey) (map[core.RatingKey]core.KindItem, error) {
	plugin := make([]core.RatingKey, 0, len(keys))
	for _, k := range keys {
		if k.IsPluginItemKey() {
			plugin = append(plugin, k)
		}
	}
	out := make(map[core.RatingKey]core.KindItem, len(plugin))
	if len(plugin) == 0 {
		return out, nil
	}
	items, err := s.app.pluginsRT.pluginitems.GetByKeys(ctx, plugin)
	if err != nil {
		return nil, err
	}
	for k, it := range items {
		if it.LibraryID != lib || it.Item.Ref.Plugin() != pluginID || it.Item.Ref.Kind() != kindKey {
			continue
		}
		out[k] = it.Item
	}
	return out, nil
}

func (s adapterViewerState) originCtx(ctx context.Context, pluginID string) context.Context {
	return state.WithOrigin(ctx, state.Origin{Surface: frontend.Protocol("plugin:" + pluginID)})
}

func (s adapterViewerState) State(ctx context.Context, pluginID string, viewerID int64, kindKey string, ids []string) ([]plugins.KindItemState, error) {
	viewer, _, err := s.viewer(ctx, pluginID, viewerID, kindKey)
	if err != nil {
		return nil, err
	}
	ids, keys, err := s.keys(ctx, pluginID, viewerID, kindKey, ids)
	if err != nil || len(keys) == 0 {
		return []plugins.KindItemState{}, err
	}
	watch, err := s.app.state.GetMany(ctx, viewer.ID, keys)
	if err != nil {
		return nil, err
	}
	favs, err := s.app.favorites.TitleMembership(ctx, viewer.ID, keys)
	if err != nil {
		return nil, err
	}
	out := make([]plugins.KindItemState, 0, len(ids))
	for i, id := range ids {
		ws := watch[keys[i]]
		st := plugins.KindItemState{ID: id, PlayCount: ws.ViewCount, Favorite: favs[keys[i]], Rating: ws.UserRating}
		if !ws.LastViewedAt.IsZero() {
			st.LastPlayedAt = ws.LastViewedAt.UnixMilli()
		}
		out = append(out, st)
	}
	return out, nil
}

func (s adapterViewerState) SetFavorite(ctx context.Context, pluginID string, viewerID int64, kindKey string, ids []string, on bool) error {
	viewer, _, err := s.viewer(ctx, pluginID, viewerID, kindKey)
	if err != nil {
		return err
	}
	_, keys, err := s.keys(ctx, pluginID, viewerID, kindKey, ids)
	if err != nil || len(keys) == 0 {
		return err
	}
	subjects := make([]favorites.Subject, len(keys))
	for i, k := range keys {
		subjects[i] = favorites.Subject{Kind: favorites.KindTitle, ID: k.String()}
	}
	_, err = s.app.favorites.SetMany(ctx, viewer, subjects, on)
	return err
}

func (s adapterViewerState) one(ctx context.Context, pluginID string, viewerID int64, kindKey, id string) (*core.User, core.RatingKey, error) {
	viewer, _, err := s.viewer(ctx, pluginID, viewerID, kindKey)
	if err != nil {
		return nil, 0, err
	}
	_, keys, err := s.keys(ctx, pluginID, viewerID, kindKey, []string{id})
	if err != nil {
		return nil, 0, err
	}
	if len(keys) == 0 {
		return nil, 0, core.ErrNotFound
	}
	return viewer, keys[0], nil
}

func (s adapterViewerState) Played(ctx context.Context, pluginID string, viewerID int64, kindKey, id string) error {
	viewer, key, err := s.one(ctx, pluginID, viewerID, kindKey, id)
	if err != nil {
		return err
	}
	return s.app.state.Scrobble(s.originCtx(ctx, pluginID), viewer.ID, key)
}

func (s adapterViewerState) Rate(ctx context.Context, pluginID string, viewerID int64, kindKey, id string, rating float64) error {
	viewer, key, err := s.one(ctx, pluginID, viewerID, kindKey, id)
	if err != nil {
		return err
	}
	if rating == 0 {
		// Zero clears the rating, as Subsonic setRating(0) and Plex do.
		return s.app.state.RateExact(s.originCtx(ctx, pluginID), viewer.ID, key, -1)
	}
	return s.app.state.Rate(s.originCtx(ctx, pluginID), viewer.ID, key, rating)
}

func (s adapterViewerState) Favorites(ctx context.Context, pluginID string, viewerID int64, kindKey string, q plugins.KindFavoritesQuery) ([]plugins.KindFavorite, error) {
	viewer, lib, err := s.viewer(ctx, pluginID, viewerID, kindKey)
	if err != nil {
		return nil, err
	}
	var entries []favorites.Entry
	if q.Level != "" {
		// Likes are listed by the level's role (track, album, artist), paged
		// on the viewer's own likes, newest first.
		rec, err := s.app.pluginsRT.pluginitems.GetKindByLibrary(ctx, lib.ID)
		if err != nil {
			return nil, err
		}
		lv, _, ok := rec.Decl.Level(q.Level)
		if !ok {
			return []plugins.KindFavorite{}, nil
		}
		role := lv.Role
		if role == "" {
			role = lv.Key
		}
		cursor := ""
		for len(entries) < q.Limit {
			page, err := s.app.favorites.Page(ctx, viewer, favorites.PageQuery{Kind: favorites.KindTitle, Role: role, Cursor: cursor, Limit: min(favorites.MaxPageLimit, q.Limit-len(entries))})
			if err != nil {
				return nil, err
			}
			entries = append(entries, page.Entries...)
			if page.Next == "" || len(page.Entries) == 0 {
				break
			}
			cursor = page.Next
		}
	} else {
		entries, err = s.app.favorites.List(ctx, viewer, favorites.KindTitle, maxAdapterFavoriteScan)
		if err != nil {
			return nil, err
		}
	}
	keys := make([]core.RatingKey, 0, len(entries))
	added := make(map[core.RatingKey]time.Time, len(entries))
	for _, e := range entries {
		k, err := core.ParseRatingKey(e.Subject.ID)
		if err != nil || !k.IsPluginItemKey() {
			continue
		}
		keys = append(keys, k)
		added[k] = e.AddedAt
	}
	items, err := s.owned(ctx, pluginID, kindKey, lib.ID, keys)
	if err != nil {
		return nil, err
	}
	out := make([]plugins.KindFavorite, 0, min(len(items), q.Limit))
	for _, k := range keys {
		it, ok := items[k]
		if !ok || (q.Level != "" && it.Level != q.Level) {
			continue
		}
		out = append(out, plugins.KindFavorite{ID: it.ID, Level: it.Level, AddedAt: added[k].UnixMilli()})
		if len(out) >= q.Limit {
			break
		}
	}
	return out, nil
}

// project turns a playlist into the plugin's view of it.
func (s adapterViewerState) project(pl core.Playlist, items map[core.RatingKey]core.KindItem) plugins.KindPlaylist {
	out := plugins.KindPlaylist{
		ID:        pl.Key.String(),
		Title:     pl.Title,
		Summary:   pl.Summary,
		Smart:     pl.Smart,
		CreatedAt: pl.AddedAt.UnixMilli(),
		UpdatedAt: pl.UpdatedAt.UnixMilli(),
		Total:     len(pl.Items),
	}
	for i, k := range pl.Items {
		if it, ok := items[k]; ok {
			out.Items = append(out.Items, it.ID)
			out.Indexes = append(out.Indexes, i)
			if len(out.Items) >= plugins.MaxKindPlaylistItems {
				break
			}
		}
	}
	return out
}

func (s adapterViewerState) Playlists(ctx context.Context, pluginID string, viewerID int64, kindKey string) ([]plugins.KindPlaylist, error) {
	viewer, lib, err := s.viewer(ctx, pluginID, viewerID, kindKey)
	if err != nil {
		return nil, err
	}
	lists, err := s.app.playlists.List(ctx, viewer.ID)
	if err != nil {
		return nil, err
	}
	var keys []core.RatingKey
	for _, pl := range lists {
		keys = append(keys, pl.Items...)
	}
	items, err := s.owned(ctx, pluginID, kindKey, lib.ID, keys)
	if err != nil {
		return nil, err
	}
	out := make([]plugins.KindPlaylist, 0, len(lists))
	for _, pl := range lists {
		if pl.Smart {
			continue
		}
		p := s.project(pl, items)
		// A playlist with entries but none of this kind belongs to another
		// library (a movie night list); an empty one may be filled here.
		if p.Total > 0 && len(p.Items) == 0 {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (s adapterViewerState) playlist(ctx context.Context, viewer *core.User, id string) (*core.Playlist, error) {
	key, err := core.ParseRatingKey(id)
	if err != nil {
		return nil, core.ErrNotFound
	}
	pl, err := s.app.playlists.Get(ctx, viewer.ID, key)
	if err != nil {
		return nil, err
	}
	if pl == nil || pl.UserID != viewer.ID {
		return nil, core.ErrNotFound
	}
	return pl, nil
}

func (s adapterViewerState) Playlist(ctx context.Context, pluginID string, viewerID int64, kindKey, id string) (plugins.KindPlaylist, error) {
	viewer, lib, err := s.viewer(ctx, pluginID, viewerID, kindKey)
	if err != nil {
		return plugins.KindPlaylist{}, err
	}
	pl, err := s.playlist(ctx, viewer, id)
	if err != nil {
		return plugins.KindPlaylist{}, err
	}
	items, err := s.owned(ctx, pluginID, kindKey, lib.ID, pl.Items)
	if err != nil {
		return plugins.KindPlaylist{}, err
	}
	return s.project(*pl, items), nil
}

func (s adapterViewerState) CreatePlaylist(ctx context.Context, pluginID string, viewerID int64, kindKey, title string, ids []string) (plugins.KindPlaylist, error) {
	viewer, lib, err := s.viewer(ctx, pluginID, viewerID, kindKey)
	if err != nil {
		return plugins.KindPlaylist{}, err
	}
	keys, err := s.keysInOrder(ctx, pluginID, viewerID, kindKey, ids)
	if err != nil {
		return plugins.KindPlaylist{}, err
	}
	pl, err := s.app.playlists.Create(ctx, viewer.ID, title, keys)
	if err != nil {
		return plugins.KindPlaylist{}, err
	}
	items, err := s.owned(ctx, pluginID, kindKey, lib.ID, pl.Items)
	if err != nil {
		return plugins.KindPlaylist{}, err
	}
	return s.project(*pl, items), nil
}

// keysInOrder maps ids to rating keys keeping duplicates and order (a
// playlist may hold one track twice).
func (s adapterViewerState) keysInOrder(ctx context.Context, pluginID string, viewerID int64, kindKey string, ids []string) ([]core.RatingKey, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	byID := make(map[string]core.RatingKey, len(unique))
	for start := 0; start < len(unique); start += plugins.MaxKindStateIDs {
		chunk := unique[start:min(start+plugins.MaxKindStateIDs, len(unique))]
		got, keys, err := s.keys(ctx, pluginID, viewerID, kindKey, chunk)
		if err != nil {
			return nil, err
		}
		for i, id := range got {
			byID[id] = keys[i]
		}
	}
	out := make([]core.RatingKey, 0, len(ids))
	for _, id := range ids {
		if k, ok := byID[id]; ok {
			out = append(out, k)
		}
	}
	return out, nil
}

func (s adapterViewerState) UpdatePlaylist(ctx context.Context, pluginID string, viewerID int64, kindKey, id string, update plugins.KindPlaylistUpdate) (plugins.KindPlaylist, error) {
	viewer, lib, err := s.viewer(ctx, pluginID, viewerID, kindKey)
	if err != nil {
		return plugins.KindPlaylist{}, err
	}
	pl, err := s.playlist(ctx, viewer, id)
	if err != nil {
		return plugins.KindPlaylist{}, err
	}
	if pl.Smart {
		return plugins.KindPlaylist{}, core.ErrNotAvailable
	}
	if update.Title != nil || update.Summary != nil {
		editor, ok := s.app.playlists.(core.PlaylistEditor)
		if !ok {
			return plugins.KindPlaylist{}, core.ErrNotAvailable
		}
		if _, err := editor.EditPlaylist(ctx, viewer.ID, pl.Key, core.PlaylistEdit{Title: update.Title, Summary: update.Summary}); err != nil {
			return plugins.KindPlaylist{}, err
		}
	}
	if len(update.Remove) > 0 {
		// Only positions that hold this plugin kind's items may be removed
		// through it; remove from the end so earlier positions stay valid.
		items, err := s.owned(ctx, pluginID, kindKey, lib.ID, pl.Items)
		if err != nil {
			return plugins.KindPlaylist{}, err
		}
		remove := slices.Clone(update.Remove)
		sort.Sort(sort.Reverse(sort.IntSlice(remove)))
		last := -1
		for _, idx := range remove {
			if idx == last || idx < 0 || idx >= len(pl.Items) {
				continue
			}
			last = idx
			if _, ok := items[pl.Items[idx]]; !ok {
				continue
			}
			if err := s.app.playlists.Remove(ctx, viewer.ID, pl.Key, idx); err != nil {
				return plugins.KindPlaylist{}, err
			}
		}
	}
	if len(update.Add) > 0 {
		keys, err := s.keysInOrder(ctx, pluginID, viewerID, kindKey, update.Add)
		if err != nil {
			return plugins.KindPlaylist{}, err
		}
		if len(keys) > 0 {
			if err := s.app.playlists.Add(ctx, viewer.ID, pl.Key, keys); err != nil {
				return plugins.KindPlaylist{}, err
			}
		}
	}
	return s.Playlist(ctx, pluginID, viewerID, kindKey, id)
}

func (s adapterViewerState) DeletePlaylist(ctx context.Context, pluginID string, viewerID int64, kindKey, id string) error {
	viewer, _, err := s.viewer(ctx, pluginID, viewerID, kindKey)
	if err != nil {
		return err
	}
	pl, err := s.playlist(ctx, viewer, id)
	if err != nil {
		return err
	}
	return s.app.playlists.Delete(ctx, viewer.ID, pl.Key)
}
