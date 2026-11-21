package mediabrowser

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/paging"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
)

type pseJellyfinThemeRows interface {
	PSEThemeRowViews(context.Context, *frontend.Viewer, string) ([]frontend.View, error)
	PSEThemeRowView(context.Context, *frontend.Viewer, string) (frontend.View, error)
}

// Jellyfin Web utilise null pour sa vue mixte native. Swiftfin le traite
// comme une bibliothèque de dossiers avant de filtrer les types pris en charge.
func (i *instance) pseJellyfinMixedTheme(view frontend.View, number int64) bool {
	return i.protocol == "jellyfin" && view.Type == "mixed" && frontend.IsPSEThemeJellyfinView(number)
}

func pseJellyfinIsThemeViewID(id string) bool {
	number, err := ids.ParseDecimal(id)
	return err == nil && frontend.IsPSEThemeJellyfinView(number)
}

func (i *instance) pseJellyfinThemeParent(r *http.Request) (int64, error) {
	if i.protocol != "jellyfin" {
		return 0, nil
	}
	parents := route.Query(r.URL.Query(), "ParentId")
	for _, parent := range parents {
		if number, err := ids.ParseGUID(ids.View, parent); err == nil && frontend.IsPSEThemeJellyfinView(number) {
			if len(parents) != 1 {
				return 0, errors.New("ambiguous theme parent")
			}
			return number, nil
		}
	}
	return 0, nil
}

type pseJellyfinScopedParentKey struct{}

func pseJellyfinScopedParent(r *http.Request) string {
	parent, _ := r.Context().Value(pseJellyfinScopedParentKey{}).(string)
	return parent
}

func (i *instance) pseJellyfinThemeRowDTO(view frontend.View, kind string) (map[string]any, bool) {
	number, err := ids.ParseDecimal(view.ID)
	if err != nil || !frontend.IsPSEThemeJellyfinRow(number) {
		return nil, false
	}
	id, _ := ids.GUID(ids.View, number)
	parent, _ := ids.GUID(ids.View, int64(view.LibraryID))
	item := map[string]any{
		"Id": id, "Name": view.Name, "SortName": view.Name, "Type": kind, "IsFolder": true,
		"ParentId": parent, "ServerId": i.info.ID, "CanDelete": false, "CanDownload": false,
		"ProviderIds": map[string]string{},
	}
	i.jellyfinCollectionFolder(item, id)
	return item, true
}

func (i *instance) pseJellyfinThemeRowDetail(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, number int64) bool {
	if i.protocol != "jellyfin" || !frontend.IsPSEThemeJellyfinRow(number) {
		return false
	}
	rows, ok := i.host.Experience.(pseJellyfinThemeRows)
	if !ok {
		http.NotFound(w, r)
		return true
	}
	view, err := rows.PSEThemeRowView(r.Context(), v, ids.Decimal(number))
	if err != nil {
		pseJellyfinThemeError(w, r, err)
		return true
	}
	item, ok := i.pseJellyfinThemeRowDTO(view, "BoxSet")
	if !ok {
		http.NotFound(w, r)
		return true
	}
	i.replyItems(w, r, item)
	return true
}

func (i *instance) pseJellyfinThemeItems(w http.ResponseWriter, r *http.Request, v *frontend.Viewer) bool {
	number, err := i.pseJellyfinThemeParent(r)
	if err != nil {
		http.Error(w, "Invalid ParentId", http.StatusBadRequest)
		return true
	}
	if number == 0 {
		return false
	}
	if i.host.Experience == nil {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
		return true
	}
	viewID := ids.Decimal(number)
	q := itemQuery(r)
	if requestedSpecialItems(r) == specialBoxSets && !frontend.IsPSEThemeJellyfinRow(number) {
		rows, err := i.pseJellyfinThemeRowItems(r, v, viewID, q, "BoxSet")
		if err != nil {
			pseJellyfinThemeError(w, r, err)
			return true
		}
		i.replyItems(w, r, i.queryResult(paging.Slice(rows, q.Offset, q.Limit), len(rows), q.Offset))
		return true
	}
	if err := navigationQuery(r, &q, false); err != nil {
		http.Error(w, "Invalid navigation anchor", http.StatusBadRequest)
		return true
	}
	if err := i.resolveFilters(r, v, &q); err != nil {
		http.Error(w, "Invalid filter", http.StatusBadRequest)
		return true
	}
	// Une requête Ids avec ParentId doit respecter les deux contraintes.
	if raw := route.Query(r.URL.Query(), "Ids"); len(raw) > 0 {
		q.Filter.IncludeKeys = pseJellyfinThemeItemKeys(raw)
		for _, key := range q.Filter.IncludeKeys {
			if key.Kind() == frontend.KindEpisode || key.Kind() == frontend.KindSeason {
				q.Recursive = true
				break
			}
		}
	}
	pseLibrarySort(r, &q)
	// Les clients mixtes demandent notamment Folder,Movie,Series. Préfixer
	// les titres par les rangées et paginer la liste combinée une seule fois.
	var folders []any
	var extra []any
	titles := q
	withFolders := !frontend.IsPSEThemeJellyfinRow(number) && q.AdjacentTo == 0 && slices.Contains(q.UnservedTypes, "folder")
	if withFolders {
		folders, err = i.pseJellyfinThemeRowItems(r, v, viewID, q, "Folder")
		if err != nil {
			pseJellyfinThemeError(w, r, err)
			return true
		}
		extra = paging.Slice(folders, q.Offset, q.Limit)
		if len(q.Types) == 0 {
			i.replyItems(w, r, i.queryResult(extra, len(folders), q.Offset))
			return true
		}
		titles.Offset = max(0, q.Offset-len(folders))
		titles.Limit = max(1, q.Limit-len(extra))
	}
	page, err := i.host.Experience.LibraryItems(r.Context(), v, device(r), viewID, titles)
	if err != nil {
		pseJellyfinThemeError(w, r, err)
		return true
	}
	if withFolders {
		page.Items = page.Items[:min(len(page.Items), q.Limit-len(extra))]
		page.Total += len(folders) - len(extra)
	}
	parent, _ := ids.GUID(ids.View, number)
	r = r.WithContext(context.WithValue(r.Context(), pseJellyfinScopedParentKey{}, parent))
	i.pageWithExtra(w, r, v, page, q.Offset, 0, "", nil, extra)
	return true
}

func pseJellyfinThemeItemKeys(values []string) []frontend.RatingKey {
	keys := make([]frontend.RatingKey, 0)
	seen := make(map[frontend.RatingKey]bool)
	for _, value := range values {
		for _, id := range splitCSV(value) {
			key, err := ids.ParseRatingKeyGUID(id)
			if err != nil || seen[key] {
				continue
			}
			seen[key] = true
			keys = append(keys, key)
			if len(keys) == maxItemsByID {
				return keys
			}
		}
	}
	return keys
}

func (i *instance) pseJellyfinThemeRowItems(r *http.Request, v *frontend.Viewer, viewID string, q frontend.ItemQuery, kind string) ([]any, error) {
	rows, ok := i.host.Experience.(pseJellyfinThemeRows)
	if !ok {
		return nil, frontend.ErrNotFound
	}
	views, err := rows.PSEThemeRowViews(r.Context(), v, viewID)
	if err != nil {
		return nil, err
	}
	if q.Sort == frontend.SortTitle {
		slices.SortStableFunc(views, func(a, b frontend.View) int {
			if q.Desc {
				a, b = b, a
			}
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		})
	}
	items := make([]any, 0, len(views))
	if !slices.Contains(q.ExcludedTypes, frontend.MediaType(strings.ToLower(kind))) {
		values := route.Query(r.URL.Query(), "Ids")
		wanted := make(map[string]bool)
		for _, value := range values {
			for _, id := range splitCSV(value) {
				if number, err := ids.ParseGUID(ids.View, id); err == nil && frontend.IsPSEThemeJellyfinRow(number) {
					wanted[ids.Decimal(number)] = true
				}
			}
		}
		for _, view := range views {
			if (len(values) == 0 || wanted[view.ID]) && pseJellyfinMatchesThemeRow(view.Name, q, kind) {
				if item, ok := i.pseJellyfinThemeRowDTO(view, kind); ok {
					items = append(items, item)
				}
			}
		}
	}
	return items, nil
}

func pseJellyfinMatchesThemeRow(name string, q frontend.ItemQuery, kind string) bool {
	f := q.Filter
	name = strings.ToLower(name)
	if !strings.Contains(name, strings.ToLower(f.Query)) {
		return false
	}
	if kind != "Folder" {
		return true
	}
	// Les dossiers n'ont pas de genre, de crédit, de note ni d'état de lecture.
	return q.Watched == nil && q.Favorite == nil && len(f.Genres) == 0 && f.Studio == "" && f.Person == 0 &&
		len(f.Years) == 0 && len(f.ExcludeYears) == 0 && len(f.ReleasedFilters) == 0 && f.MinRating == 0 &&
		len(f.ContentRatings) == 0 && f.MinRuntime == 0 && f.MaxRuntime == 0 &&
		strings.HasPrefix(name, strings.ToLower(f.NameStartsWith)) && name >= strings.ToLower(f.NameStartsWithOrGreater) &&
		(f.NameLessThan == "" || name < strings.ToLower(f.NameLessThan))
}

// Copie de browse.go search (SearchHints), à resynchroniser avec Ether.
// Pas de type boxset : une vue Remix ne renvoie que des films, séries, saisons
// et épisodes ; ses collections sont des dossiers servis par /Items, pas par
// la recherche.
// pseJellyfinSearch answers /Search/Hints when ParentId names a Remix view. The
// search then stays in that view and skips Live TV. It returns false for any
// other request, which keeps the usual search.
func (i *instance) pseJellyfinSearch(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, term string, q frontend.ItemQuery) bool {
	number, err := i.pseJellyfinThemeParent(r)
	if err != nil {
		http.Error(w, "Invalid ParentId", http.StatusBadRequest)
		return true
	}
	if number == 0 {
		return false
	}
	q.Filter.Query = term
	q.Recursive = q.Recursive || slices.Contains(q.Types, frontend.MediaType("season")) || slices.Contains(q.Types, frontend.MediaType("episode"))
	page, err := i.host.Experience.LibraryItems(r.Context(), v, device(r), ids.Decimal(number), q)
	if err != nil {
		pseJellyfinThemeError(w, r, err)
		return true
	}
	images := i.gridListImages(r, page.Items)
	hints := make([]any, 0, len(page.Items))
	for _, t := range page.Items {
		id := ids.RatingKeyGUID(t.Key)
		kind := map[frontend.MediaType]string{"movie": "Movie", "show": "Series", "season": "Season", "episode": "Episode"}[t.Type]
		hint := map[string]any{"Id": id, "ItemId": id, "Name": t.Title, "Type": kind, "ProductionYear": t.Year, "IsFolder": t.Type == "show" || t.Type == "season"}
		if tags, ok := images[t.Key]["ImageTags"].(map[string]string); ok && tags["Primary"] != "" {
			hint["PrimaryImageTag"] = tags["Primary"]
		}
		if t.Type == "movie" || t.Type == "episode" {
			hint["MediaType"] = "Video"
		}
		if t.Runtime > 0 {
			hint["RunTimeTicks"] = t.Runtime.Nanoseconds() / 100
		}
		if t.Type == "episode" {
			hint["IndexNumber"] = t.Episode
			hint["ParentIndexNumber"] = t.Season
			if t.ShowTitle != "" {
				hint["Series"] = t.ShowTitle
			}
		}
		hints = append(hints, hint)
	}
	i.replyItems(w, r, map[string]any{"SearchHints": hints, "TotalRecordCount": page.Total})
	return true
}

func pseJellyfinThemeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, frontend.ErrNotFound) {
		http.NotFound(w, r)
	} else {
		http.Error(w, "Unavailable", http.StatusServiceUnavailable)
	}
}
