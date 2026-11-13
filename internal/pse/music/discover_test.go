package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"github.com/braedonsaunders/ether/internal/store"
)

func fakeDiscoverDeezer(t *testing.T) *httptest.Server {
	t.Helper()
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	tr := func(id int64, title string, artist int64, name string) map[string]any {
		return map[string]any{"id": id, "title": title, "duration": 180, "readable": true,
			"artist": map[string]any{"id": artist, "name": name},
			"album":  map[string]any{"id": id + 1000, "title": "Album " + title, "md5_image": "0123456789abcdef0123456789abcdef"}}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/genre", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Language") != "fr" {
			t.Errorf("genres sans langue : %q", r.Header.Get("Accept-Language"))
		}
		reply(w, map[string]any{"data": []any{
			map[string]any{"id": 0, "name": "Tous"}, map[string]any{"id": 116, "name": "Rap/Hip Hop"}, map[string]any{"id": 457, "name": "Livres audio"},
		}})
	})
	mux.HandleFunc("/chart/116/tracks", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"data": []any{tr(500, "Rap du moment", 90, "Rappeur")}})
	})
	mux.HandleFunc("/chart/116/albums", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"data": []any{map[string]any{"id": 600, "title": "Album rap", "md5_image": "0123456789abcdef0123456789abcdef", "artist": map[string]any{"id": 90, "name": "Rappeur"}}}})
	})
	mux.HandleFunc("/chart/116/artists", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"data": []any{map[string]any{"id": 90, "name": "Rappeur"}}})
	})
	mux.HandleFunc("/artist/5/related", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"data": []any{
			map[string]any{"id": 5, "name": "Stromae"}, map[string]any{"id": 77, "name": "Angèle"}, map[string]any{"id": 78, "name": "Orelsan"},
		}})
	})
	mux.HandleFunc("/artist/77/top", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"data": []any{tr(771, "Balance ton quoi", 77, "Angèle"), tr(772, "Tout oublier", 77, "Angèle")}})
	})
	mux.HandleFunc("/artist/78/top", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"data": []any{tr(781, "Basique", 78, "Orelsan")}})
	})
	mux.HandleFunc("/artist/5/top", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"data": []any{tr(1, "Ta fête", 5, "Stromae")}})
	})
	mux.HandleFunc("/artist/5/radio", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"data": []any{tr(781, "Basique", 78, "Orelsan")}})
	})
	mux.HandleFunc("/search/artist", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"data": []any{map[string]any{"id": 6, "name": "Stromae Tribute"}, map[string]any{"id": 5, "name": "Stromae"}}})
	})
	mux.HandleFunc("/playlist/1109890291/tracks", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"data": []any{tr(900, "Numéro un", 91, "Star")}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestParseCharts(t *testing.T) {
	got := ParseCharts("fr, gb,US,xx,FR")
	if len(got) != 3 || got[0].Code != "FR" || got[1].Code != "UK" || got[2].Code != "US" {
		t.Fatalf("%+v", got)
	}
}

func TestDiscovery(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	srv := fakeDiscoverDeezer(t)
	dz := NewDeezerSource(nil, srv.Client())
	dz.API = srv.URL
	pls := &Playlists{DB: db}
	sync := &PlaylistSync{Store: pls, Deezer: dz, HTTP: srv.Client(), Pace: 1}
	const viewer = 7
	stromae := Artist{ID: 5, Name: "Stromae"}
	if err := pls.replace(ctx, viewer, ServiceDeezer, []Playlist{{Ref: "1", Name: "Mix · Deezer", Tracks: []Track{{ID: 1, Title: "Ta fête", Artist: stromae}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO pse_music_plays(viewer_id,track_id,title,artist,played_at) VALUES(?,?,?,?,strftime('%s','now'))`, viewer, 1, "Ta fête", "Stromae"); err != nil {
		t.Fatal(err)
	}

	opt := SystemOptions{Charts: ParseCharts("FR"), Discover: true}
	if err := sync.SyncSystem(ctx, viewer, opt); err != nil {
		t.Fatal(err)
	}
	lists, _ := pls.List(ctx, viewer)
	if len(lists) != 3 || lists[0].Source != SourceDiscover || lists[2].Name != "Top France · Deezer" {
		t.Fatalf("ordre des playlists : %+v", lists)
	}
	var titles []string
	for _, tr := range lists[0].Tracks {
		titles = append(titles, tr.Title)
	}
	// Premier titre de chaque artiste proposé, puis les suivants ; jamais
	// d'artiste déjà connu.
	if strings.Join(titles, "|") != "Balance ton quoi|Basique|Tout oublier" {
		t.Fatalf("Découvertes : %v", titles)
	}
	if sync.systemDue(ctx, viewer, opt, SystemEvery) {
		t.Fatal("régénération trop tôt")
	}
	if !sync.systemDue(ctx, viewer, SystemOptions{Charts: ParseCharts("FR,US"), Discover: true}, SystemEvery) {
		t.Fatal("réglage modifié sans régénération")
	}
	// Désactiver retire les playlists système.
	if err := sync.SyncSystem(ctx, viewer, SystemOptions{}); err != nil {
		t.Fatal(err)
	}
	if lists, _ := pls.List(ctx, viewer); len(lists) != 1 {
		t.Fatalf("après désactivation : %+v", lists)
	}
	_ = sync.SyncSystem(ctx, viewer, opt)

	s := NewServer(Options{Source: dz, Library: NewLibrary(db), Playlists: pls,
		Auth: func(context.Context, string, string) (int64, error) { return viewer, nil }})
	sub := func(method, extra string, want ...string) {
		t.Helper()
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", "/rest/"+method+"?u=a&p=b&f=json&v=1.16.1&c=t"+extra, nil))
		for _, w := range want {
			if !strings.Contains(rec.Body.String(), w) {
				t.Fatalf("%s sans %q : %s", method, w, rec.Body.String())
			}
		}
	}
	sub("getGenres", "", `"value":"Rap/Hip Hop"`)
	sub("getSongsByGenre", "&genre=rap/hip%20hop", `"title":"Rap du moment"`)
	sub("getAlbumList2", "&type=byGenre&genre=Rap/Hip%20Hop", `"name":"Album rap"`)
	sub("getSimilarSongs2", "&id=ar-5", `"title":"Basique"`)
	sub("getArtistInfo2", "&id=ar-5", `"similarArtist":[`, `"name":"Angèle"`)
	sub("getTopSongs", "&artist=Stromae", `"title":"Ta fête"`)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/rest/getGenres?u=a&p=b&v=1.16.1&c=t", nil))
	if !strings.Contains(rec.Body.String(), `>Rap/Hip Hop</genre>`) || strings.Contains(rec.Body.String(), "Livres audio") {
		t.Fatalf("getGenres XML : %s", rec.Body.String())
	}

	// Jellyfin
	j := frontend.PSEMusicJellyfin{ServerID: "srv", ViewID: "view"}
	jf := func(target string, want ...string) {
		t.Helper()
		rec := httptest.NewRecorder()
		if !s.ServeJellyfin(rec, httptest.NewRequest("GET", target, nil), viewer, j) {
			t.Fatalf("%s non servi", target)
		}
		for _, w := range want {
			if !strings.Contains(rec.Body.String(), w) {
				t.Fatalf("%s sans %q : %s", target, w, rec.Body.String())
			}
		}
	}
	genre := JellyfinGUID(guidGenre, 116)
	jf("/MusicGenres?ParentId=view", `"Type":"MusicGenre"`, genre)
	jf("/Users/u/Items?ParentId=view&GenreIds="+genre+"&IncludeItemTypes=MusicAlbum", "Album rap")
	jf("/Users/u/Items?ParentId="+genre+"&IncludeItemTypes=Audio", "Rap du moment")
	jf("/Items/"+JellyfinGUID(guidArtist, 5)+"/InstantMix", "Basique")
	jf("/Artists/"+JellyfinGUID(guidArtist, 5)+"/Similar", "Angèle")
	jf("/Songs/" + JellyfinGUID(guidTrack, 1) + "/InstantMix")

	// Plex
	env := frontend.PSEMusicPlex{Write: func(w http.ResponseWriter, _ *http.Request, v any) {
		b, err := model.Marshal(v, model.XML)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}}
	plex := func(target string, want ...string) {
		t.Helper()
		rec := httptest.NewRecorder()
		if !s.ServePlex(rec, httptest.NewRequest("GET", target, nil), viewer, env) || rec.Code != 200 {
			t.Fatalf("%s : code %d", target, rec.Code)
		}
		for _, w := range want {
			if !strings.Contains(rec.Body.String(), w) {
				t.Fatalf("%s sans %q : %s", target, w, rec.Body.String())
			}
		}
	}
	sec := "/library/sections/5053"
	plex(sec, `title="Par style"`)
	plex(sec+"/genre", `title="Rap/Hip Hop"`)
	plex(sec+"/all?type=9&genre=116", `title="Album rap"`)
	plex("/hubs/sections/5053", `title="Découvertes · Ether-Remix"`, `title="Top France · Deezer"`)
	plex("/hubs/metadata/"+psemKey(psemArtist, 5)+"/related", `title="Artistes similaires"`, `title="Angèle"`)
}
