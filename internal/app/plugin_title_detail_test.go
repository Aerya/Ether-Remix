package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/plugins"
)

func TestAdminCatalogTitleResolvesPluginItemAndInheritsAncestorArtwork(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "owner-plex-token"); err != nil {
		t.Fatal(err)
	}
	ownerSess, _, err := a.users.IssueOwnerSession(ctx, "owner-plex-token")
	if err != nil {
		t.Fatal(err)
	}

	decl := plugins.MediaKindMeta{
		Key:   "music",
		Media: string(core.MediaClassMusic),
		Levels: []plugins.KindLevel{
			{Key: "artist", Role: string(core.MediaRoleArtist), Container: true},
			{Key: "album", Parent: "artist", Role: string(core.MediaRoleAlbum), Container: true},
			{Key: "track", Parent: "album", Role: string(core.MediaRoleTrack), Playable: true},
		},
	}
	lib, err := a.libraries.UpsertPluginLibrary(ctx, "ether.music", "music", libraries.PluginLibraryDecl{
		Name: "Music",
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.pluginsRT == nil || a.pluginsRT.pluginitems == nil {
		t.Fatal("plugins runtime not initialized")
	}
	if _, err := a.pluginsRT.pluginitems.UpsertKind(ctx, "ether.music", lib.ID, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pluginsRT.pluginitems.UpsertItems(ctx, "ether.music", "music", []core.KindItem{
		{
			ID:    "artist-pf",
			Level: "artist",
			Title: "Pink Floyd",
			Art: []core.KindArtRef{
				{Role: "backdrop", URL: "https://art.example/pink-floyd-backdrop.jpg"},
			},
		},
		{
			ID:       "album-dsotm",
			Level:    "album",
			ParentID: "artist-pf",
			Title:    "The Dark Side of the Moon",
			Year:     1973,
			Art: []core.KindArtRef{
				{Role: "poster", URL: "https://art.example/dsotm-cover.jpg"},
			},
		},
		{
			ID:       "track-speak",
			Level:    "track",
			ParentID: "album-dsotm",
			Title:    "Speak to Me",
			Index:    1,
			Playable: true,
		},
		{
			ID:       "track-own-art",
			Level:    "track",
			ParentID: "album-dsotm",
			Title:    "Breathe",
			Index:    2,
			Playable: true,
			Art: []core.KindArtRef{
				{Role: "poster", URL: "https://art.example/breathe-single.jpg"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	trackItem, _, err := a.pluginsRT.pluginitems.GetByRef(ctx, core.NewItemRef("ether.music", "music", "track-speak"))
	if err != nil {
		t.Fatal(err)
	}
	ownArtItem, _, err := a.pluginsRT.pluginitems.GetByRef(ctx, core.NewItemRef("ether.music", "music", "track-own-art"))
	if err != nil {
		t.Fatal(err)
	}

	adminH := a.buildAdmin()
	getTitle := func(key core.RatingKey) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/ether/api/catalog/title/"+key.String(), nil)
		r.RemoteAddr = "127.0.0.1:4321"
		r.Header.Set("X-Ether-Admin-Token", ownerSess)
		r.Header.Set("X-Ether-Request", "1")
		w := httptest.NewRecorder()
		adminH.ServeHTTP(w, r)
		return w
	}

	// Track with no art of its own resolves 200 and inherits album poster + artist backdrop.
	w := getTitle(trackItem.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("track without own art: status %d body %s", w.Code, w.Body.String())
	}
	var got struct {
		Key      string `json:"key"`
		Title    string `json:"title"`
		Poster   string `json:"poster"`
		Backdrop string `json:"backdrop"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Key != trackItem.Key.String() || got.Title != "Speak to Me" {
		t.Fatalf("unexpected title card: %+v", got)
	}
	if got.Poster != "https://art.example/dsotm-cover.jpg" {
		t.Fatalf("expected inherited album poster, got %q", got.Poster)
	}
	if got.Backdrop != "https://art.example/pink-floyd-backdrop.jpg" {
		t.Fatalf("expected inherited artist backdrop, got %q", got.Backdrop)
	}

	// Track with its own poster keeps its own artwork instead of overwriting with album art.
	w = getTitle(ownArtItem.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("track with own art: status %d body %s", w.Code, w.Body.String())
	}
	got = struct {
		Key      string `json:"key"`
		Title    string `json:"title"`
		Poster   string `json:"poster"`
		Backdrop string `json:"backdrop"`
	}{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Poster != "https://art.example/breathe-single.jpg" {
		t.Fatalf("expected track's own poster, got %q", got.Poster)
	}

	// Unknown plugin rating key still returns 404.
	if w := getTitle(core.PluginKey(999999)); w.Code != http.StatusNotFound {
		t.Fatalf("unknown plugin key: status %d body %s, want 404", w.Code, w.Body.String())
	}
}
