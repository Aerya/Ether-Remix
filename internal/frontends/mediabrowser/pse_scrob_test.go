package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontend/kit/scrobview"
)

// Rejoue les appels Jellyfin de Scrob (backend/core/jellyfin.py).
func TestPSEScrobLibraries(t *testing.T) {
	viewer := &frontend.Viewer{ID: 7}
	movie, show, ep1, ep2, rated := core.MovieKey(550), core.ShowKey(100), core.EpisodeKey(100, 1, 1), core.EpisodeKey(100, 1, 2), core.MovieKey(551)
	catalogue := map[core.RatingKey]core.Title{
		movie: {Key: movie, Type: core.TypeMovie, Title: "Fjord Harbor", IDs: core.IDs{TMDB: 550}},
		rated: {Key: rated, Type: core.TypeMovie, Title: "Family Fun", IDs: core.IDs{TMDB: 551}},
		show:  {Key: show, Type: core.TypeShow, Title: "Space Show", IDs: core.IDs{TMDB: 100}},
		ep1:   {Key: ep1, Type: core.TypeEpisode, Title: "Pilot", ShowKey: show, Season: 1, Episode: 1},
		ep2:   {Key: ep2, Type: core.TypeEpisode, Title: "Two", ShowKey: show, Season: 1, Episode: 2},
	}
	scrobview.SetProvider(scrobview.Provider{
		Watched: func(_ context.Context, id int64) ([]core.RatingKey, error) {
			if id != 7 {
				t.Errorf("historique d'un autre spectateur : %d", id)
			}
			return []core.RatingKey{movie, rated, ep2, ep1}, nil
		},
		Titles: func(_ context.Context, keys []core.RatingKey) ([]core.Title, error) {
			var out []core.Title
			for _, k := range keys {
				if tt, ok := catalogue[k]; ok {
					out = append(out, tt)
				}
			}
			return out, nil
		},
		Resolve: func(_ context.Context, k core.RatingKey) (*core.Title, error) {
			if tt, ok := catalogue[k]; ok {
				return &tt, nil
			}
			return nil, core.ErrNotFound
		},
	})
	t.Cleanup(func() { scrobview.SetProvider(scrobview.Provider{}) })
	var rating float64
	played := map[core.RatingKey]bool{}
	exp := frontendtest.Experience{
		ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
			if req.Viewer != viewer {
				t.Fatal("wrong viewer for episode lookup")
			}
			if title, ok := catalogue[req.Key]; ok {
				count := 0
				if played[req.Key] {
					count = 1
				}
				return frontend.ItemDetail{Title: title, UserData: core.WatchStatus{Key: req.Key, ViewCount: count, UserRating: rating}}, nil
			}
			return frontend.ItemDetail{}, core.ErrNotFound
		},
		SetPlayedFunc: func(_ context.Context, v *core.Viewer, k core.RatingKey) error {
			if v != viewer {
				t.Fatal("wrong viewer")
			}
			played[k] = true
			return nil
		},
		SetUnplayedFunc: func(_ context.Context, v *core.Viewer, k core.RatingKey) error {
			if v != viewer {
				t.Fatal("wrong viewer")
			}
			played[k] = false
			return nil
		},
		ViewsFunc: func(context.Context, *core.Viewer) ([]frontend.View, error) {
			return []frontend.View{{ID: "1", Name: "Films", Type: core.TypeMovie}}, nil
		},
		SetRatingFunc: func(_ context.Context, v *core.Viewer, k core.RatingKey, r float64) error {
			if v != viewer || k != movie {
				t.Errorf("note sur %v/%v", v, k)
			}
			rating = r
			return nil
		},
		UserDataFunc: func(_ context.Context, _ *core.Viewer, keys []core.RatingKey) (map[core.RatingKey]core.WatchStatus, error) {
			out := map[core.RatingKey]core.WatchStatus{}
			for _, k := range keys {
				count := 0
				if played[k] {
					count = 1
				}
				out[k] = core.WatchStatus{Key: k, ViewCount: count, UserRating: rating}
			}
			return out, nil
		},
	}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Experience: exp})
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := ids.GUID(ids.User, viewer.ID)
	call := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", `MediaBrowser Token="grant"`)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		inst.Handler().ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	names := func(doc map[string]any) []string {
		var out []string
		items, _ := doc["Items"].([]any)
		for _, it := range items {
			m, _ := it.(map[string]any)
			out = append(out, m["Name"].(string))
		}
		return out
	}

	// Scrob's Trigger Library Scan expects a quick successful empty response.
	if code, _ := call("POST", "/Library/Refresh", ""); code != 204 {
		t.Fatalf("refresh: %d", code)
	}
	anonymous := httptest.NewRecorder()
	inst.Handler().ServeHTTP(anonymous, httptest.NewRequest("POST", "/Library/Refresh", nil))
	if anonymous.Code != 401 {
		t.Fatalf("anonymous refresh: %d", anonymous.Code)
	}
	// Désactivé : seules les bibliothèques d'Ether.
	if code, doc := call("GET", "/Users/"+uid+"/Views", ""); code != 200 || len(names(doc)) != 1 {
		t.Fatalf("vues sans PSE : %d %v", code, names(doc))
	}
	t.Cleanup(scrobview.ForceEnable(true))
	code, doc := call("GET", "/Users/"+uid+"/Views", "")
	if got := names(doc); code != 200 || len(got) != 3 || got[1] != "Vus et notés — Films" || got[2] != "Vus et notés — Séries" {
		t.Fatalf("vues : %d %v", code, got)
	}
	moviesID, _ := ids.GUID(ids.View, scrobview.MoviesView)
	showsID, _ := ids.GUID(ids.View, scrobview.ShowsView)

	// get_movies : pagination StartIndex/Limit et TotalRecordCount.
	code, doc = call("GET", "/Users/"+uid+"/Items?ParentId="+moviesID+"&IncludeItemTypes=Movie&Recursive=true&Fields=ProviderIds,MediaStreams,UserData&Limit=1&StartIndex=0", "")
	if code != 200 || doc["TotalRecordCount"] != float64(2) || len(names(doc)) != 1 {
		t.Fatalf("films vus/notés : %d %v", code, doc)
	}
	items := doc["Items"].([]any)
	if pid, _ := items[0].(map[string]any)["ProviderIds"].(map[string]any); pid["Tmdb"] == nil {
		t.Fatalf("ProviderIds absents : %v", items[0])
	}
	// get_shows puis get_episodes.
	if code, doc = call("GET", "/Users/"+uid+"/Items?ParentId="+showsID+"&IncludeItemTypes=Series&Recursive=true&Fields=ProviderIds&Limit=2000", ""); code != 200 || strings.Join(names(doc), ",") != "Space Show" {
		t.Fatalf("séries : %d %v", code, names(doc))
	}
	code, doc = call("GET", "/Users/"+uid+"/Items?ParentId="+showsID+"&IncludeItemTypes=Episode&Recursive=true&ExcludeLocationTypes=Virtual&IsMissing=false&Limit=500&StartIndex=0", "")
	if code != 200 || strings.Join(names(doc), ",") != "Pilot,Two" {
		t.Fatalf("épisodes : %d %v", code, names(doc))
	}
	if sid := doc["Items"].([]any)[0].(map[string]any)["SeriesId"]; sid != ids.RatingKeyGUID(show) {
		t.Fatalf("SeriesId = %v", sid)
	}
	// La bibliothèque elle-même.
	if code, doc = call("GET", "/Users/"+uid+"/Items/"+moviesID, ""); code != 200 || doc["Name"] != "Vus et notés — Films" {
		t.Fatalf("dossier : %d %v", code, doc)
	}
	// find_movie_by_tmdb_id.
	if code, doc = call("GET", "/Items?Recursive=true&IncludeItemTypes=Movie&AnyProviderIdEquals=Tmdb.550&Fields=MediaStreams,Path,ProviderIds&Limit=1", ""); code != 200 || strings.Join(names(doc), ",") != "Fjord Harbor" {
		t.Fatalf("recherche TMDB : %d %v", code, doc)
	}
	if code, doc = call("GET", "/Items?Recursive=true&IncludeItemTypes=Series&AnyProviderIdEquals=Tmdb.100&Limit=1", ""); code != 200 || strings.Join(names(doc), ",") != "Space Show" {
		t.Fatalf("recherche TMDB série : %d %v", code, doc)
	}
	if code, doc = call("GET", "/Items?Recursive=true&IncludeItemTypes=Movie&AnyProviderIdEquals=Tmdb.999&Limit=1", ""); code != 200 || len(names(doc)) != 0 {
		t.Fatalf("TMDB inconnu : %d %v", code, doc)
	}
	// Scrob resolves the exact episode by SeriesId + season/episode, not ParentId.
	code, doc = call("GET", "/Items?SeriesId="+ids.RatingKeyGUID(show)+"&Recursive=true&IncludeItemTypes=Episode&ParentIndexNumber=1&IndexNumber=2&Limit=1", "")
	if code != 200 || strings.Join(names(doc), ",") != "Two" {
		t.Fatalf("exact episode lookup: %d %v", code, doc)
	}
	// Never return a different episode, including invalid selectors or pagination.
	for _, tc := range []struct {
		query string
		code  int
		total float64
	}{
		{"&ParentIndexNumber=1&IndexNumber=99", 200, 0},
		{"&ParentIndexNumber=-1&IndexNumber=2", 400, 0},
		{"&ParentIndexNumber=1000&IndexNumber=2", 400, 0},
		{"&ParentIndexNumber=1&IndexNumber=10000", 400, 0},
		{"&ParentIndexNumber=x&IndexNumber=2", 400, 0},
		{"&ParentIndexNumber=1&IndexNumber=2&StartIndex=1", 200, 1},
	} {
		code, doc = call("GET", "/Items?SeriesId="+ids.RatingKeyGUID(show)+tc.query, "")
		if code != tc.code || (code == 200 && (doc["TotalRecordCount"] != tc.total || len(names(doc)) != 0)) {
			t.Fatalf("selector %s: %d %v", tc.query, code, doc)
		}
	}
	// Scrob's exact lookup -> detail -> watched/unwatched -> readback.
	for _, key := range []core.RatingKey{movie, ep2} {
		id := ids.RatingKeyGUID(key)
		if code, doc = call("GET", "/Items/"+id, ""); code != 200 || doc["Id"] != id {
			t.Fatalf("detail: %d %v", code, doc)
		}
		for _, method := range []string{"POST", "DELETE"} {
			code, doc = call(method, "/Users/"+uid+"/PlayedItems/"+id, "")
			if code != 200 || played[key] != (method == "POST") {
				t.Fatalf("watched %s: %d %v", method, code, doc)
			}
			code, doc = call("GET", "/Items/"+id, "")
			data, _ := doc["UserData"].(map[string]any)
			if code != 200 || data["Played"] != (method == "POST") {
				t.Fatalf("readback: %d %v", code, doc)
			}
		}
	}
	other, _ := ids.GUID(ids.User, 8)
	if code, _ = call("POST", "/Users/"+other+"/PlayedItems/"+ids.RatingKeyGUID(ep1), ""); code != 401 || played[ep1] {
		t.Fatalf("cross-viewer mutation: %d", code)
	}
	// set_rating.
	code, doc = call("POST", "/Users/"+uid+"/Items/"+ids.RatingKeyGUID(movie)+"/UserData", `{"PlayedPercentage":null,"UnplayedItemCount":null,"Rating":8.5}`)
	if code != 200 || rating != 8.5 {
		t.Fatalf("note : %d %v (%v)", code, doc, rating)
	}
	if code, _ = call("POST", "/Users/"+uid+"/Items/"+ids.RatingKeyGUID(movie)+"/UserData", `{"Rating":11}`); code != 400 {
		t.Fatalf("note hors échelle : %d", code)
	}
}
