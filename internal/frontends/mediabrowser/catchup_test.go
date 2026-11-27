package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

func TestCatchUpViewPlayback(t *testing.T) {
	for _, protocol := range []frontend.Protocol{"jellyfin", "emby"} {
		t.Run(string(protocol), func(t *testing.T) {
			live := frontendtest.NewCatchUpLive()
			viewer := &frontend.Viewer{ID: 1, IsOwner: true}
			h, err := mount(t.Context(), frontend.Host{
				Identity:   frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }},
				Experience: frontendtest.Experience{}, Sessions: frontendtest.Sessions{}, Capabilities: frontendtest.Capabilities{}, LiveTV: live,
			}, "test", protocol)
			if err != nil {
				t.Fatal(err)
			}
			request := func(method, path, body string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("X-Emby-Token", "fixture-credential")
				h.Handler().ServeHTTP(w, r)
				return w
			}
			views := request(http.MethodGet, "/UserViews", "")
			if views.Code != 200 || !strings.Contains(views.Body.String(), catchUpViewID()) {
				t.Fatalf("catch-up views: %d", views.Code)
			}
			page := request(http.MethodGet, "/Items?ParentId="+catchUpViewID()+"&IncludeItemTypes=Episode", "")
			var listed struct{ Items []struct{ ID string } }
			if page.Code != 200 || json.Unmarshal(page.Body.Bytes(), &listed) != nil || len(listed.Items) != 1 {
				t.Fatalf("catch-up list: %d", page.Code)
			}
			path := "/Items/" + listed.Items[0].ID + "/PlaybackInfo"
			info := request(http.MethodPost, path, `{"IsPlayback":true}`)
			var played struct {
				MediaSources []struct{ TranscodingURL string }
			}
			if info.Code != 200 || json.Unmarshal(info.Body.Bytes(), &played) != nil || len(played.MediaSources) != 1 ||
				live.Opened.ChannelID != live.Item.Programme.ChannelID || live.Opened.ProgrammeID != live.Item.Programme.ID {
				t.Fatalf("catch-up playback info: %d", info.Code)
			}
			stream := request(http.MethodGet, played.MediaSources[0].TranscodingURL, "")
			if stream.Code != 200 || !strings.Contains(stream.Body.String(), "#EXT-X-ENDLIST") {
				t.Fatalf("catch-up stream: %d", stream.Code)
			}
		})
	}
}
