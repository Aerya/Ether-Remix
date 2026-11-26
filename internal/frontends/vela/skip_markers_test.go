package vela

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type skipMarkersFake struct {
	key    frontend.RatingKey
	ranges []frontend.MarkerRange
}

func (f skipMarkersFake) CachedMarkers(_ context.Context, key frontend.RatingKey, _ frontend.MediaInfo) ([]frontend.MarkerRange, error) {
	if key != f.key {
		return nil, nil
	}
	return f.ranges, nil
}

// A Vela plan carries the cached intro/credits ranges Plex serves as Marker
// elements, in seconds, with final only on the tail credits range.
func TestVelaPlaybackPlanCarriesSkipMarkers(t *testing.T) {
	key := frontend.EpisodeKey(10, 1, 2)
	sess := &versionsSessionsFake{}
	sess.planFunc = func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error) {
		plan := retryPlayPlan()
		plan.Media.Duration = 50 * time.Minute
		plan.Media.Streams = []frontend.StreamInfo{{Type: "video", Codec: "h264"}}
		return plan, nil
	}
	ranges := []frontend.MarkerRange{
		{Type: "credits", StartMS: 2_940_000, EndMS: 3_000_000, Final: true},
		{Type: "recap", StartMS: 0, EndMS: 30_000},
		{Type: "intro", StartMS: 30_500, EndMS: 92_250},
		{Type: "credits", StartMS: 2_880_000, EndMS: 2_910_000, Final: true},
	}
	exp := frontendtest.Experience{
		ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
			return frontend.ItemDetail{Title: frontend.Title{Key: key, Type: frontend.TypeEpisode, Title: "Half Loop"}}, nil
		},
		UserDataFunc: func(context.Context, *frontend.Viewer, []frontend.RatingKey) (map[frontend.RatingKey]frontend.WatchStatus, error) {
			return map[frontend.RatingKey]frontend.WatchStatus{}, nil
		},
	}
	host := frontend.Host{Identity: identityFake{}, Experience: exp, Sessions: sess, Capabilities: frontendtest.Capabilities{}, SkipMarkers: skipMarkersFake{key: key, ranges: ranges}}
	inst, err := (Frontend{DB: openVelaTestReplayDB(t), ReplayKey: make([]byte, 32)}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inst.Close(context.Background()) })

	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, velaReq("POST", "/vela/v0/playback", `{"idempotencyKey":"play-markers","titleId":"`+ids.RatingKeyGUID(key)+`","positionSeconds":0,"client":{"device":{"id":"dev-1"}}}`))
	if w.Code != 200 {
		t.Fatalf("playback = %d %s", w.Code, w.Body.String())
	}
	var plan struct {
		Markers []map[string]any `json:"markers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"kind": "intro", "startSeconds": 30.5, "endSeconds": 92.25},
		{"kind": "credits", "startSeconds": 2880.0, "endSeconds": 2910.0},
		{"kind": "credits", "startSeconds": 2940.0, "endSeconds": 3000.0, "final": true},
	}
	if !reflect.DeepEqual(plan.Markers, want) {
		t.Fatalf("markers = %v, want %v", plan.Markers, want)
	}
}
