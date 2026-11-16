package mediabrowser

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoveryViewsLatestAndRestore(t *testing.T) {
	enabled := true
	exp := frontendtest.Experience{ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{Collection: &frontend.Collection{Key: req.Key, NavigationGroup: "studios"}}, nil
	}, ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) { return nil, nil }, CollectionsFunc: func(_ context.Context, v *frontend.Viewer, _ frontend.Device, q frontend.CollectionQuery) (frontend.CollectionPage, error) {
		if q.ExistsOnly {
			return frontend.CollectionPage{}, nil
		}
		if !q.NavigationOnly || (q.NavigationGroup != "" && q.NavigationGroup != "studios") || v.ID != 1 {
			t.Fatalf("unscoped navigation %+v", q)
		}
		return frontend.CollectionPage{Items: []frontend.Collection{{Key: 5, Title: "A24", NavigationGroup: "studios", ChildCount: 1}}, Total: 1}, nil
	}}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return &frontend.Viewer{ID: 1}, nil }}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Experience: exp, Artwork: navigationArtwork{}, ImageGrants: &imageGrantsFake{}, HostConfig: func() config.Config { return config.Config{DiscoveryCards: enabled} }})
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Emby-Token", "grant")
		inst.Handler().ServeHTTP(w, r)
		return w
	}
	w := get("/UserViews")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Studios") {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"CollectionType":"boxsets"`) {
		t.Fatal("boxsets views are excluded from Jellyfin home shelves")
	}
	id := discoveryFolder(0)["Id"].(string)
	for _, path := range []string{"/Items?ParentId=" + id, "/Items/Latest?ParentId=" + id} {
		w = get(path)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"Type":"BoxSet"`) || !strings.Contains(w.Body.String(), "PrimaryImageAspectRatio") || !strings.Contains(w.Body.String(), "scoped-image/poster") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	enabled = false
	w = get("/Items?ParentId=" + id)
	if w.Code != 404 {
		t.Fatal("disabled view remains accessible", w.Code)
	}
	w = get("/UserViews")
	if strings.Contains(w.Body.String(), "Studios") {
		t.Fatal("disabled home remains visible")
	}
}

type navigationArtwork struct{ frontendtest.Artwork }

func (navigationArtwork) List(context.Context, frontend.ArtworkRequest) ([]frontend.Image, bool, error) {
	return []frontend.Image{{Kind: frontend.ImagePoster, Path: "/poster", Width: 600, Height: 337}}, true, nil
}
