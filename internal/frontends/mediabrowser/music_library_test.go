package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type musicTestExperience struct {
	frontendtest.Experience
	countFn       func(context.Context, *frontend.Viewer, string) (int, error)
	facetPeopleFn func(context.Context, *frontend.Viewer, frontend.Device, string, string, string, frontend.PageQuery) (frontend.FacetPersonPage, error)
}

func (m musicTestExperience) IndexedLibraryCount(ctx context.Context, v *frontend.Viewer, view string) (int, error) {
	if m.countFn != nil {
		return m.countFn(ctx, v, view)
	}
	return 0, nil
}

func (m musicTestExperience) LibraryFacetPeople(ctx context.Context, v *frontend.Viewer, d frontend.Device, viewID, field, order string, page frontend.PageQuery) (frontend.FacetPersonPage, error) {
	if m.facetPeopleFn != nil {
		return m.facetPeopleFn(ctx, v, d, viewID, field, order, page)
	}
	return frontend.FacetPersonPage{}, nil
}

// TestPageFreshnessInvalidatesPluginKindCaches verifies that browsing a plugin
// library before its first sync completes does not freeze Views ChildCount or
// /Items at zero once PageFreshness.KindSync (or Libraries) advances.
func TestPageFreshnessInvalidatesPluginKindCaches(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	var synced atomic.Bool
	var kindSyncGen atomic.Uint64
	albumKey := frontend.RatingKey(1<<62 | 1)
	musicViewID := "9"
	musicGUID, err := ids.GUID(ids.View, 9)
	if err != nil {
		t.Fatal(err)
	}

	exp := musicTestExperience{
		Experience: frontendtest.Experience{
			ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
				return []frontend.View{{ID: musicViewID, Name: "Music", Type: "movie", Native: "music", KindKey: "albums", PluginID: "test.music"}}, nil
			},
			LibraryItemsFunc: func(context.Context, *frontend.Viewer, frontend.Device, string, frontend.ItemQuery) (frontend.Page, error) {
				if !synced.Load() {
					return frontend.Page{Items: []frontend.Title{}, Total: 0}, nil
				}
				return frontend.Page{
					Items: []frontend.Title{{Key: albumKey, Type: frontend.TypeMovie, MBItemType: "MusicAlbum", Title: "Warmth"}},
					Total: 1,
				}, nil
			},
		},
		countFn: func(context.Context, *frontend.Viewer, string) (int, error) {
			if !synced.Load() {
				return 0, nil
			}
			return 1, nil
		},
	}

	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{
		Identity:   frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }},
		Experience: exp,
		PageFreshness: func() frontend.PageFreshness {
			return frontend.PageFreshness{Catalog: 1, KindSync: kindSyncGen.Load()}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	uid, _ := ids.GUID(ids.User, 1)
	getJSON := func(path string) map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/jellyfin"+path, nil)
		r.Header.Set("X-Emby-Token", "ok")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s decode: %v", path, err)
		}
		return out
	}

	// Pre-sync requests populate both viewFactCache and browseCache with 0 items.
	beforeViews := getJSON("/Users/" + uid + "/Views")
	viewsList := beforeViews["Items"].([]any)
	if len(viewsList) != 1 || viewsList[0].(map[string]any)["ChildCount"].(float64) != 0 {
		t.Fatalf("expected pre-sync ChildCount=0, got %+v", viewsList)
	}
	itemsPath := "/Users/" + uid + "/Items?ParentId=" + musicGUID + "&IncludeItemTypes=MusicAlbum&Recursive=true&Limit=10"
	beforeItems := getJSON(itemsPath)
	if beforeItems["TotalRecordCount"].(float64) != 0 {
		t.Fatalf("expected pre-sync TotalRecordCount=0, got %+v", beforeItems)
	}

	// Sync persists items and bumps only PageFreshness.KindSync (Catalog stays 1).
	synced.Store(true)
	kindSyncGen.Add(1)

	afterViews := getJSON("/Users/" + uid + "/Views")
	viewsList = afterViews["Items"].([]any)
	if viewsList[0].(map[string]any)["ChildCount"].(float64) != 1 {
		t.Fatalf("expected post-sync ChildCount=1 after KindSync bump, got %+v", viewsList)
	}
	afterItems := getJSON(itemsPath)
	if afterItems["TotalRecordCount"].(float64) != 1 {
		t.Fatalf("expected post-sync TotalRecordCount=1 after KindSync bump, got %+v", afterItems)
	}
}

// TestMusicLibraryEndpointsAndArtistNavigation verifies /Artists,
// /Artists/AlbumArtists, /MusicGenres, AlbumArtist/ArtistItems DTO credits,
// AlbumArtistIds filtering, and HomeSections LatestMedia for music libraries.
func TestMusicLibraryEndpointsAndArtistNavigation(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	musicViewID := "12"
	musicGUID, err := ids.GUID(ids.View, 12)
	if err != nil {
		t.Fatal(err)
	}
	artistPID := frontend.PluginPersonID("Bracket")
	artistGUID := frontend.FacetID("person", strconv.FormatInt(artistPID, 10))
	albumKey := frontend.RatingKey(1<<62 | 7)

	var seenPersonFilter int64
	exp := musicTestExperience{
		Experience: frontendtest.Experience{
			ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
				return []frontend.View{{
					ID:       musicViewID,
					Name:     "Deezer Music",
					Type:     "movie",
					Native:   "music",
					KindKey:  "albums",
					PluginID: "test.music",
					Facets:   []frontend.ViewFacet{{ID: "artists", Label: "Artists", Type: "people"}, {ID: "genres", Label: "Genres", Type: "strings"}},
				}}, nil
			},
			LibraryItemsFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, _ string, q frontend.ItemQuery) (frontend.Page, error) {
				for _, u := range q.UnservedTypes {
					if u == "musicartist" {
						return frontend.Page{}, nil
					}
				}
				seenPersonFilter = q.Filter.Person
				return frontend.Page{
					Items: []frontend.Title{{
						Key:        albumKey,
						Type:       frontend.TypeMovie,
						MBItemType: "MusicAlbum",
						Title:      "Novelty Forever",
						Genres:     []string{"Punk Rock"},
						Cast:       []frontend.Person{{TMDB: artistPID, Name: "Bracket"}},
					}},
					Total: 1,
				}, nil
			},
			FacetsFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, _ string, name string) ([]frontend.FacetValue, error) {
				if name == "genre" {
					return []frontend.FacetValue{{ID: frontend.FacetID("genre", "Punk Rock"), Key: "Punk Rock", Title: "Punk Rock", Count: 1}}, nil
				}
				return nil, nil
			},
			ResolveFacetFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, _ string, kind, id string) (frontend.FacetValue, error) {
				if kind == "person" && id == artistGUID {
					return frontend.FacetValue{ID: artistGUID, Key: strconv.FormatInt(artistPID, 10), Title: "Bracket"}, nil
				}
				return frontend.FacetValue{}, frontend.ErrNotFound
			},
		},
		facetPeopleFn: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, viewID, field, _ string, _ frontend.PageQuery) (frontend.FacetPersonPage, error) {
			if viewID != musicViewID || field != "artists" {
				t.Fatalf("unexpected facet people query: view=%q field=%q", viewID, field)
			}
			return frontend.FacetPersonPage{
				Items: []frontend.FacetPerson{{
					Person:    frontend.PersonDetail{ID: artistGUID, Name: "Bracket", TMDB: artistPID, ArtworkItem: albumKey},
					ItemCount: 3,
				}},
				Total: 1,
			}, nil
		},
	}

	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{
		Identity:   frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }},
		Experience: exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	uid, _ := ids.GUID(ids.User, 1)
	call := func(path string) map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/jellyfin"+path, nil)
		r.Header.Set("X-Emby-Token", "ok")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s decode: %v", path, err)
		}
		return out
	}

	// /Artists and /Artists/AlbumArtists serve MusicArtist items via SQL-backed facet people.
	for _, ep := range []string{"/Artists?ParentId=" + musicGUID, "/Artists/AlbumArtists?ParentId=" + musicGUID} {
		res := call(ep)
		items := res["Items"].([]any)
		if len(items) != 1 {
			t.Fatalf("%s: expected 1 artist, got %+v", ep, res)
		}
		a := items[0].(map[string]any)
		if a["Type"] != "MusicArtist" || a["Name"] != "Bracket" || a["Id"] != artistGUID {
			t.Fatalf("%s: unexpected artist DTO %+v", ep, a)
		}
		if a["AlbumCount"].(float64) != 3 {
			t.Fatalf("%s: expected AlbumCount=3, got %+v", ep, a)
		}
	}

	// /MusicGenres serves MusicGenre items instead of 404.
	genres := call("/MusicGenres?ParentId=" + musicGUID)
	gItems := genres["Items"].([]any)
	if len(gItems) != 1 || gItems[0].(map[string]any)["Type"] != "MusicGenre" || gItems[0].(map[string]any)["Name"] != "Punk Rock" {
		t.Fatalf("/MusicGenres unexpected response: %+v", genres)
	}

	// Album items include AlbumArtist / AlbumArtists / Artists / ArtistItems matching artistGUID,
	// and AlbumArtistIds resolves to Filter.Person.
	albums := call("/Users/" + uid + "/Items?ParentId=" + musicGUID + "&IncludeItemTypes=MusicAlbum&AlbumArtistIds=" + artistGUID + "&Recursive=true")
	if seenPersonFilter != artistPID {
		t.Fatalf("expected AlbumArtistIds to resolve Filter.Person=%d, got %d", artistPID, seenPersonFilter)
	}
	albumDTO := albums["Items"].([]any)[0].(map[string]any)
	if albumDTO["AlbumArtist"] != "Bracket" {
		t.Fatalf("expected AlbumArtist=Bracket, got %+v", albumDTO)
	}
	aa := albumDTO["AlbumArtists"].([]any)
	if len(aa) != 1 || aa[0].(map[string]any)["Id"] != artistGUID {
		t.Fatalf("expected AlbumArtists[0].Id=%s, got %+v", artistGUID, aa)
	}

	// HomeSections includes the music view in LatestMedia.
	sections := call("/Users/" + uid + "/HomeSections")
	foundLatest := false
	for _, raw := range sections["Items"].([]any) {
		sec := raw.(map[string]any)
		if sec["SectionType"] == "LatestMedia" && sec["CollectionId"] == musicGUID {
			foundLatest = true
		}
	}
	if !foundLatest {
		t.Fatalf("expected LatestMedia HomeSection for music library %s, got %+v", musicGUID, sections["Items"])
	}
}

func TestJellyfinMusicArtistItemsParentIndexSortAndNoTrickplay(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	artistKey := frontend.RatingKey(1<<62 | 10)
	albumKey := frontend.RatingKey(1<<62 | 11)
	trackWithParent := frontend.RatingKey(1<<62 | 12)
	trackNoArtist := frontend.RatingKey(1<<62 | 13)
	artistGUID := ids.RatingKeyGUID(artistKey)
	albumGUID := ids.RatingKeyGUID(albumKey)
	trackGUID := ids.RatingKeyGUID(trackWithParent)

	var libraryCalls atomic.Int32
	var seenSort frontend.SortOrder
	exp := musicTestExperience{
		Experience: frontendtest.Experience{
			LibraryItemsFunc: func(context.Context, *frontend.Viewer, frontend.Device, string, frontend.ItemQuery) (frontend.Page, error) {
				libraryCalls.Add(1)
				return frontend.Page{}, nil
			},
			ChildrenFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, parent frontend.RatingKey, q frontend.ItemQuery) (frontend.Page, error) {
				seenSort = q.Sort
				if parent != albumKey {
					return frontend.Page{}, nil
				}
				return frontend.Page{
					Items: []frontend.Title{
						{
							Key:          trackWithParent,
							Type:         frontend.TypeMovie,
							MediaClass:   frontend.MediaClassMusic,
							MBItemType:   "Audio",
							Title:        "Hunter",
							ParentKey:    albumKey,
							ParentTitle:  "Homogenic",
							ItemIndex:    1,
							MediaParents: []frontend.TitleParent{{Key: albumKey, Title: "Homogenic", Role: frontend.MediaRoleAlbum}, {Key: artistKey, Title: "Björk", Role: frontend.MediaRoleArtist}},
						},
						{
							Key:         trackNoArtist,
							Type:        frontend.TypeMovie,
							MediaClass:  frontend.MediaClassMusic,
							MBItemType:  "Audio",
							Title:       "Untitled",
							ParentKey:   albumKey,
							ParentTitle: "Homogenic",
							Disc:        2,
							ItemIndex:   3,
						},
					},
					Total: 2,
				}, nil
			},
			ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
				if req.Key == trackWithParent {
					return frontend.ItemDetail{
						Title: frontend.Title{
							Key:          trackWithParent,
							Type:         frontend.TypeMovie,
							MediaClass:   frontend.MediaClassMusic,
							MBItemType:   "Audio",
							Title:        "Hunter",
							ParentKey:    albumKey,
							ParentTitle:  "Homogenic",
							ItemIndex:    1,
							MediaParents: []frontend.TitleParent{{Key: albumKey, Title: "Homogenic", Role: frontend.MediaRoleAlbum}, {Key: artistKey, Title: "Björk", Role: frontend.MediaRoleArtist}},
						},
					}, nil
				}
				return frontend.ItemDetail{}, frontend.ErrNotFound
			},
		},
	}

	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{
		Identity:   frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }},
		Experience: exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	uid, _ := ids.GUID(ids.User, 1)
	call := func(path string) map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/jellyfin"+path, nil)
		r.Header.Set("X-Emby-Token", "ok")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s decode: %v", path, err)
		}
		return out
	}

	// 1. Album children query with SortBy=ParentIndexNumber,IndexNumber,SortName maps to SortIndex,
	//    always emits non-nil ArtistItems/AlbumArtists (inheriting MediaParents artist), and sets ParentIndexNumber.
	tracks := call("/Users/" + uid + "/Items?ParentId=" + albumGUID + "&SortBy=ParentIndexNumber,IndexNumber,SortName")
	if seenSort != frontend.SortIndex {
		t.Fatalf("SortBy=ParentIndexNumber,IndexNumber,SortName parsed as %q, want %q", seenSort, frontend.SortIndex)
	}
	list := tracks["Items"].([]any)
	t1 := list[0].(map[string]any)
	if t1["AlbumArtist"] != "Björk" || t1["ParentIndexNumber"].(float64) != 1 || t1["IndexNumber"].(float64) != 1 {
		t.Fatalf("track1 missing inherited artist or disc/track index: %+v", t1)
	}
	if ai, ok := t1["ArtistItems"].([]any); !ok || len(ai) != 1 || ai[0].(map[string]any)["Id"] != artistGUID {
		t.Fatalf("track1 ArtistItems = %#v, want [%s]", t1["ArtistItems"], artistGUID)
	}
	t2 := list[1].(map[string]any)
	if _, ok := t2["ArtistItems"].([]any); !ok {
		t.Fatalf("artistless track must emit empty ArtistItems array, got %#v", t2["ArtistItems"])
	}
	if _, ok := t2["AlbumArtists"].([]any); !ok {
		t.Fatalf("artistless track must emit empty AlbumArtists array, got %#v", t2["AlbumArtists"])
	}
	if t2["ParentIndexNumber"].(float64) != 2 {
		t.Fatalf("track2 ParentIndexNumber = %v, want 2", t2["ParentIndexNumber"])
	}

	// 2. Multi-token SortBy starting with Album still recognizes ParentIndexNumber.
	_ = call("/Users/" + uid + "/Items?ParentId=" + albumGUID + "&SortBy=Album,ParentIndexNumber,IndexNumber,SortName")
	if seenSort != frontend.SortIndex {
		t.Fatalf("SortBy=Album,ParentIndexNumber,... parsed as %q, want %q", seenSort, frontend.SortIndex)
	}

	// 3. Explicit empty AlbumArtistIds= returns empty page without scanning LibraryItems.
	empty := call("/Users/" + uid + "/Items?AlbumArtistIds=")
	if empty["TotalRecordCount"].(float64) != 0 || libraryCalls.Load() != 0 {
		t.Fatalf("empty AlbumArtistIds should return empty without scanning LibraryItems: resp=%+v calls=%d", empty, libraryCalls.Load())
	}

	// 4. Audio detail omits Trickplay and emits ArtistItems/AlbumArtists.
	detail := call("/Users/" + uid + "/Items/" + trackGUID)
	if _, hasTrickplay := detail["Trickplay"]; hasTrickplay {
		t.Fatalf("Audio detail should not emit Trickplay: %+v", detail["Trickplay"])
	}
	if _, ok := detail["AlbumArtists"].([]any); !ok {
		t.Fatalf("Audio detail missing AlbumArtists array: %+v", detail)
	}
}
