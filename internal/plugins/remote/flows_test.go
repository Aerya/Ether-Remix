package remote

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/remotetest"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestRemotePluginHostPublishEventAndActionIdempotency(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)

	evReg := events.NewRegistry()
	actReg := flows.NewActionRegistry()
	nowMu := sync.Mutex{}
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	nowFn := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	bus := events.NewBus(events.BusOptions{
		NodeID:   "node-remote",
		Now:      nowFn,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Registry: evReg,
	})
	defer bus.Close()

	const pluginID = "community.example.remotelights"
	manifest := `{
		"id": "community.example.remotelights",
		"name": "Remote Lights",
		"version": "1.0.0",
		"ether": {
			"protocol": 1,
			"kind": "extension",
			"summary": "Controls lights from a paired sidecar",
			"runtime": { "kind": "remote", "mode": "url" },
			"capabilities": ["events", "flow_actions"],
			"contributes": {
				"events": {
					"publish": [{
						"key": "scene.changed",
						"version": 1,
						"label": "Scene changed",
						"fields": [
							{ "name": "scene", "type": "enum", "label": "Scene", "enum": [{ "value": "cinema" }, { "value": "bright" }] },
							{ "name": "brightness", "type": "int", "label": "Brightness" }
						]
					}]
				},
				"flowActions": [{
					"key": "set_scene",
					"label": "Set scene",
					"route": "/ether/v1/flow-actions/set_scene",
					"idempotent": true,
					"params": [
						{ "key": "scene", "type": "string", "label": "Scene", "required": true }
					]
				}]
			}
		}
	}`

	// Remote HTTP plugin server receiving Flow action invocations.
	var gotIdemHeader string
	var gotBody map[string]any
	pluginSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/manifest.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(manifest))
		case r.URL.Path == "/ether/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/ether/v1/flow-actions/set_scene":
			gotIdemHeader = r.Header.Get(HeaderIdempotencyKey)
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"output":{"scene":"cinema","applied":true}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer pluginSrv.Close()

	mgr, err := plugins.New(plugins.Deps{
		DB:            db,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           nowFn,
		IsLeader:      func() bool { return true },
		FetchClient:   pluginSrv.Client(),
		EventBus:      bus,
		EventRegistry: evReg,
		FlowActions:   actReg,
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.RegisterRuntime("remote", (&Factory{
		TrustedLAN: func(string) bool { return true },
	}).RuntimeFactory())

	prev, err := mgr.PreviewInstall(ctx, plugins.Source{
		Kind: "url",
		URL:  pluginSrv.URL + "/manifest.json",
		LAN:  true,
	})
	if err != nil {
		t.Fatalf("PreviewInstall: %v", err)
	}
	if _, err := mgr.Install(ctx, prev, plugins.InstallOptions{AcceptAddedPermissions: true}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Host HTTP service for remote plugins.
	hostSrv := httptest.NewServer(NewHostHandler(HostOptions{
		Host: HostAdapter{
			HostFor:             mgr.HostFor,
			SettingsWithSecrets: mgr.PluginSettings,
		},
		Auth: remotetest.Credentials{
			"cred-lights": pluginID,
		},
	}))
	defer hostSrv.Close()

	doPublish := func(body string) (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, hostSrv.URL+"/ether/plugin-host/events/publish", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(HeaderCredential, "cred-lights")
		req.Header.Set("Content-Type", "application/json")
		resp, err := hostSrv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	sub := bus.Subscribe(events.SubscriptionOptions{
		Name:     "remote-lights-sub",
		Patterns: []string{events.PluginEventType(pluginID, "scene.changed")},
		Buffer:   64,
	})
	defer sub.Unsubscribe()

	// 1. Valid publish returns 200 and delivers event onto events.Bus.
	code, _ := doPublish(`{"key":"scene.changed","payload":{"scene":"cinema","brightness":20},"correlation":{"key":"room:den"}}`)
	if code != http.StatusOK {
		t.Fatalf("expected 200 on valid publish, got %d", code)
	}
	select {
	case ev := <-sub.C():
		if ev.Type != events.PluginEventType(pluginID, "scene.changed") || ev.Correlation.Key != "room:den" {
			t.Fatalf("unexpected bus event: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for remote-published event on bus")
	}

	// 2. Schema validation failure returns 400.
	if code, _ := doPublish(`{"key":"scene.changed","payload":{"scene":"invalid_scene"}}`); code != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid enum, got %d", code)
	}

	// 3. Rate limit exhaustion returns 429 rate_limited.
	for i := 1; i < plugins.DefaultEventPublishPerMin; i++ {
		if code, _ := doPublish(`{"key":"scene.changed","payload":{"scene":"bright","brightness":90}}`); code != http.StatusOK {
			t.Fatalf("publish %d failed with status %d", i+1, code)
		}
	}
	code, errBody := doPublish(`{"key":"scene.changed","payload":{"scene":"bright","brightness":90}}`)
	if code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 when rate limited, got %d (%+v)", code, errBody)
	}

	// 4. Revoked events grant returns 403.
	nowMu.Lock()
	now = now.Add(61 * time.Second)
	nowMu.Unlock()
	if err := mgr.SetGrants(ctx, pluginID, plugins.Grants{Revoked: []string{string(plugins.CapEvents)}}); err != nil {
		t.Fatal(err)
	}
	if code, _ := doPublish(`{"key":"scene.changed","payload":{"scene":"cinema"}}`); code != http.StatusForbidden {
		t.Fatalf("expected 403 when events grant revoked, got %d", code)
	}

	// 5. Executing remote Flow action forwards X-Ether-Idempotency-Key header over HTTP.
	actionType := flows.PluginActionType(pluginID, "set_scene")
	handler, ok := actReg.Get(actionType)
	if !ok {
		t.Fatalf("expected remote flow action %s registered", actionType)
	}
	res, err := handler.Execute(ctx, flows.ActionContext{
		FlowID:         "flow-1",
		RunID:          "run-99",
		NodeID:         "node-1",
		IdempotencyKey: "run-99:node-1:0",
	}, map[string]any{"scene": "cinema"})
	if err != nil {
		t.Fatalf("remote flow action Execute: %v", err)
	}
	if gotIdemHeader != "run-99:node-1:0" {
		t.Fatalf("expected X-Ether-Idempotency-Key=run-99:node-1:0 over HTTP, got %q", gotIdemHeader)
	}
	if res.Output["scene"] != "cinema" || res.Output["applied"] != true {
		t.Fatalf("unexpected remote action output: %+v", res.Output)
	}
}
