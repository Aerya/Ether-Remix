package mediabrowser

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/scrobview"
)

// Ajouts PSE pour Scrob (voir docs/pse/FORK.fr.md) :
//   - bibliothèques « Vus et notés » (PSE_SCROB_VIEWS=on) ;
//   - /Items?AnyProviderIdEquals=Tmdb.123 ;
//   - POST /Users/{userId}/Items/{itemId}/UserData (note, vu, favori).

// pseViews ajoute les bibliothèques PSE à /Users/{id}/Views.
func (i *instance) pseViews(r *http.Request, v *frontend.Viewer, items []any) []any {
	for _, view := range scrobview.Views() {
		if item, ok := i.pseFolder(r, v, view); ok {
			items = append(items, item)
		}
	}
	if item, ok := i.pseMusicFolder(); ok {
		items = append(items, item)
	}
	return items
}

func (i *instance) pseFolder(r *http.Request, v *frontend.Viewer, view scrobview.View) (map[string]any, bool) {
	viewID, err := ids.GUID(ids.View, view.Number)
	if err != nil {
		return nil, false
	}
	kind := "movies"
	if view.Type == scrobview.TypeShow {
		kind = "tvshows"
	}
	item := map[string]any{"Id": viewID, "Name": view.Name, "Type": "CollectionFolder", "CollectionType": kind, "IsFolder": true, "ServerId": i.info.ID, "SortName": view.Name, "DisplayPreferencesId": viewID, "CanDelete": false, "CanDownload": false, "ProviderIds": map[string]string{}}
	if i.protocol == "emby" {
		i.embyViewDTO(item)
	}
	if i.protocol == "jellyfin" {
		i.jellyfinCollectionFolder(item, viewID)
		item["ParentId"] = jellyfinRootID()
	}
	return item, true
}

// pseWriteFolder répond à /Items/{id} pour une bibliothèque PSE.
func (i *instance) pseWriteFolder(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, viewNum int64) bool {
	view, ok := scrobview.Lookup(viewNum)
	if !ok {
		return false
	}
	item, _ := i.pseFolder(r, v, view)
	i.replyItems(w, r, item)
	return true
}

// pseItems répond à /Items pour une bibliothèque PSE ou une recherche par
// identifiant externe. Renvoie false si la requête ne concerne pas PSE.
func (i *instance) pseItems(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, parent []string, q frontend.ItemQuery) bool {
	if i.pseScrobEpisode(w, r, v, q) {
		return true
	}
	var titles []frontend.Title
	var err error
	switch {
	case len(parent) == 1 && parent[0] != "":
		num, e := ids.ParseGUID(ids.View, parent[0])
		if e != nil {
			return false
		}
		view, ok := scrobview.Lookup(num)
		if !ok {
			return false
		}
		titles, err = scrobview.Items(r.Context(), v.ID, view, q.Types)
		sort.SliceStable(titles, func(a, b int) bool {
			if titles[a].Type != titles[b].Type {
				return titles[a].Type > titles[b].Type // séries avant épisodes
			}
			if titles[a].ShowKey != titles[b].ShowKey {
				return titles[a].ShowKey < titles[b].ShowKey
			}
			if titles[a].Season != titles[b].Season {
				return titles[a].Season < titles[b].Season
			}
			if titles[a].Episode != titles[b].Episode {
				return titles[a].Episode < titles[b].Episode
			}
			return titles[a].SortTitle < titles[b].SortTitle
		})
	default:
		value := strings.TrimSpace(firstQuery(r, "AnyProviderIdEquals"))
		if value == "" {
			return false
		}
		titles, err = scrobview.ByProviderID(r.Context(), value, q.Types, func(ctx context.Context, id string, typ frontend.MediaType) (frontend.RatingKey, error) {
			if i.host.Experience == nil {
				return 0, nil
			}
			return i.host.Experience.ResolveExternal(ctx, v, device(r), frontend.ExternalRequest{Source: "imdb", ID: id, Type: typ})
		})
	}
	if err != nil {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return true
	}
	total := len(titles)
	start := min(max(q.Offset, 0), total)
	end := total
	if q.Limit > 0 {
		end = min(start+q.Limit, total)
	}
	i.page(w, r, v, frontend.Page{Items: titles[start:end], Total: total}, q.Offset)
	return true
}

// Scrob's reverse sync looks up an episode using SeriesId rather than ParentId.
// Resolve the exact key through Experience so viewer visibility rules still apply.
func (i *instance) pseScrobEpisode(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, q frontend.ItemQuery) bool {
	series := firstQuery(r, "SeriesId")
	seasonRaw, episodeRaw := firstQuery(r, "ParentIndexNumber"), firstQuery(r, "IndexNumber")
	if series == "" || seasonRaw == "" || episodeRaw == "" {
		return false
	}
	show, err := ids.ParseRatingKeyGUID(series)
	season, se := strconv.Atoi(seasonRaw)
	episode, ee := strconv.Atoi(episodeRaw)
	if err != nil || show.Kind() != core.KindShow || se != nil || ee != nil || season < 0 || season > 999 || episode < 1 || episode > 9999 {
		http.Error(w, "Invalid episode selector", http.StatusBadRequest)
		return true
	}
	page := frontend.Page{}
	allowed := len(q.Types) == 0
	for _, typ := range q.Types {
		allowed = allowed || typ == core.TypeEpisode
	}
	if allowed {
		key := core.EpisodeKey(show.TMDB(), season, episode)
		if key.Kind() != core.KindEpisode || key.TMDB() != show.TMDB() || key.Season() != season || key.Episode() != episode {
			http.Error(w, "Invalid episode selector", http.StatusBadRequest)
			return true
		}
		detail, err := i.host.Experience.Item(i.identityContext(r), frontend.ItemRequest{Viewer: v, Device: device(r), Key: key})
		if err != nil && !errors.Is(err, frontend.ErrNotFound) && !errors.Is(err, core.ErrNotAvailable) {
			http.Error(w, "Unavailable", http.StatusServiceUnavailable)
			return true
		}
		if err == nil && detail.Title.Key == key && detail.Title.Type == core.TypeEpisode {
			page.Total = 1
			if q.Offset == 0 {
				page.Items = []frontend.Title{detail.Title}
			}
		}
	}
	i.page(w, r, v, page, q.Offset)
	return true
}

// pseRoutes enregistre POST /Users/{userId}/Items/{itemId}/UserData, que
// Scrob utilise pour envoyer une note (échelle 0–10).
func (i *instance) pseRoutes() error {
	for _, pattern := range []string{"/Users/{userId}/Items/{itemId}/UserData", "/UserItems/{itemId}/UserData"} {
		if err := i.router.Handle("POST", pattern, http.HandlerFunc(i.pseUpdateUserData)); err != nil {
			return err
		}
	}
	return nil
}

func (i *instance) pseUpdateUserData(w http.ResponseWriter, r *http.Request) {
	v, _, err := i.viewer(r)
	if err != nil || v == nil {
		http.Error(w, "Unauthorized", 401)
		return
	}
	if i.host.Experience == nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	key, err := ids.ParseRatingKeyGUID(r.PathValue("itemId"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var body struct {
		Rating     *float64 `json:"Rating"`
		Played     *bool    `json:"Played"`
		IsFavorite *bool    `json:"IsFavorite"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil || (len(raw) > 0 && json.Unmarshal(raw, &body) != nil) {
		http.Error(w, "Bad Request", 400)
		return
	}
	ctx := r.Context()
	if body.Rating != nil {
		rating := *body.Rating
		if rating < 0 || rating > 10 {
			http.Error(w, "Bad Request", 400)
			return
		}
		if err := i.host.Experience.SetRating(ctx, v, key, rating); err != nil {
			http.Error(w, "Unavailable", 503)
			return
		}
	}
	if body.Played != nil {
		if *body.Played {
			err = i.host.Experience.SetPlayed(ctx, v, key)
		} else {
			err = i.host.Experience.SetUnplayed(ctx, v, key)
		}
		if err != nil {
			http.Error(w, "Unavailable", 503)
			return
		}
	}
	if body.IsFavorite != nil {
		if err := i.host.Experience.SetFavorite(ctx, v, key, *body.IsFavorite); err != nil {
			http.Error(w, "Unavailable", 503)
			return
		}
	}
	data, err := i.host.Experience.UserData(ctx, v, []frontend.RatingKey{key})
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return
	}
	jsonReply(w, i.userDataDTO(ids.RatingKeyGUID(key), data[key]))
}
