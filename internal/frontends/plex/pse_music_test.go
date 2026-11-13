package plex

import (
	"context"
	"net/http"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

type pseFakeMusic struct{ viewers []int64 }

func (f *pseFakeMusic) ServeJellyfin(http.ResponseWriter, *http.Request, int64, frontend.PSEMusicJellyfin) bool {
	return false
}
func (f *pseFakeMusic) ServePlex(http.ResponseWriter, *http.Request, int64, frontend.PSEMusicPlex) bool {
	return false
}
func (f *pseFakeMusic) PlexSearch(_ context.Context, viewer int64, query string, _ int) ([]any, []any, []any) {
	f.viewers = append(f.viewers, viewer)
	artist := model.Metadata{RatingKey: "50000000000000027", Type: "artist", Title: "Daft Punk"}
	artist.SetElement("Directory")
	album := model.Metadata{RatingKey: "51000000000302127", Type: "album", Title: "Discovery"}
	album.SetElement("Directory")
	track := model.Metadata{RatingKey: "52000000003135553", Type: "track", Title: "One More Time"}
	track.SetElement("Track")
	return []any{artist}, []any{album}, []any{track}
}

func (f *pseFakeMusic) JellyfinPlaylists(context.Context, int64, frontend.PSEMusicJellyfin) []any {
	return nil
}
func (f *pseFakeMusic) PlexPlaylists(context.Context, int64) []any {
	pl := model.Metadata{RatingKey: "53000000000000099", Type: "playlist", Title: "Road trip · Deezer", PlaylistType: "audio"}
	pl.SetElement("Playlist")
	return []any{pl}
}

// La liste des playlists d'Ether reçoit les playlists musicales importées.
func TestPSEMusicPlaylistsListed(t *testing.T) {
	frontend.SetPSEMusic(&pseFakeMusic{})
	t.Cleanup(func() { frontend.SetPSEMusic(nil) })
	f := newFixture()
	defer f.srv.Close()
	items := func(path string) []any {
		mc := decodeJSON(t, f.get(t, path, jsonAccept...))
		list, _ := mc["Metadata"].([]any)
		return list
	}
	found := false
	for _, v := range items("/playlists") {
		if md, _ := v.(map[string]any); md["title"] == "Road trip · Deezer" && md["playlistType"] == "audio" {
			found = true
		}
	}
	if !found {
		t.Fatalf("playlist musicale absente : %+v", items("/playlists"))
	}
	for _, v := range items("/playlists?playlistType=video") {
		if md, _ := v.(map[string]any); md["playlistType"] == "audio" {
			t.Fatal("playlist musicale dans la liste vidéo")
		}
	}
}

// La recherche globale de Plex garde ses 17 hubs et y range la musique.
func TestPSEMusicInGlobalSearch(t *testing.T) {
	fake := &pseFakeMusic{}
	frontend.SetPSEMusic(fake)
	t.Cleanup(func() { frontend.SetPSEMusic(nil) })
	f := newFixture()
	defer f.srv.Close()

	mc := decodeJSON(t, f.get(t, "/library/sections", jsonAccept...))
	found := false
	for _, d := range mc["Directory"].([]any) {
		if dd := d.(map[string]any); dd["key"] == "5053" && dd["type"] == "artist" {
			found = true
		}
	}
	if !found {
		t.Fatalf("section Musique absente : %v", mc["Directory"])
	}

	mc = decodeJSON(t, f.get(t, "/hubs/search?query=daft+punk&limit=10", jsonAccept...))
	hubs := mc["Hub"].([]any)
	if len(hubs) != 17 {
		t.Fatalf("hubs = %d, want 17", len(hubs))
	}
	first := hubs[0].(map[string]any)
	if first["hubIdentifier"] != "artist" || first["size"].(float64) != 1 {
		t.Fatalf("le titre exact doit placer Artists en tête : %v", first)
	}
	for _, h := range hubs {
		hm := h.(map[string]any)
		if hm["hubIdentifier"] == "track" && hm["size"].(float64) != 1 {
			t.Fatalf("hub Tracks : %v", hm)
		}
	}

	mc = decodeJSON(t, f.get(t, "/library/search?query=daft+punk&limit=10", jsonAccept...))
	n := 0
	for _, r := range mc["SearchResult"].([]any) {
		if md, ok := r.(map[string]any)["Metadata"].(map[string]any); ok && md["title"] == "Daft Punk" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("/library/search sans la musique : %v", mc["SearchResult"])
	}
	calls := len(fake.viewers)
	mc = decodeJSON(t, f.get(t, "/library/search?query=daft+punk&searchTypes=movies", jsonAccept...))
	if len(fake.viewers) != calls {
		t.Fatal("searchTypes=movies ne doit pas interroger la musique")
	}
	if len(fake.viewers) == 0 || fake.viewers[0] == 0 {
		t.Fatalf("spectateur transmis : %v", fake.viewers)
	}
}

func TestPSEMusicInMediaProviders(t *testing.T) {
	frontend.SetPSEMusic(&pseFakeMusic{})
	t.Cleanup(func() { frontend.SetPSEMusic(nil) })
	f := newFixture()
	defer f.srv.Close()

	mc := decodeJSON(t, f.get(t, "/media/providers", jsonAccept...))
	prov := mc["MediaProvider"].([]any)[0].(map[string]any)
	if prov["types"] != "video,audio" {
		t.Fatalf("types = %v", prov["types"])
	}
	var dirs []any
	for _, ft := range prov["Feature"].([]any) {
		if fm := ft.(map[string]any); fm["type"] == "content" {
			dirs = fm["Directory"].([]any)
		}
	}
	found := false
	for _, d := range dirs {
		if dd := d.(map[string]any); dd["id"] == "5053" && dd["type"] == "artist" && dd["key"] == "/library/sections/5053" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Musique absente de /media/providers : %v", dirs)
	}

	frontend.SetPSEMusic(nil)
	mc = decodeJSON(t, f.get(t, "/media/providers", jsonAccept...))
	if p := mc["MediaProvider"].([]any)[0].(map[string]any); p["types"] != "video" {
		t.Fatalf("types sans musique = %v", p["types"])
	}
}
