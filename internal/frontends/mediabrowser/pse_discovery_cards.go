package mediabrowser

import (
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
	"net/http"
	"strconv"
)

const discoveryViewBase int64 = 1 << 58

func (i *instance) discoveryCardsEnabled() bool {
	return i.host.HostConfig != nil && i.host.HostConfig().DiscoveryCards
}
func discoveryGroup(n int64) string {
	for j, g := range frontend.NavigationGroups {
		if n == discoveryViewBase+int64(j)+1 {
			return g.ID
		}
	}
	return ""
}

// Mixed views are eligible for Jellyfin latest-media shelves and landscape cards.
// A boxsets view is explicitly excluded by Jellyfin Web from those shelves.
func discoveryFolder(n int) map[string]any {
	id, _ := ids.GUID(ids.View, discoveryViewBase+int64(n)+1)
	return map[string]any{"Id": id, "Name": frontend.NavigationGroups[n].Name, "Type": "CollectionFolder", "CollectionType": "", "IsFolder": true, "DisplayPreferencesId": id, "SortName": "00" + strconv.Itoa(n)}
}
func (i *instance) discoveryViews(r *http.Request, v *frontend.Viewer, items []any) []any {
	if !i.discoveryCardsEnabled() {
		return items
	}
	out := []any{}
	for _, n := range i.activeDiscoveryGroups(r, v) {
		item := discoveryFolder(n)
		item["ServerId"] = i.info.ID
		if i.protocol == "jellyfin" {
			i.jellyfinCollectionFolder(item, item["Id"].(string))
			item["ParentId"] = jellyfinRootID()
		}
		if i.protocol == "emby" {
			i.embyViewDTO(item)
		}
		out = append(out, item)
	}
	return append(out, items...)
}
func (i *instance) discoveryFolderDetail(w http.ResponseWriter, r *http.Request, n int64) bool {
	if discoveryGroup(n) == "" {
		return false
	}
	if !i.discoveryCardsEnabled() {
		http.NotFound(w, r)
		return true
	}
	item := discoveryFolder(int(n - discoveryViewBase - 1))
	item["ServerId"] = i.info.ID
	if i.protocol == "jellyfin" {
		i.jellyfinCollectionFolder(item, item["Id"].(string))
		item["ParentId"] = jellyfinRootID()
	}
	if i.protocol == "emby" {
		i.embyViewDTO(item)
	}
	i.replyItems(w, r, item)
	return true
}
func (i *instance) discoveryItems(w http.ResponseWriter, r *http.Request, v *frontend.Viewer, latest bool) bool {
	parents := route.Query(r.URL.Query(), "ParentId")
	if len(parents) != 1 {
		return false
	}
	n, err := ids.ParseGUID(ids.View, parents[0])
	if err != nil || discoveryGroup(n) == "" {
		return false
	}
	if !i.discoveryCardsEnabled() {
		http.NotFound(w, r)
		return true
	}
	offset, limit := queryInt(r, "StartIndex", 0), queryInt(r, "Limit", 50)
	page, err := i.host.Experience.Collections(r.Context(), v, device(r), frontend.CollectionQuery{NavigationOnly: true, NavigationGroup: discoveryGroup(n), Offset: offset, Limit: limit})
	if err != nil {
		http.Error(w, "Unavailable", 503)
		return true
	}
	_, token, _ := i.viewer(r)
	items := []any{}
	for _, c := range page.Items {
		item := i.boxSetDTO(c)
		detail, e := i.host.Experience.Item(i.identityContext(r), frontend.ItemRequest{Viewer: v, Device: device(r), Key: c.Key})
		if e == nil {
			detail.Title.Key = c.Key
			i.discoveryArtwork(r, v, &detail)
			tags, backs, metadata := i.imageTags(r, detail, token)
			for k, val := range metadata {
				item[k] = val
			}
			item["ImageTags"] = tags
			item["BackdropImageTags"] = backs
		}
		item["PrimaryImageAspectRatio"] = float64(16) / 9
		items = append(items, item)
	}
	if latest {
		i.replyItems(w, r, items)
	} else {
		i.replyItems(w, r, i.queryResult(items, page.Total, offset))
	}
	return true
}

// Overlay only response preferences. Persisted viewer choices are never rewritten.
func (i *instance) discoveryPreferences(r *http.Request, v *frontend.Viewer, cfg map[string]any) {
	if !i.discoveryCardsEnabled() {
		return
	}
	order := []string{}
	for _, n := range i.activeDiscoveryGroups(r, v) {
		id, _ := discoveryFolder(n)["Id"].(string)
		order = append(order, id)
	}
	excludes := []string{}
	if views, e := i.host.Experience.Views(r.Context(), v); e == nil {
		for _, view := range views {
			n, e := ids.ParseDecimal(view.ID)
			if e == nil {
				id, _ := ids.GUID(ids.View, n)
				order = append(order, id)
				excludes = append(excludes, id)
			}
		}
	}
	cfg["OrderedViews"] = order
	cfg["LatestItemsExcludes"] = excludes
}

func (i *instance) discoveryArtwork(r *http.Request, v *frontend.Viewer, detail *frontend.ItemDetail) {
	if detail.Collection == nil || detail.Collection.NavigationGroup == "" || i.host.Artwork == nil {
		return
	}
	detail.Title.Key = detail.Collection.Key
	images, complete, err := i.host.Artwork.List(r.Context(), frontend.ArtworkRequest{Viewer: v, Device: device(r), Item: detail.Collection.Key})
	if err == nil {
		detail.Images = images
		detail.ImagesAuthoritative = complete
	}
}

func (i *instance) activeDiscoveryGroups(r *http.Request, v *frontend.Viewer) []int {
	groups := []int{}
	page, err := i.host.Experience.Collections(r.Context(), v, device(r), frontend.CollectionQuery{NavigationOnly: true, Limit: 500})
	if err != nil {
		return groups
	}
	active := map[string]bool{}
	for _, c := range page.Items {
		active[c.NavigationGroup] = true
	}
	for _, n := range frontend.NavigationGroupOrder() {
		g := frontend.NavigationGroups[n]
		if active[g.ID] {
			groups = append(groups, n)
		}
	}
	return groups
}
