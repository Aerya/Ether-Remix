package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type themeCache struct{ scheduled, served int }

func (*themeCache) ThemeEnabled() bool { return true }
func (c *themeCache) Theme(_ frontend.Title, schedule bool) bool {
	if schedule {
		c.scheduled++
	}
	return true
}
func (c *themeCache) ServeTheme(w http.ResponseWriter, r *http.Request, _ frontend.Title) {
	c.served++
	w.WriteHeader(206)
}

func TestThemeMediaCacheBoundary(t *testing.T) {
	key := core.ShowKey(42)
	for _, front := range []frontend.Frontend{Jellyfin{MachineID: "test"}, Emby{MachineID: "test"}} {
		cache := &themeCache{}
		denied := false
		inst, err := front.Mount(context.Background(), frontend.Host{Themes: cache,
			Identity: frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return &frontend.Viewer{ID: 1}, nil }},
			ThemeTitle: func(context.Context, *frontend.Viewer, frontend.RatingKey) (*frontend.Title, error) {
				if denied {
					return nil, frontend.ErrNotFound
				}
				return &frontend.Title{Key: key, Title: "Example"}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		request := func(path string) *httptest.ResponseRecorder {
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("X-Emby-Token", "token")
			r.Header.Set("X-Emby-Device-Id", "tv")
			w := httptest.NewRecorder()
			inst.Handler().ServeHTTP(w, r)
			return w
		}
		w := request("/Items/" + ids.RatingKeyGUID(key) + "/ThemeMedia")
		if w.Code != 200 {
			t.Fatalf("themes %d %s", w.Code, w.Body)
		}
		var wire struct {
			Songs struct {
				Items []struct {
					ID string `json:"Id"`
				}
			} `json:"ThemeSongsResult"`
			Videos struct{ Items []any } `json:"ThemeVideosResult"`
		}
		if err = json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		if len(wire.Songs.Items) != 1 || len(wire.Videos.Items) != 0 {
			t.Fatal(w.Body.String())
		}
		audioID := wire.Songs.Items[0].ID
		if themeParentID(audioID) != ids.RatingKeyGUID(key) {
			t.Fatal("audio identity lost parent")
		}
		w = request("/Audio/" + audioID + "/stream.m4a?static=true")
		if w.Code != 206 || cache.served != 1 || cache.scheduled != 1 {
			t.Fatalf("stream %d %s", w.Code, w.Body)
		}
		denied = true
		if w = request("/Audio/" + audioID + "/stream.m4a"); w.Code != 404 {
			t.Fatalf("policy %d", w.Code)
		}
		if w = request("/Items/" + ids.RatingKeyGUID(key) + "/ThemeSongs"); w.Code != 404 {
			t.Fatalf("policy list %d", w.Code)
		}
		if strings.Contains(w.Body.String(), "MediaSources") {
			t.Fatal("policy leak")
		}
		inst.Close(context.Background())
	}
}
