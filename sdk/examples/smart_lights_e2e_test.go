package examples_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/kit"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestSmartLightsExampleEndToEndFlow(t *testing.T) {
	ctx := context.Background()

	// Pack the real smart-lights example plugin into an .etherpkg zip.
	pkg, err := kit.LoadDir("smart-lights")
	if err != nil {
		t.Fatalf("kit.LoadDir(smart-lights): %v", err)
	}
	pkgBytes, err := kit.Pack(pkg)
	if err != nil {
		t.Fatalf("kit.Pack(smart-lights): %v", err)
	}

	evReg := events.NewRegistry()
	evReg.MustRegister(events.EventSchema{
		Type:  "playback.session.started",
		Label: "Playback started",
		Fields: []events.FieldSchema{
			{Name: "title", Type: events.FieldString, Label: "Title"},
		},
	})
	actReg := flows.NewActionRegistry()
	bus := events.NewBus(events.BusOptions{
		NodeID:   "node-e2e",
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Registry: evReg,
	})
	defer bus.Close()

	mgr, err := plugins.New(plugins.Deps{
		DB:            store.OpenTest(t),
		Registry:      extend.New(),
		Tools:         tools.NewCatalogue(),
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		IsLeader:      func() bool { return true },
		EventBus:      bus,
		EventRegistry: evReg,
		FlowActions:   actReg,
	})
	if err != nil {
		t.Fatalf("plugins.New: %v", err)
	}
	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = eng.Close() })
	mgr.RegisterRuntime("hosted", plugins.HostedRuntimeFactory(eng))

	prev, err := mgr.PreviewInstall(ctx, plugins.Source{Kind: "pkg", Pkg: pkgBytes})
	if err != nil {
		t.Fatalf("PreviewInstall: %v", err)
	}
	rec, err := mgr.Install(ctx, prev, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	eventType := events.PluginEventType(rec.ID, "scene.changed")
	actionType := flows.PluginActionType(rec.ID, "set_scene")

	if _, ok := evReg.Get(eventType); !ok {
		t.Fatalf("expected %s registered in events.Registry", eventType)
	}
	handler, ok := actReg.Get(actionType)
	if !ok {
		t.Fatalf("expected %s registered in flows.ActionRegistry", actionType)
	}

	// Validate a Flow graph triggered by smart-lights' event and calling smart-lights' action.
	valRes := flows.ValidateGraph(flows.Graph{
		SchemaVersion: flows.SchemaVersion,
		Nodes: []flows.Node{
			{
				ID:     "on_scene",
				Kind:   flows.NodeTriggerEvent,
				Config: flows.MustMarshalConfig(flows.EventTriggerConfig{Events: []string{eventType}}),
			},
			{
				ID:   "set_warm",
				Kind: flows.NodeAction,
				Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
					ActionType: actionType,
					Params: map[string]any{
						"room":       "Den",
						"scene":      "warm",
						"brightness": 65,
					},
				}),
			},
		},
		Edges: []flows.Edge{{ID: "e1", Source: "on_scene", Target: "set_warm"}},
	}, flows.ValidateOptions{
		Events:   evReg,
		Actions:  actReg,
		Triggers: flows.NewTriggerRegistry(),
	})
	if !valRes.Valid {
		t.Fatalf("ValidateGraph failed: %s", valRes.Error())
	}

	// Subscribe to the plugin's custom event on events.Bus.
	sub := bus.Subscribe(events.SubscriptionOptions{
		Name:     "smart-lights-flow",
		Patterns: []string{eventType},
		Buffer:   8,
	})
	defer sub.Unsubscribe()

	// Bridge a playback.session.started event into the plugin -> plugin publishes scene.changed.
	mgr.HandleBusEvent(ctx, events.Envelope{
		Type:    "playback.session.started",
		Source:  "playback",
		Payload: json.RawMessage(`{"title":"Sample Film"}`),
	})
	if err := mgr.DeliverOutbox(ctx, rec.ID); err != nil {
		t.Fatalf("DeliverOutbox: %v", err)
	}

	select {
	case ev := <-sub.C():
		var data map[string]any
		if err := json.Unmarshal(ev.Payload, &data); err != nil {
			t.Fatalf("unmarshal scene.changed: %v", err)
		}
		if data["scene"] != "cinema" {
			t.Fatalf("expected cinema scene after playback start, got %+v", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for smart-lights scene.changed event")
	}

	// Triggered Flow calls the plugin's set_scene action with X-Ether-Idempotency-Key.
	actx := flows.ActionContext{
		FlowID:         "flow-smart-lights",
		RunID:          "run-100",
		NodeID:         "set_warm",
		IdempotencyKey: "run-100:set_warm:0",
		FlowDepth:      1,
	}
	res1, err := handler.Execute(ctx, actx, map[string]any{
		"room":       "Den",
		"scene":      "warm",
		"brightness": 65,
	})
	if err != nil {
		t.Fatalf("set_scene Execute: %v", err)
	}
	if res1.Output["room"] != "Den" || res1.Output["scene"] != "warm" || res1.Output["applied"] != true || res1.Output["deduplicated"] != false {
		t.Fatalf("unexpected set_scene output: %+v", res1.Output)
	}

	// Re-running with the same IdempotencyKey deduplicates cleanly via KV.
	res2, err := handler.Execute(ctx, actx, map[string]any{
		"room":       "Den",
		"scene":      "warm",
		"brightness": 65,
	})
	if err != nil {
		t.Fatalf("set_scene retry Execute: %v", err)
	}
	if res2.Output["deduplicated"] != true {
		t.Fatalf("expected deduplicated=true on retry, got %+v", res2.Output)
	}

	// Dry-run succeeds without mutating state or deduplicating.
	dryCtx := actx
	dryCtx.DryRun = true
	dryCtx.IdempotencyKey = "run-100:set_warm:dry"
	resDry, err := handler.Execute(ctx, dryCtx, map[string]any{
		"room":       "Den",
		"scene":      "off",
		"brightness": 0,
	})
	if err != nil {
		t.Fatalf("set_scene dryRun Execute: %v", err)
	}
	if resDry.Output["applied"] != false || resDry.Output["scene"] != "off" {
		t.Fatalf("unexpected dryRun output: %+v", resDry.Output)
	}
}
