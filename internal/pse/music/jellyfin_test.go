package music

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestJellyfinGUID(t *testing.T) {
	g := JellyfinGUID(guidAlbum, 302127)
	if len(g) != 32 {
		t.Fatalf("GUID %q", g)
	}
	dashed := g[:8] + "-" + g[8:12] + "-" + g[12:16] + "-" + g[16:20] + "-" + g[20:]
	if k, id, ok := ParseJellyfinGUID(strings.ToUpper(dashed)); !ok || k != guidAlbum || id != 302127 {
		t.Fatalf("décodage %v %v %v", k, id, ok)
	}
	for _, bad := range []string{"", "0000000000000001000000000000002a", musicGUIDPrefix + "09" + "0000000000000001", musicGUIDPrefix + "01" + "0000000000000000"} {
		if IsJellyfinGUID(bad) {
			t.Fatalf("%q ne doit pas être un GUID musique", bad)
		}
	}
}

func TestJellyfinMusicLibrary(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dz := fakeDeezer(t)
	const audio = "fLaC-flux-jellyfin"
	addon, _ := fakeEclipse(t, audio)
	src := NewDeezerSource(NewEclipse([]string{addon.URL + "/u/tok/manifest.json"}, addon.Client()), dz.Client())
	src.API = dz.URL
	src.Images = dz.URL + "/images"
	s := NewServer(Options{Source: src, Library: NewLibrary(db), Media: dz.Client()})
	j := Jellyfin{ServerID: "srv", ViewID: "view0000000000000000000000000001", Folder: func() map[string]any {
		return map[string]any{"Id": "view0000000000000000000000000001", "CollectionType": "music"}
	}}
	const viewer = 7
	call := func(method, target, body string) (*httptest.ResponseRecorder, bool) {
		var rd *strings.Reader
		if body != "" {
			rd = strings.NewReader(body)
		} else {
			rd = strings.NewReader("")
		}
		rec := httptest.NewRecorder()
		ok := s.ServeJellyfin(rec, httptest.NewRequest(method, target, rd), viewer, j)
		return rec, ok
	}
	items := func(target string) []map[string]any {
		t.Helper()
		rec, ok := call("GET", target, "")
		if !ok || rec.Code != 200 {
			t.Fatalf("%s : ok=%v code=%d %s", target, ok, rec.Code, rec.Body.String())
		}
		var res struct {
			Items            []map[string]any
			TotalRecordCount int
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("%s : %v", target, err)
		}
		return res.Items
	}
	album := JellyfinGUID(guidAlbum, 302127)
	artist := JellyfinGUID(guidArtist, 27)
	track := JellyfinGUID(guidTrack, 3135553)

	// Bibliothèque vide : le classement la remplit.
	got := items("/Users/u1/Items?ParentId=" + j.ViewID + "&IncludeItemTypes=MusicAlbum&Recursive=true")
	if len(got) != 1 || got[0]["Id"] != album || got[0]["Type"] != "MusicAlbum" || got[0]["AlbumArtist"] != "Daft Punk" {
		t.Fatalf("albums : %v", got)
	}
	if tags := got[0]["ImageTags"].(map[string]any); tags["Primary"] != "c"+dzMD5 {
		t.Fatalf("ImageTags : %v", tags)
	}
	if got := items("/Artists/AlbumArtists?ParentId=" + j.ViewID); len(got) != 1 || got[0]["Id"] != artist {
		t.Fatalf("artistes : %v", got)
	}
	tracks := items("/Users/u1/Items?ParentId=" + album + "&SortBy=ParentIndexNumber,IndexNumber")
	if len(tracks) != 2 || tracks[0]["Id"] != track || tracks[1]["Name"] != "Aerodynamic" || tracks[0]["MediaType"] != "Audio" {
		t.Fatalf("titres : %v", tracks)
	}
	if got := items("/Items?ArtistIds=" + artist + "&IncludeItemTypes=MusicAlbum&Recursive=true"); len(got) != 1 {
		t.Fatalf("albums de l'artiste : %v", got)
	}
	if got := items("/Items?searchTerm=one+more&IncludeItemTypes=Audio&Recursive=true"); len(got) != 1 || got[0]["Name"] != "One More Time" {
		t.Fatalf("recherche : %v", got)
	}

	rec, _ := call("GET", "/Users/u1/Items/"+album, "")
	if !strings.Contains(rec.Body.String(), `"Name":"Discovery"`) {
		t.Fatalf("album : %s", rec.Body.String())
	}
	rec, _ = call("GET", "/Items/"+j.ViewID, "")
	if !strings.Contains(rec.Body.String(), `"CollectionType":"music"`) {
		t.Fatalf("bibliothèque : %s", rec.Body.String())
	}
	rec, _ = call("POST", "/Items/"+track+"/PlaybackInfo", "{}")
	if !strings.Contains(rec.Body.String(), `"Container":"flac"`) || !strings.Contains(rec.Body.String(), `"SupportsDirectPlay":true`) {
		t.Fatalf("PlaybackInfo : %s", rec.Body.String())
	}
	rec, ok := call("GET", "/Audio/"+track+"/stream.flac?static=true", "")
	if !ok || rec.Code != 200 || rec.Body.String() != audio || rec.Header().Get("Content-Type") != "audio/flac" {
		t.Fatalf("flux : %v %d %q %q", ok, rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}
	rec = httptest.NewRecorder()
	if !s.ServeJellyfin(rec, httptest.NewRequest("GET", "/Items/"+album+"/Images/Primary?tag=c"+dzMD5+"&maxWidth=300", nil), 0, j) || rec.Body.String() != "jpeg-pochette" {
		t.Fatalf("image : %d %q", rec.Code, rec.Body.String())
	}

	// Favori, puis écoute signalée par le client.
	rec, _ = call("POST", "/Users/u1/FavoriteItems/"+album, "")
	if !strings.Contains(rec.Body.String(), `"IsFavorite":true`) {
		t.Fatalf("favori : %s", rec.Body.String())
	}
	if got := items("/Users/u1/Items?ParentId=" + j.ViewID + "&IncludeItemTypes=MusicAlbum&Filters=IsFavorite"); len(got) != 1 {
		t.Fatalf("favoris : %v", got)
	}
	rec, ok = call("POST", "/Sessions/Playing/Stopped", `{"ItemId":"`+track+`","PositionTicks":3000000000}`)
	if !ok || rec.Code != http.StatusNoContent {
		t.Fatalf("Stopped : %v %d", ok, rec.Code)
	}
	if it, found, _ := s.o.Library.Get(context.Background(), viewer, KindTrack, 3135553); !found || it.PlayCount != 1 {
		t.Fatalf("écoute non enregistrée : %+v %v", it, found)
	}

	// Ce qui ne concerne pas la musique reste à Ether, corps intact.
	for _, target := range []string{"/Items?IncludeItemTypes=Movie", "/Users/u1/Items?ParentId=0000000000000004000000000000000a", "/Users/u1/Views"} {
		if _, ok := call("GET", target, ""); ok {
			t.Fatalf("%s ne doit pas être traité", target)
		}
	}
	req := httptest.NewRequest("POST", "/Sessions/Playing", strings.NewReader(`{"ItemId":"0000000000000001000000000000002a"}`))
	if s.ServeJellyfin(httptest.NewRecorder(), req, viewer, j) {
		t.Fatal("un rapport de lecture vidéo ne doit pas être traité")
	}
	var rest map[string]string
	if err := json.NewDecoder(req.Body).Decode(&rest); err != nil || rest["ItemId"] == "" {
		t.Fatalf("corps non rendu à Ether : %v %v", rest, err)
	}
	if s.ServeJellyfin(httptest.NewRecorder(), httptest.NewRequest("GET", "/Users/u1/Items?ParentId="+j.ViewID, nil), 0, j) {
		t.Fatal("sans spectateur, seules les images sont servies")
	}
}
