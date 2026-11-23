package etherplugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFlowsActionRouteAndPublishEvent(t *testing.T) {
	sideEffect := true
	man := Manifest{
		ID:      "community.example.lights",
		Name:    "Smart Lights",
		Version: "1.0.0",
		Ether: &EtherManifestBlock{
			Protocol:     1,
			Kind:         KindExtension,
			Summary:      "Controls room lighting scenes",
			Runtime:      RuntimeDecl{Kind: "remote"},
			Capabilities: []Capability{CapEvents, CapFlowActions},
			Contributes: &Contributes{
				Events: &EventsMeta{
					Subscribe: []string{"playback.session.started"},
					Publish: []PublishedEventMeta{{
						Key:   "scene.changed",
						Label: "Scene changed",
						Fields: []EventFieldMeta{
							{Name: "scene", Type: "string", Label: "Scene"},
						},
					}},
				},
				FlowActions: []FlowActionMeta{
					{
						Key:        "set_scene",
						Label:      "Set scene",
						Route:      "/ether/v1/flow-actions/set_scene",
						Idempotent: true,
						SideEffect: &sideEffect,
					},
					{
						Key:   "custom_dim",
						Label: "Custom dim",
						Route: "/ether/custom/dim",
					},
				},
			},
		},
	}

	p := New(Definition{
		Manifest: man,
		Routes: Routes{
			FlowAction: func(ctx *Context, req FlowActionRequest) (FlowActionResponse, error) {
				if err := ctx.PublishEvent("scene.changed", map[string]any{
					"scene": req.Params["scene"],
				}, &EventCorrelation{Key: "room:den"}); err != nil {
					return FlowActionResponse{}, err
				}
				return FlowActionResponse{
					Output: map[string]any{
						"key":            req.Key,
						"scene":          req.Params["scene"],
						"idempotencyKey": req.IdempotencyKey,
						"dryRun":         req.DryRun,
					},
				}, nil
			},
		},
	})

	h := NewTestHarness(p, nil)

	// Default route /ether/v1/flow-actions/{key} with idempotency header.
	status, out, err := h.FlowAction("set_scene", FlowActionRequest{
		FlowID:         "flow-1",
		RunID:          "run-1",
		NodeID:         "node-1",
		IdempotencyKey: "run-1:node-1:0",
		Params:         map[string]any{"scene": "cinema"},
	})
	if err != nil {
		t.Fatalf("FlowAction(set_scene): %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d", status)
	}
	if out.Output["key"] != "set_scene" || out.Output["scene"] != "cinema" || out.Output["idempotencyKey"] != "run-1:node-1:0" {
		t.Fatalf("unexpected FlowAction output: %+v", out.Output)
	}
	if len(h.Host.PublishedEvents) != 1 || h.Host.PublishedEvents[0].Key != "scene.changed" {
		t.Fatalf("expected 1 published event, got %+v", h.Host.PublishedEvents)
	}

	// Custom declared route /ether/custom/dim resolves back to key "custom_dim".
	status2, out2, err := h.FlowAction("custom_dim", FlowActionRequest{
		IdempotencyKey: "idem-custom",
		DryRun:         true,
		Params:         map[string]any{"scene": "warm"},
	})
	if err != nil {
		t.Fatalf("FlowAction(custom_dim): %v", err)
	}
	if status2 != http.StatusOK {
		t.Fatalf("expected 200, got %d", status2)
	}
	if out2.Output["key"] != "custom_dim" || out2.Output["dryRun"] != true || out2.Output["idempotencyKey"] != "idem-custom" {
		t.Fatalf("unexpected custom_dim output: %+v", out2.Output)
	}
}

func TestRemoteHostClientPublishEvent(t *testing.T) {
	var gotPath, gotCred string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCred = r.Header.Get("X-Ether-Plugin-Credential")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := NewRemoteHostClient(RemoteHostOptions{
		BaseURL:    srv.URL,
		Credential: "cred-123",
		HTTPClient: srv.Client(),
	})
	err := client.PublishEvent(context.Background(), "scene.changed", map[string]any{"scene": "cinema"}, &EventCorrelation{Key: "room:den"})
	if err != nil {
		t.Fatalf("RemoteHostClient.PublishEvent: %v", err)
	}
	if gotPath != "/ether/plugin-host/events/publish" || gotCred != "cred-123" {
		t.Fatalf("unexpected request path=%q cred=%q", gotPath, gotCred)
	}
	if gotBody["key"] != "scene.changed" {
		t.Fatalf("unexpected body: %+v", gotBody)
	}
}
