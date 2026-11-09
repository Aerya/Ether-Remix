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
	"github.com/braedonsaunders/ether/internal/pse/music"
	"github.com/braedonsaunders/ether/internal/store"
)

type pseFakeMusic struct{}

var pseAlbum = music.Album{ID: 302127, Title: "Discovery", Artist: music.Artist{ID: 27, Name: "Daft Punk"}, Tracks: 1}

func (pseFakeMusic) SearchTracks(context.Context, string, int, int) ([]music.Track, error) {
	return nil, nil
}
func (pseFakeMusic) SearchAlbums(context.Context, string, int, int) ([]music.Album, error) {
	return []music.Album{pseAlbum}, nil
}
func (pseFakeMusic) SearchArtists(context.Context, string, int, int) ([]music.Artist, error) {
	return nil, nil
}
func (pseFakeMusic) Album(context.Context, int64) (music.Album, []music.Track, error) {
	return pseAlbum, []music.Track{{ID: 1, Title: "One More Time", Duration: 320, Album: pseAlbum, Artist: pseAlbum.Artist}}, nil
}
func (pseFakeMusic) Artist(context.Context, int64) (music.Artist, []music.Album, error) {
	return pseAlbum.Artist, []music.Album{pseAlbum}, nil
}
func (pseFakeMusic) Track(context.Context, int64) (music.Track, error) {
	return music.Track{}, music.ErrNotFound
}
func (pseFakeMusic) Stream(context.Context, int64, string) (music.Stream, error) {
	return music.Stream{}, music.ErrNotFound
}
func (pseFakeMusic) ImageURL(byte, string, int) string { return "" }
func (pseFakeMusic) ChartAlbums(context.Context, int) ([]music.Album, error) {
	return []music.Album{pseAlbum}, nil
}
func (pseFakeMusic) ChartArtists(context.Context, int) ([]music.Artist, error) { return nil, nil }
func (pseFakeMusic) ChartTracks(context.Context, int) ([]music.Track, error)   { return nil, nil }

// Parcours d'un client Jellyfin dans la bibliothèque Musique de PSE.
func TestPSEMusicLibrary(t *testing.T) {
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	viewer := &frontend.Viewer{ID: 7}
	exp := frontendtest.Experience{
		ViewsFunc: func(context.Context, *core.Viewer) ([]frontend.View, error) {
			return []frontend.View{{ID: "1", Name: "Films", Type: core.TypeMovie}}, nil
		},
	}
	ident := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		if token != "grant" {
			return nil, core.ErrNotFound
		}
		return viewer, nil
	}}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Experience: exp})
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := ids.GUID(ids.User, viewer.ID)
	call := func(path, token string) (int, map[string]any) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		if token != "" {
			r.Header.Set("Authorization", `MediaBrowser Token="`+token+`"`)
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

	if _, doc := call("/Users/"+uid+"/Views", "grant"); len(names(doc)) != 1 {
		t.Fatalf("musique coupée : %v", names(doc))
	}
	frontend.SetPSEMusic(music.NewServer(music.Options{Source: pseFakeMusic{}, Library: music.NewLibrary(db)}))
	t.Cleanup(func() { frontend.SetPSEMusic(nil) })

	_, doc := call("/Users/"+uid+"/Views", "grant")
	got := names(doc)
	if len(got) != 2 || got[1] != "Musique" {
		t.Fatalf("vues : %v", got)
	}
	view := doc["Items"].([]any)[1].(map[string]any)
	viewID := view["Id"].(string)
	if view["CollectionType"] != "music" {
		t.Fatalf("bibliothèque : %v", view)
	}
	if code, doc := call("/Items/"+viewID, "grant"); code != 200 || doc["CollectionType"] != "music" {
		t.Fatalf("/Items/{vue} : %d %v", code, doc)
	}
	code, doc := call("/Users/"+uid+"/Items?ParentId="+viewID+"&IncludeItemTypes=MusicAlbum&Recursive=true", "grant")
	if code != 200 || strings.Join(names(doc), ",") != "Discovery" {
		t.Fatalf("albums : %d %v", code, doc)
	}
	album := doc["Items"].([]any)[0].(map[string]any)["Id"].(string)
	if _, doc := call("/Users/"+uid+"/Items?ParentId="+album, "grant"); strings.Join(names(doc), ",") != "One More Time" {
		t.Fatalf("titres : %v", doc)
	}
	if code, _ := call("/Users/"+uid+"/Items?ParentId="+viewID+"&IncludeItemTypes=MusicAlbum", ""); code != 401 {
		t.Fatalf("sans jeton : %d", code)
	}
	if code, _ := call("/Users/"+uid+"/Items?ParentId="+viewID+"&IncludeItemTypes=MusicAlbum", "faux"); code != 401 {
		t.Fatalf("jeton refusé : %d", code)
	}
}
