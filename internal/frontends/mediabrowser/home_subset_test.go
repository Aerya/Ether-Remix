package mediabrowser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type subsetLatestExperience struct {
	frontendtest.Experience
	latest map[string][]frontend.LatestItem
}

func (e subsetLatestExperience) LatestAdded(_ context.Context, _ *frontend.Viewer, _ frontend.Device, viewID string, limit int) ([]frontend.LatestItem, error) {
	items := e.latest[viewID]
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (e subsetLatestExperience) Resume(context.Context, *frontend.Viewer, frontend.Device, string, []frontend.MediaType, int, int, bool) (frontend.ActivityPage, error) {
	return frontend.ActivityPage{}, nil
}

var _ frontend.LibraryActivity = subsetLatestExperience{}

func TestHomeSectionsAndLatestExcludesSuppressRedundantRuleSubsets(t *testing.T) {
	owner := &frontend.Viewer{ID: 1, Username: "owner"}
	kid := &frontend.Viewer{ID: 2, Username: "kid"}
	identity := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		switch token {
		case "owner":
			return owner, nil
		case "kid":
			return kid, nil
		default:
			return nil, errors.New("denied")
		}
	}}
	latestItem := func(k frontend.RatingKey, typ frontend.MediaType) frontend.LatestItem {
		return frontend.LatestItem{Title: frontend.Title{Key: k, Type: typ}}
	}
	exp := subsetLatestExperience{
		Experience: frontendtest.Experience{
			ViewsFunc: func(_ context.Context, v *frontend.Viewer) ([]frontend.View, error) {
				if v.ID == kid.ID {
					return []frontend.View{
						{ID: "3", LibraryID: 3, Name: "Kids Movies", Type: "movie", Subset: true},
					}, nil
				}
				return []frontend.View{
					{ID: "1", LibraryID: core.LibraryMovies, Name: "Movies", Type: "movie"},
					{ID: "2", LibraryID: core.LibraryShows, Name: "TV Shows", Type: "show"},
					{ID: "3", LibraryID: 3, Name: "Kids Movies", Type: "movie", Subset: true},
					{ID: "4", LibraryID: 4, Name: "Kids Shows", Type: "show", Subset: true},
				}, nil
			},
		},
		latest: map[string][]frontend.LatestItem{
			// Movies (1) and Kids Movies (3) overlap 3/4 (75%) -> Kids Movies is redundant.
			"1": {
				latestItem(core.MovieKey(1), "movie"),
				latestItem(core.MovieKey(2), "movie"),
				latestItem(core.MovieKey(3), "movie"),
				latestItem(core.MovieKey(4), "movie"),
			},
			"3": {
				latestItem(core.MovieKey(1), "movie"),
				latestItem(core.MovieKey(2), "movie"),
				latestItem(core.MovieKey(3), "movie"),
				latestItem(core.MovieKey(10), "movie"),
			},
			// TV Shows (2) and Kids Shows (4) overlap 1/4 (25%) -> Kids Shows differs meaningfully.
			"2": {
				latestItem(core.ShowKey(1), "show"),
				latestItem(core.ShowKey(2), "show"),
				latestItem(core.ShowKey(3), "show"),
				latestItem(core.ShowKey(4), "show"),
			},
			"4": {
				latestItem(core.ShowKey(1), "show"),
				latestItem(core.ShowKey(20), "show"),
				latestItem(core.ShowKey(21), "show"),
				latestItem(core.ShowKey(22), "show"),
			},
		},
	}

	moviesGUID, _ := ids.GUID(ids.View, 1)
	showsGUID, _ := ids.GUID(ids.View, 2)
	kidsMoviesGUID, _ := ids.GUID(ids.View, 3)
	kidsShowsGUID, _ := ids.GUID(ids.View, 4)
	ownerGUID, _ := ids.GUID(ids.User, 1)
	kidGUID, _ := ids.GUID(ids.User, 2)

	// 1. Emby HomeSections skips redundant Kids Movies (3) while keeping distinct Kids Shows (4).
	embyInst, err := (Emby{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: identity, Experience: exp})
	if err != nil {
		t.Fatal(err)
	}
	defer embyInst.Close(context.Background())

	embyCall := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("X-Emby-Token", token)
		w := httptest.NewRecorder()
		embyInst.Handler().ServeHTTP(w, r)
		return w
	}

	w := embyCall("/Users/"+ownerGUID+"/HomeSections", "owner")
	if w.Code != http.StatusOK {
		t.Fatalf("Emby HomeSections: %d %s", w.Code, w.Body)
	}
	var sections struct {
		Items []struct {
			SectionType  string
			CollectionId string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sections); err != nil {
		t.Fatal(err)
	}
	gotLatest := map[string]bool{}
	for _, s := range sections.Items {
		if s.SectionType == "LatestMedia" {
			gotLatest[s.CollectionId] = true
		}
	}
	if !gotLatest[moviesGUID] || !gotLatest[showsGUID] || !gotLatest[kidsShowsGUID] || gotLatest[kidsMoviesGUID] {
		t.Fatalf("Emby LatestMedia sections = %v, want Movies, TV Shows, Kids Shows (not redundant Kids Movies)", gotLatest)
	}

	// Kid viewer with only Kids Movies (3) keeps its LatestMedia section.
	w = embyCall("/Users/"+kidGUID+"/HomeSections", "kid")
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &sections) != nil {
		t.Fatalf("kid Emby HomeSections: %d %s", w.Code, w.Body)
	}
	kidHasKidsMovies := false
	for _, s := range sections.Items {
		if s.SectionType == "LatestMedia" && s.CollectionId == kidsMoviesGUID {
			kidHasKidsMovies = true
		}
	}
	if !kidHasKidsMovies {
		t.Fatalf("kid-only viewer should keep Kids Movies LatestMedia: %+v", sections.Items)
	}

	// 2. Jellyfin default LatestItemsExcludes hides redundant Kids Movies (3) without hiding distinct Kids Shows (4).
	savedPrefs := map[string]json.RawMessage{}
	prefs := frontendtest.Preferences{
		GetFunc: func(_ context.Context, v *frontend.Viewer, client, key string) (json.RawMessage, error) {
			b, ok := savedPrefs[client+":"+key]
			if !ok {
				return nil, frontend.ErrPreferencesNotFound
			}
			return b, nil
		},
		PutFunc: func(_ context.Context, v *frontend.Viewer, client, key string, b json.RawMessage) error {
			savedPrefs[client+":"+key] = append([]byte(nil), b...)
			return nil
		},
	}
	jfInst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: identity, Experience: exp, Preferences: prefs})
	if err != nil {
		t.Fatal(err)
	}
	defer jfInst.Close(context.Background())

	jfCall := func(method, path, token, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if body != "" {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		}
		r.Header.Set("X-Emby-Token", token)
		w := httptest.NewRecorder()
		jfInst.Handler().ServeHTTP(w, r)
		return w
	}

	me := jfCall(http.MethodGet, "/Users/Me", "owner", "")
	var user struct {
		Configuration struct {
			LatestItemsExcludes []string
		}
	}
	if me.Code != http.StatusOK || json.Unmarshal(me.Body.Bytes(), &user) != nil {
		t.Fatalf("Jellyfin /Users/Me: %d %s", me.Code, me.Body)
	}
	if len(user.Configuration.LatestItemsExcludes) != 1 || user.Configuration.LatestItemsExcludes[0] != kidsMoviesGUID {
		t.Fatalf("Jellyfin default LatestItemsExcludes = %v, want [%s]", user.Configuration.LatestItemsExcludes, kidsMoviesGUID)
	}

	// Explicit viewer configuration overrides default LatestItemsExcludes.
	if save := jfCall(http.MethodPost, "/Users/"+ownerGUID+"/Configuration", "owner", `{"LatestItemsExcludes":[]}`); save.Code != http.StatusNoContent {
		t.Fatalf("save LatestItemsExcludes: %d %s", save.Code, save.Body)
	}
	meAfter := jfCall(http.MethodGet, "/Users/Me", "owner", "")
	if meAfter.Code != http.StatusOK || json.Unmarshal(meAfter.Body.Bytes(), &user) != nil {
		t.Fatalf("Jellyfin /Users/Me after save: %d %s", meAfter.Code, meAfter.Body)
	}
	if len(user.Configuration.LatestItemsExcludes) != 0 {
		t.Fatalf("explicit empty LatestItemsExcludes should win, got %v", user.Configuration.LatestItemsExcludes)
	}
}
