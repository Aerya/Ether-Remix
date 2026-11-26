package silo

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

func TestCatchUpLibraryPlayback(t *testing.T) {
	live := frontendtest.NewCatchUpLive()
	viewer := &frontend.Viewer{ID: 7, IsOwner: true}
	identity := testDeviceIdentity(frontendtest.Identity{
		ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil },
	}, viewer)
	inst, err := (Frontend{MachineID: "test"}).Mount(t.Context(), frontend.Host{
		Identity: identity, Experience: frontendtest.Experience{}, Sessions: frontendtest.Sessions{},
		Capabilities: frontendtest.Capabilities{}, LiveTV: live,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer grant")
		r.Header.Set("X-Silo-Device-Id", "tv")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		return w
	}
	if list := request(http.MethodGet, "/api/v2/user/libraries", ""); list.Code != 200 || !strings.Contains(list.Body.String(), "Catch-up") {
		t.Fatalf("library list: %d", list.Code)
	}
	type result struct {
		Items []struct {
			ID string `json:"content_id"`
		}
	}
	shows := request(http.MethodGet, "/api/v2/catalog?library_id=catchup", "")
	var listed result
	if shows.Code != 200 || json.Unmarshal(shows.Body.Bytes(), &listed) != nil || len(listed.Items) != 1 {
		t.Fatalf("series list: %d", shows.Code)
	}
	episodes := request(http.MethodGet, "/api/v2/catalog/series/"+listed.Items[0].ID+"/seasons/1/episodes", "")
	if episodes.Code != 200 || json.Unmarshal(episodes.Body.Bytes(), &listed) != nil || len(listed.Items) != 1 {
		t.Fatalf("episode list: %d", episodes.Code)
	}
	detail := request(http.MethodGet, "/api/v2/watch/"+listed.Items[0].ID, "")
	var watch struct {
		Versions []struct {
			FileID string `json:"file_id"`
		}
	}
	if detail.Code != 200 || json.Unmarshal(detail.Body.Bytes(), &watch) != nil || len(watch.Versions) != 1 {
		t.Fatalf("watch detail: %d", detail.Code)
	}
	body, err := json.Marshal(map[string]any{"protocol_version": 3, "installation_id": inst.(*instance).installationID(),
		"playback_attempt_id": "test", "file_id": watch.Versions[0].FileID})
	if err != nil {
		t.Fatal(err)
	}
	start := request(http.MethodPost, "/api/v2/playback/start", string(body))
	var plan struct {
		PlaybackPlan struct{ Stream struct{ URL string } } `json:"playback_plan"`
	}
	if start.Code != 201 || json.Unmarshal(start.Body.Bytes(), &plan) != nil || plan.PlaybackPlan.Stream.URL == "" ||
		live.Opened.ChannelID != live.Item.Programme.ChannelID || live.Opened.ProgrammeID != live.Item.Programme.ID {
		t.Fatalf("playback start: %d", start.Code)
	}
	stream := request(http.MethodGet, plan.PlaybackPlan.Stream.URL, "")
	if stream.Code != 200 || !strings.Contains(stream.Body.String(), "segment/0.ts") || !strings.Contains(stream.Body.String(), "#EXT-X-ENDLIST") {
		t.Fatalf("playback stream: %d", stream.Code)
	}
}
