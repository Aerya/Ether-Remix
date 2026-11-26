package vela

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestLiveTimeshiftCommands(t *testing.T) {
	live := newMVBrokerLiveFake()
	live.windows["live-1"] = frontend.LiveWindow{LiveEdgeSeconds: 100, TimeshiftMinutes: 60}
	h, _ := newVelaMultiviewHarness(t, live)
	var requestNumber int
	post := func(path string, payload object, wantStatus int) object {
		t.Helper()
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/vela/v0"+path, bytes.NewReader(b))
		requestNumber++
		r.Header.Set("Idempotency-Key", strconv.Itoa(requestNumber))
		r.Header.Set("Authorization", "Bearer tok-1")
		r.Header.Set("X-Vela-Device-Id", "web-1")
		r.Header.Set("X-Vela-Version", "0.2.45")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != wantStatus {
			t.Fatalf("%s %v returned %d, want %d", path, payload["kind"], w.Code, wantStatus)
		}
		var doc object
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	plan := post("/playback", object{"channelId": "news", "client": object{
		"device": object{"id": "web-1", "platform": "web"}, "mse": true,
		"containers": []string{"mp4"}, "videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"},
	}}, 200)
	session := "/sessions/" + plan["sessionId"].(string)
	initialURL := plan["paths"].([]any)[0].(map[string]any)["url"]
	check := func(p object, pos, behind float64) {
		t.Helper()
		if p["positionSeconds"] != pos || p["live"].(map[string]any)["behindLiveSeconds"] != behind {
			t.Fatalf("position/behind = %v/%v, want %v/%v", p["positionSeconds"], p["live"].(map[string]any)["behindLiveSeconds"], pos, behind)
		}
		if p["paths"].([]any)[0].(map[string]any)["url"] != initialURL {
			t.Fatal("timeshift changed the media URL")
		}
	}
	check(plan, 100, 0)
	initial := plan["live"].(map[string]any)
	if initial["edgeAt"] != "2026-11-25T12:00:00Z" || initial["timeshift"].(map[string]any)["maxMinutes"] != float64(60) {
		t.Fatal("initial plan is missing the injected edge time or rewind limit")
	}
	command := func(kind string, position *float64, status int) object {
		t.Helper()
		return post(session+"/commands", object{"kind": kind, "positionSeconds": position,
			"sourceGeneration": plan["sourceGeneration"], "seekGeneration": plan["seekGeneration"],
		}, status)
	}
	plan = command("pause", nil, 200)
	live.mu.Lock()
	live.windows["live-1"] = frontend.LiveWindow{LiveEdgeSeconds: 110, TimeshiftMinutes: 60}
	live.mu.Unlock()
	plan = command("pause", nil, 200)
	check(plan, 100, 10)

	live.mu.Lock()
	live.windows["live-1"] = frontend.LiveWindow{
		Anchor: frontend.LiveAnchor{MediaSequence: 50, WindowStartSeconds: 104}, LiveEdgeSeconds: 114,
	}
	live.mu.Unlock()
	checkError := func(doc object, want string) {
		t.Helper()
		issue := doc["error"].(map[string]any)
		if issue["code"] != want || issue["windowStartSeconds"] != float64(104) || issue["liveEdgeSeconds"] != float64(114) {
			t.Fatalf("unexpected timeshift problem: %v", issue)
		}
	}
	outside := 103.0
	checkError(command("seek", &outside, 409), "timeshift_position_evicted")
	plan = command("resume", nil, 200)
	check(plan, 104, 10)
	if _, ok := plan["live"].(map[string]any)["timeshift"]; ok {
		t.Fatal("rewind limit remains present after playable history was evicted")
	}
	checkError(command("seek", &outside, 409), "timeshift_outside_window")
	target := 108.0
	plan = command("seek", &target, 200)
	check(plan, 108, 6)
	plan = command("goLive", nil, 200)
	check(plan, 114, 0)
	if plan["seekGeneration"] != float64(3) || live.count() != 1 {
		t.Fatal("expected two successful seeks and one live tune")
	}
}
