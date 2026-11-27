package mediabrowser

import (
	"fmt"
	"net/http"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/libattr"
)

// A movie or show names, as ParentId, the library that lists it for the
// viewer (#788): two libraries of one type over one source, split by rules,
// each own the titles their rules show. Emby used to name the built-in
// library of the type, and Jellyfin left the field out once a type had two
// libraries.

// titleLibraries builds the resolver for the request's viewer: the library
// the request browses (ParentId) first, then the built-in library of the
// type, then the other catalogue libraries in published order. Plugin
// libraries own their items by key and are not candidates.
func (i *instance) titleLibraries(r *http.Request, viewer *frontend.Viewer) (libattr.Resolver, bool) {
	if r == nil || viewer == nil || i.host.Experience == nil {
		return libattr.Resolver{}, false
	}
	views, err := i.host.Experience.Views(r.Context(), viewer)
	if err != nil {
		return libattr.Resolver{}, false
	}
	prefer, _, _ := parseLibraryParentView(r)
	res := libattr.Resolver{
		Candidates: map[frontend.MediaType][]frontend.LibraryID{},
		Memo:       fmt.Sprintf("mb:%p:%d:%s", i, viewer.ID, prefer),
	}
	seen := map[frontend.LibraryID]bool{}
	add := func(v frontend.View) {
		if pseJellyfinIsThemeViewID(v.ID) || v.PluginID != "" || v.KindKey != "" || (v.Type != frontend.TypeMovie && v.Type != frontend.TypeShow) {
			return
		}
		lib := v.LibraryID
		if lib == 0 {
			n, err := ids.ParseDecimal(v.ID)
			if err != nil || n <= 0 {
				return
			}
			lib = frontend.LibraryID(n)
		}
		if seen[lib] {
			return
		}
		seen[lib] = true
		res.Candidates[v.Type] = append(res.Candidates[v.Type], lib)
	}
	for _, v := range views {
		if prefer != "" && v.ID == prefer {
			add(v)
		}
	}
	for _, v := range views {
		if v.LibraryID == frontend.LibraryMovies || v.LibraryID == frontend.LibraryShows {
			add(v)
		}
	}
	for _, v := range views {
		add(v)
	}
	if sr, ok := i.host.Experience.(frontend.LibraryScopeReader); ok {
		res.Members = sr.LibraryMembers
	}
	return res, true
}

// primeTitleLibraries resolves a page's libraries in one pass (one
// membership read per candidate library), so each row's attachTitleLibrary
// reads the request memo.
func (i *instance) primeTitleLibraries(r *http.Request, viewer *frontend.Viewer, titles []frontend.Title) {
	if res, ok := i.titleLibraries(r, viewer); ok {
		res.Resolve(r.Context(), titles)
	}
}

// attachTitleLibrary sets a movie's or show's ParentId to the library that
// lists it. A title no candidate lists keeps what the item already carries.
func (i *instance) attachTitleLibrary(r *http.Request, viewer *frontend.Viewer, item map[string]any, title frontend.Title) {
	if item == nil || (title.Type != frontend.TypeMovie && title.Type != frontend.TypeShow) || title.Key.IsPluginKey() || mbDeclaredNonVideo(title) {
		return
	}
	if r != nil {
		if parent := pseJellyfinScopedParent(r); parent != "" {
			item["ParentId"] = parent
			return
		}
	}
	res, ok := i.titleLibraries(r, viewer)
	if !ok {
		return
	}
	lib, ok := res.Resolve(r.Context(), []frontend.Title{title})[title.Key]
	if !ok {
		return
	}
	if id, err := ids.GUID(ids.View, int64(lib)); err == nil {
		item["ParentId"] = id
	}
}
