package vela

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func (f *previewBrokerFake) ExtraPlaybackAvailable() bool { return true }
func (f *previewBrokerFake) ExtraPlayback(_ context.Context, req frontend.TrailerPreviewRequest) (frontend.TrailerPreviewResult, error) {
	f.calls++
	return f.result, nil
}

// Cold preparation is retryable under the same mutation key; admission is
// replayed once ready, with independent controls and explicit owner hero policy.
func TestExtraPlaybackContract(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE vela_replays(key TEXT PRIMARY KEY,digest TEXT NOT NULL,response BLOB,expires INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	broker := &previewBrokerFake{result: frontend.TrailerPreviewResult{State: "preparing", RetryAfterSeconds: 1}}
	reports, stops := 0, 0
	broker.previewBaseSessions = frontendtest.Sessions{ReportFunc: func(context.Context, frontend.SessionAccess, string, frontend.Timeline) error { reports++; return nil }, StopFunc: func(context.Context, frontend.SessionAccess, string) error { stops++; return nil }}
	i := &instance{host: frontend.Host{Identity: identityFake{}, Capabilities: frontendtest.Capabilities{}, Sessions: broker}, ctx: context.Background(), mux: http.NewServeMux(), db: db, seal: seal, replays: map[string]*replay{}, sessions: map[string]*playSession{}}
	i.routes()
	request := func(path, dev string, b object) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(b)
		r := httptest.NewRequest("POST", "/vela/v0"+path, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer token")
		r.Header.Set("X-Vela-Device-Id", dev)
		w := httptest.NewRecorder()
		i.serve(w, r)
		return w
	}
	extra := ids.RatingKeyGUID(core.ExtraKey(1))
	b := object{"extraId": extra, "idempotencyKey": "play-extra", "client": object{"device": object{"id": "device"}, "protocolVersions": []int{0}, "containers": []string{"mp4"}, "videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}}}
	if w := request("/playback", "device", b); w.Code != 202 || w.Header().Get("Retry-After") != "1" || len(i.sessions) != 0 {
		t.Fatal("cold extra promised admission", w.Code, w.Body.String())
	}
	broker.result = frontend.TrailerPreviewResult{State: "ready", Grant: &frontend.TrailerPreviewGrant{SessionID: "full-extra", URL: "/sessions/media/full-extra/grant/preview.mp4", MIMEType: "video/mp4", Purpose: "playback", ExpiresAt: time.Now().Add(time.Hour), SourceGeneration: 1, SeekGeneration: 1}}
	ready := request("/playback", "device", b)
	replay := request("/playback", "device", b)
	if ready.Code != 200 || replay.Body.String() != ready.Body.String() || broker.calls != 2 || len(i.sessions) != 1 {
		t.Fatal("preparing retry/admission replay failed", ready.Code, replay.Code, broker.calls, ready.Body.String())
	}
	s := i.sessions["full-extra"]
	if s.plan["purpose"] != "playback" || s.plan["extraId"] != extra || s.plan["titleId"] != nil || s.plan["positionSeconds"] != 0 {
		t.Fatal("extra acquired library/resume identity", s.plan)
	}
	command := object{"idempotencyKey": "seek", "kind": "seek", "positionSeconds": 15, "sourceGeneration": 1, "seekGeneration": 1}
	if w := request("/sessions/full-extra/commands", "device", command); w.Code != 200 {
		t.Fatal("seek failed", w.Code, w.Body.String())
	}
	command = object{"idempotencyKey": "pause", "kind": "pause", "sourceGeneration": 1, "seekGeneration": 2}
	if w := request("/sessions/full-extra/commands", "device", command); w.Code != 200 || reports != 2 {
		t.Fatal("pause failed", w.Code, w.Body.String(), reports)
	}
	command = object{"idempotencyKey": "stop", "kind": "stop", "sourceGeneration": 1, "seekGeneration": 2}
	if w := request("/sessions/full-extra/commands", "another", command); w.Code != 404 || stops != 0 {
		t.Fatal("cross-device stop", w.Code)
	}
	command["idempotencyKey"] = "stop-correct-device"
	if w := request("/sessions/full-extra/commands", "device", command); w.Code != 200 || stops != 1 {
		t.Fatal("stop failed", w.Code, w.Body.String())
	}
	t.Run("hero default and owner off", func(t *testing.T) {
		row := object{"purpose": frontend.RowRecentlyReleased}
		entry := object{"kind": "title", "receiptToken": "issued", "title": object{"id": "title", "kind": "movie", "artwork": []object{{"kind": "backdrop", "sizes": []object{{"url": "/signed"}}}}}}
		off, on := false, true
		for _, tc := range []struct {
			available bool
			policy    *bool
			want      bool
		}{{true, nil, true}, {true, &off, false}, {true, &on, true}, {false, nil, false}, {false, &on, false}} {
			got, ok := heroFromEntry(row, entry, heroSource{previewDefault: tc.available, previewPolicy: tc.policy})
			enabled, _ := got.hero["previewOnFocus"].(bool)
			if !ok || enabled != tc.want {
				t.Fatal("hero preview ignored availability/owner override", tc, got)
			}
		}
	})
}
