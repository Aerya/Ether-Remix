package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// TestMediaBrowserVideoChildUnderMusicArtist proves a branching video level
// (MBItemType: "Video") under a MusicArtist renders as a playable Video item
// with MediaType="Video", IsFolder=false, ParentId=artistGUID, and video
// MediaSources (not mislabelled as Audio or Folder).
func TestMediaBrowserVideoChildUnderMusicArtist(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}

	artistKey := core.PluginKey(401)
	albumKey := core.PluginKey(402)
	concertKey := core.PluginKey(403)
	artistGUID := ids.RatingKeyGUID(artistKey)
	concertGUID := ids.RatingKeyGUID(concertKey)

	artist := core.Title{
		Key:        artistKey,
		LibraryID:  77,
		Type:       core.TypeShow,
		MBItemType: "MusicArtist",
		MediaClass: core.MediaClassMusic,
		MediaRole:  core.MediaRoleArtist,
		Title:      "Miles Davis",
	}
	album := core.Title{
		Key:         albumKey,
		LibraryID:   77,
		Type:        core.TypeSeason,
		MBItemType:  "MusicAlbum",
		MediaClass:  core.MediaClassMusic,
		MediaRole:   core.MediaRoleAlbum,
		ParentKey:   artistKey,
		ParentTitle: "Miles Davis",
		Title:       "Kind of Blue",
	}
	concert := core.Title{
		Key:         concertKey,
		LibraryID:   77,
		Type:        core.TypeMovie,
		MBItemType:  "Video",
		MediaClass:  core.MediaClassVideo,
		MediaRole:   "concert",
		ParentKey:   artistKey,
		ParentTitle: "Miles Davis",
		Title:       "Live at Newport 1958",
	}

	exp := &frontendtest.Experience{}
	exp.ViewsFunc = func(context.Context, *core.Viewer) ([]frontend.View, error) {
		return []frontend.View{{
			ID:        "77",
			LibraryID: 77,
			Name:      "Music",
			Type:      core.TypeShow,
			PluginID:  "community.example.music",
			KindKey:   "music",
			Native:    "music",
		}}, nil
	}
	exp.ChildrenFunc = func(_ context.Context, _ *core.Viewer, _ core.Device, parent core.RatingKey, _ frontend.ItemQuery) (core.Page, error) {
		if parent == artistKey {
			return core.Page{Items: []core.Title{album, concert}, Total: 2, TotalExact: true}, nil
		}
		return core.Page{}, nil
	}
	exp.ItemFunc = func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
		switch req.Key {
		case artistKey:
			return frontend.ItemDetail{Title: artist}, nil
		case concertKey:
			canPlay := true
			return frontend.ItemDetail{Title: concert, CanPlay: &canPlay}, nil
		default:
			return frontend.ItemDetail{}, core.ErrNotFound
		}
	}

	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{
		Identity:   ident,
		Experience: exp,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Listing children of MusicArtist includes MusicAlbum (folder) and Video (playable video leaf).
	req := httptest.NewRequest(http.MethodGet, "/Items?ParentId="+artistGUID+"&Fields=ParentId", nil)
	req.Header.Set("X-Emby-Token", "grant")
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/Items?ParentId=artist status = %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Items []map[string]any `json:"Items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("artist children len = %d, want 2: %+v", len(list.Items), list.Items)
	}
	vid := list.Items[1]
	if vid["Type"] != "Video" || vid["MediaType"] != "Video" || vid["IsFolder"] != false || vid["ParentId"] != artistGUID {
		t.Fatalf("video child DTO = %+v, want Type=Video MediaType=Video IsFolder=false ParentId=%s", vid, artistGUID)
	}

	// 2. Direct detail lookup on the Video item preserves Type=Video, MediaType=Video, ParentId=artistGUID.
	userGUID, err := ids.GUID(ids.User, 1)
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/Users/"+userGUID+"/Items/"+concertGUID, nil)
	req.Header.Set("X-Emby-Token", "grant")
	w = httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/Users/1/Items/concert status = %d %s", w.Code, w.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["Type"] != "Video" || detail["MediaType"] != "Video" || detail["IsFolder"] != false || detail["ParentId"] != artistGUID {
		t.Fatalf("concert detail DTO = %+v", detail)
	}
}

// TestMediaBrowserBranchingArtistCountsAndSeparatedLevels verifies:
//   - MusicArtist DTO preserves direct ChildCount separately from RecursiveItemCount;
//   - AlbumArtistIds + IncludeItemTypes=MusicAlbum + Recursive=true returns only MusicAlbum;
//   - IncludeItemTypes=Video returns only Video;
//   - /Shows/{id}/Seasons narrows a branching container to its PrimaryChildLevel.
func TestMediaBrowserBranchingArtistCountsAndSeparatedLevels(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}

	artistKey := core.PluginKey(501)
	albumKey := core.PluginKey(502)
	concertKey := core.PluginKey(503)
	artistGUID := ids.RatingKeyGUID(artistKey)
	musicViewGUID, err := ids.GUID(ids.View, 78)
	if err != nil {
		t.Fatal(err)
	}
	userGUID, err := ids.GUID(ids.User, 1)
	if err != nil {
		t.Fatal(err)
	}

	artist := core.Title{
		Key:               artistKey,
		LibraryID:         78,
		Type:              core.TypeShow,
		MBItemType:        "MusicArtist",
		KindLevel:         "artist",
		PrimaryChildLevel: "album",
		MediaClass:        core.MediaClassMusic,
		MediaRole:         core.MediaRoleArtist,
		Title:             "AC/DC",
		ChildCount:        7,
		LeafCount:         27,
	}
	album := core.Title{
		Key:         albumKey,
		LibraryID:   78,
		Type:        core.TypeSeason,
		MBItemType:  "MusicAlbum",
		KindLevel:   "album",
		MediaClass:  core.MediaClassMusic,
		MediaRole:   core.MediaRoleAlbum,
		ParentKey:   artistKey,
		ParentTitle: "AC/DC",
		Title:       "Back in Black",
	}
	concert := core.Title{
		Key:         concertKey,
		LibraryID:   78,
		Type:        core.TypeMovie,
		MBItemType:  "Video",
		KindLevel:   "concert",
		MediaClass:  core.MediaClassVideo,
		MediaRole:   "concert",
		ParentKey:   artistKey,
		ParentTitle: "AC/DC",
		Title:       "Live at River Plate",
	}

	var lastSeasonQuery frontend.ItemQuery
	exp := &frontendtest.Experience{}
	exp.ViewsFunc = func(context.Context, *core.Viewer) ([]frontend.View, error) {
		return []frontend.View{{
			ID:        "78",
			LibraryID: 78,
			Name:      "Music",
			Type:      core.TypeShow,
			PluginID:  "community.example.music",
			KindKey:   "music",
			Native:    "music",
		}}, nil
	}
	exp.ItemFunc = func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
		if req.Key == artistKey {
			return frontend.ItemDetail{Title: artist}, nil
		}
		return frontend.ItemDetail{}, core.ErrNotFound
	}
	exp.ChildrenFunc = func(_ context.Context, _ *core.Viewer, _ core.Device, parent core.RatingKey, q frontend.ItemQuery) (core.Page, error) {
		if parent != artistKey {
			return core.Page{}, nil
		}
		lastSeasonQuery = q
		var out []core.Title
		for _, it := range []core.Title{album, concert} {
			if q.Level != "" && it.KindLevel != q.Level {
				continue
			}
			if len(q.UnservedTypes) > 0 {
				matched := false
				for _, u := range q.UnservedTypes {
					if u == "musicalbum" && it.MBItemType == "MusicAlbum" {
						matched = true
					}
					if u == "video" && it.MBItemType == "Video" {
						matched = true
					}
				}
				if !matched {
					continue
				}
			}
			out = append(out, it)
		}
		return core.Page{Items: out, Total: len(out), TotalExact: true}, nil
	}

	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{
		Identity:   ident,
		Experience: exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	call := func(path string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Emby-Token", "grant")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d %s", path, w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	// 1. Artist detail keeps ChildCount (direct children=7) distinct from RecursiveItemCount (leaves=27).
	artistDTO := call("/Users/" + userGUID + "/Items/" + artistGUID)
	if artistDTO["ChildCount"] != float64(7) || artistDTO["RecursiveItemCount"] != float64(27) {
		t.Fatalf("artist DTO counts = ChildCount:%v RecursiveItemCount:%v, want 7 / 27", artistDTO["ChildCount"], artistDTO["RecursiveItemCount"])
	}

	// 2. AlbumArtistIds + IncludeItemTypes=MusicAlbum + Recursive=true returns MusicAlbum only.
	albumsDTO := call("/Users/" + userGUID + "/Items?ParentId=" + musicViewGUID + "&IncludeItemTypes=MusicAlbum&AlbumArtistIds=" + artistGUID + "&Recursive=true")
	albumItems, _ := albumsDTO["Items"].([]any)
	if len(albumItems) != 1 || albumItems[0].(map[string]any)["Type"] != "MusicAlbum" {
		t.Fatalf("MusicAlbum query items = %+v, want single MusicAlbum", albumItems)
	}

	// 3. IncludeItemTypes=Video returns Video only.
	videosDTO := call("/Items?ParentId=" + artistGUID + "&IncludeItemTypes=Video")
	videoItems, _ := videosDTO["Items"].([]any)
	if len(videoItems) != 1 || videoItems[0].(map[string]any)["Type"] != "Video" {
		t.Fatalf("Video query items = %+v, want single Video", videoItems)
	}

	// 4. /Shows/{id}/Seasons narrows a branching container to its primary child level only.
	seasonsDTO := call("/Shows/" + artistGUID + "/Seasons")
	seasonItems, _ := seasonsDTO["Items"].([]any)
	if len(seasonItems) != 1 || seasonItems[0].(map[string]any)["Type"] != "MusicAlbum" || lastSeasonQuery.Level != "album" {
		t.Fatalf("/Shows/{id}/Seasons = items:%+v level:%q, want single MusicAlbum with Level=album", seasonItems, lastSeasonQuery.Level)
	}
}
