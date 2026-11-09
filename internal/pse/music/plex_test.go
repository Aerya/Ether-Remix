package music

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plex/model"
	"github.com/braedonsaunders/ether/internal/store"
)

// Parcours d'un client Plex (Plexamp) dans la bibliothèque Musique.
func TestPlexMusicLibrary(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	dz := fakeDeezer(t)
	const audio = "fLaC-flux-plex"
	addon, _ := fakeEclipse(t, audio)
	src := NewDeezerSource(NewEclipse([]string{addon.URL + "/u/tok/manifest.json"}, addon.Client()), dz.Client())
	src.API = dz.URL
	src.Images = dz.URL + "/images"
	s := NewServer(Options{Source: src, Library: NewLibrary(db), Media: dz.Client()})
	env := frontend.PSEMusicPlex{Write: func(w http.ResponseWriter, _ *http.Request, v any) {
		b, err := model.Marshal(v, model.XML)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b)
	}}
	const viewer = 7
	call := func(method, target string) (*httptest.ResponseRecorder, bool) {
		rec := httptest.NewRecorder()
		return rec, s.ServePlex(rec, httptest.NewRequest(method, target, nil), viewer, env)
	}
	body := func(method, target string, want ...string) string {
		t.Helper()
		rec, ok := call(method, target)
		if !ok || rec.Code != 200 {
			t.Fatalf("%s %s : ok=%v code=%d", method, target, ok, rec.Code)
		}
		b := rec.Body.String()
		for _, w := range want {
			if !strings.Contains(b, w) {
				t.Fatalf("%s sans %q : %s", target, w, b)
			}
		}
		return b
	}
	artist, album, track := psemKey(psemArtist, 27), psemKey(psemAlbum, 302127), psemKey(psemTrack, 3135553)

	body("GET", "/library/sections/5053/all?type=8", `type="artist"`, `title="Daft Punk"`, `ratingKey="`+artist+`"`)
	body("GET", "/library/sections/5053/all?type=9", `<Directory`, `type="album"`, `parentTitle="Daft Punk"`)
	body("GET", "/hubs/sections/5053", `Albums populaires`, `Titres populaires`, `<Track`)
	body("GET", "/library/metadata/"+artist+"/children", `title="Discovery"`)
	b := body("GET", "/library/metadata/"+album+"/children", `title="One More Time"`, `title="Aerodynamic"`, `/library/parts/`+track+`/0/file.flac`, `grandparentTitle="Daft Punk"`)
	if strings.Count(b, "<Track") != 2 {
		t.Fatalf("titres : %s", b)
	}
	body("GET", "/hubs/search?query=one+more&sectionId=5053", `title="One More Time"`)
	body("GET", "/music/:/transcode/universal/decision?path=%2Flibrary%2Fmetadata%2F"+track, `generalDecisionCode="1000"`, `decision="directplay"`)

	rec, ok := call("GET", "/library/parts/"+track+"/0/file.flac")
	if !ok || rec.Code != 200 || rec.Body.String() != audio {
		t.Fatalf("flux : %v %d %q", ok, rec.Code, rec.Body.String())
	}
	rec, ok = call("GET", "/photo/:/transcode?width=300&url="+url.QueryEscape("/library/metadata/"+album+"/thumb/c"+dzMD5))
	if !ok || rec.Body.String() != "jpeg-pochette" {
		t.Fatalf("pochette : %v %q", ok, rec.Body.String())
	}

	// File de lecture d'un album, en partant du second titre.
	uri := "server://m/com.plexapp.plugins.library/library/metadata/" + album
	b = body("POST", "/playQueues?type=audio&uri="+url.QueryEscape(uri)+"&key="+url.QueryEscape("/library/metadata/"+psemKey(psemTrack, 3135554)),
		`playQueueTotalCount="2"`, `playQueueSelectedItemOffset="1"`)
	i := strings.Index(b, `playQueueID="`) + len(`playQueueID="`)
	pq := b[i : i+10]
	body("GET", "/playQueues/"+pq, `playQueueID="`+pq+`"`, `playQueueItemID=`)
	if _, ok := call("GET", "/playQueues/7999999999"); ok {
		t.Fatal("une file inconnue reste à Ether")
	}

	// Écoute et favori.
	body("GET", "/:/timeline?ratingKey="+track+"&key=%2Flibrary%2Fmetadata%2F"+track+"&state=stopped&time=200000")
	if it, found, _ := s.o.Library.Get(context.Background(), viewer, KindTrack, 3135553); !found || it.PlayCount != 1 {
		t.Fatalf("écoute non enregistrée : %+v", it)
	}
	body("GET", "/:/rate?key="+album+"&identifier=com.plexapp.plugins.library&rating=10")
	body("GET", "/library/sections/5053/all?type=9&album.userRating>>=0", `title="Discovery"`)
	body("GET", "/library/metadata/"+album, `userRating="10"`)

	// Ce qui ne vise pas la musique reste à Ether.
	for _, target := range []string{"/library/sections/1/all", "/library/metadata/1000000000000550", "/hubs/search?query=dune", "/hubs/search?query=dune&searchTypes=movies,music,tv", "/playQueues/12", "/:/timeline?ratingKey=1000000000000550&state=stopped"} {
		if _, ok := call("GET", target); ok {
			t.Fatalf("%s ne doit pas être traité", target)
		}
	}
	if s.ServePlex(httptest.NewRecorder(), httptest.NewRequest("GET", "/library/sections/5053/all", nil), 0, env) {
		t.Fatal("sans spectateur, rien n'est servi")
	}
}
