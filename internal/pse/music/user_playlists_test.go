package music

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

// trackSource renvoie un titre fictif pour chaque identifiant.
type trackSource struct{ Source }

func (trackSource) Artist(context.Context, int64) (Artist, []Album, error) {
	return Artist{}, nil, ErrNotFound
}

func (trackSource) Track(_ context.Context, id int64) (Track, error) {
	return Track{ID: id, Title: "Titre " + strconv.FormatInt(id, 10), Duration: 100, Artist: Artist{ID: 1, Name: "Artiste"}}, nil
}

func TestPersonalAndSmartPlaylists(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	viewer := int64(7)
	lib := NewLibrary(db)
	s := NewServer(Options{Source: trackSource{}, Library: lib, Playlists: &Playlists{DB: db},
		Auth: func(_ context.Context, u, _ string) (int64, error) {
			if u == "b" {
				return 8, nil
			}
			return viewer, nil
		}})
	sub := func(user, method, extra string) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", "/rest/"+method+"?u="+user+"&p=x&f=json&v=1.16.1&c=t"+extra, nil))
		var v map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatal(rec.Body.String())
		}
		return v["subsonic-response"].(map[string]any)
	}
	tr := func(id int) string { return "&songId=" + trackID(int64(id)) }

	// Création avec trois titres.
	r := sub("a", "createPlaylist", "&name=Soirée"+tr(1)+tr(2)+tr(3))
	if r["status"] != "ok" {
		t.Fatalf("création : %v", r)
	}
	pl := r["playlist"].(map[string]any)
	id := pl["id"].(string)
	if pl["readonly"] != false || pl["songCount"].(float64) != 3 {
		t.Fatalf("playlist : %v", pl)
	}
	// Renommer, retirer le 2e titre, ajouter le 4e.
	if r := sub("a", "updatePlaylist", "&playlistId="+id+"&name=Apéro&songIndexToRemove=1&songIdToAdd="+trackID(4)); r["status"] != "ok" {
		t.Fatalf("modification : %v", r)
	}
	got := sub("a", "getPlaylist", "&id="+id)["playlist"].(map[string]any)
	titles := []string{}
	for _, e := range got["entry"].([]any) {
		titles = append(titles, e.(map[string]any)["title"].(string))
	}
	if got["name"] != "Apéro" || strings.Join(titles, ",") != "Titre 1,Titre 3,Titre 4" {
		t.Fatalf("après modification : %v %v", got["name"], titles)
	}
	// Réordonner : createPlaylist avec playlistId remplace la liste.
	if r := sub("a", "createPlaylist", "&playlistId="+id+tr(4)+tr(1)); r["status"] != "ok" {
		t.Fatalf("réordonnancement : %v", r)
	}
	if n := sub("a", "getPlaylist", "&id="+id)["playlist"].(map[string]any)["songCount"].(float64); n != 2 {
		t.Fatalf("%v titres après réordonnancement", n)
	}
	// Un autre spectateur ne la voit ni ne la supprime.
	if r := sub("b", "getPlaylist", "&id="+id); r["status"] != "failed" {
		t.Fatal("playlist visible d'un autre spectateur")
	}
	if r := sub("b", "deletePlaylist", "&id="+id); r["status"] != "failed" {
		t.Fatal("playlist supprimée par un autre spectateur")
	}

	// Playlists automatiques : favoris et écoutes.
	for i := int64(1); i <= 3; i++ {
		tk, _ := trackSource{}.Track(ctx, i)
		if err := lib.StarTrack(ctx, viewer, tk, true); err != nil {
			t.Fatal(err)
		}
	}
	t2, _ := trackSource{}.Track(ctx, 2)
	for range 3 {
		if err := lib.Played(ctx, viewer, t2); err != nil {
			t.Fatal(err)
		}
	}
	names := map[string]float64{}
	var smartID string
	for _, p := range sub("a", "getPlaylists", "")["playlists"].(map[string]any)["playlist"].([]any) {
		m := p.(map[string]any)
		names[m["name"].(string)] = m["songCount"].(float64)
		if m["name"] == "Favoris peu écoutés" {
			smartID = m["id"].(string)
		}
	}
	if names["Apéro"] != 2 || names["Titres favoris"] != 3 || names["Les plus écoutés"] != 1 || names["Favoris peu écoutés"] != 2 {
		t.Fatalf("playlists : %v", names)
	}
	if r := sub("a", "deletePlaylist", "&id="+smartID); r["status"] != "failed" {
		t.Fatal("playlist automatique supprimée")
	}
	if r := sub("a", "deletePlaylist", "&id="+id); r["status"] != "ok" {
		t.Fatalf("suppression : %v", r)
	}
}
