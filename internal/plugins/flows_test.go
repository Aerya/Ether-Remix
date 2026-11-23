package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/store"
)

type flowsTestEnv struct {
	m       *Manager
	bus     *events.Bus
	evReg   *events.Registry
	actions *flows.InMemoryActionRegistry
	nowMu   sync.Mutex
	now     time.Time
}

func newFlowsTestEnv(t *testing.T, useHosted bool) *flowsTestEnv {
	t.Helper()
	evReg := events.NewRegistry()
	evReg.MustRegister(events.EventSchema{
		Type:  "playback.session.started",
		Label: "Playback started",
		Fields: []events.FieldSchema{
			{Name: "title", Type: events.FieldString, Label: "Title"},
			{Name: "room", Type: events.FieldString, Label: "Room"},
			{Name: "viewerId", Type: events.FieldInt, Label: "Viewer", PII: true},
			{Name: "viewerName", Type: events.FieldString, Label: "Viewer name", PII: true},
			{Name: "sessionToken", Type: events.FieldString, Label: "Token", Secret: true},
		},
	})
	actReg := flows.NewActionRegistry()
	e := &flowsTestEnv{
		evReg:   evReg,
		actions: actReg,
		now:     time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
	}
	nowFn := func() time.Time {
		e.nowMu.Lock()
		defer e.nowMu.Unlock()
		return e.now
	}
	e.bus = events.NewBus(events.BusOptions{
		NodeID:   "node-test",
		Now:      nowFn,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Registry: evReg,
	})
	t.Cleanup(func() { e.bus.Close() })

	m, err := New(Deps{
		DB:            store.OpenTest(t),
		Registry:      extend.New(),
		Tools:         tools.NewCatalogue(),
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           nowFn,
		IsLeader:      func() bool { return true },
		EventBus:      e.bus,
		EventRegistry: evReg,
		FlowActions:   actReg,
	})
	if err != nil {
		t.Fatalf("plugins.New: %v", err)
	}
	if useHosted {
		eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
		t.Cleanup(func() { _ = eng.Close() })
		m.RegisterRuntime("hosted", HostedRuntimeFactory(eng))
	}
	t.Cleanup(func() {
		m.cancelActionJobs("")
		m.waitSchedule()
	})
	e.m = m
	return e
}

func (e *flowsTestEnv) advance(d time.Duration) {
	e.nowMu.Lock()
	e.now = e.now.Add(d)
	e.nowMu.Unlock()
}

func smartLightsTestManifest(id, version, eventKey, actionKey string) []byte {
	man := map[string]any{
		"id":      id,
		"version": version,
		"name":    "Smart Lights",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "extension",
			"summary":      "$t:summary",
			"runtime":      map[string]any{"kind": "hosted", "entry": "plugin.js"},
			"capabilities": []string{"events", "flow_actions"},
			"strings":      map[string]any{"en": "i18n/en.json"},
			"contributes": map[string]any{
				"events": map[string]any{
					"subscribe": []string{"playback.session.started"},
					"publish": []map[string]any{
						{
							"key":         eventKey,
							"version":     1,
							"label":       "$t:eventLabel",
							"description": "Scene changed.",
							"fields": []map[string]any{
								{
									"name":       "scene",
									"type":       "enum",
									"label":      "Scene",
									"filterable": true,
									"enum": []map[string]any{
										{"value": "cinema", "label": "Cinema"},
										{"value": "bright", "label": "Bright"},
									},
								},
								{"name": "brightness", "type": "int", "label": "Brightness", "unit": "percent"},
								{"name": "room", "type": "string", "label": "Room"},
								{"name": "source", "type": "string", "label": "Source"},
							},
						},
					},
				},
				"flowActions": []map[string]any{
					{
						"key":         actionKey,
						"label":       "$t:actionLabel",
						"description": "Switch the room lighting scene.",
						"route":       "/ether/v1/flow-actions/" + actionKey,
						"timeout":     "15s",
						"idempotent":  true,
						"sideEffect":  true,
						"params": []map[string]any{
							{
								"key":      "scene",
								"type":     "enum",
								"label":    "Scene",
								"required": true,
								"enum": []map[string]any{
									{"value": "cinema", "label": "Cinema"},
									{"value": "bright", "label": "Bright"},
								},
							},
							{"key": "brightness", "type": "int", "label": "Brightness", "default": 20},
							{"key": "room", "type": "string", "label": "Room", "default": "living-room"},
							{"key": "emitFollowUp", "type": "bool", "label": "Emit follow-up"},
						},
						"outputs": []map[string]any{
							{"name": "scene", "type": "string", "label": "Scene"},
							{"name": "brightness", "type": "int", "label": "Brightness"},
							{"name": "room", "type": "string", "label": "Room"},
							{"name": "replayed", "type": "bool", "label": "Replayed"},
							{"name": "idempotencyKey", "type": "string", "label": "Idempotency key"},
							{"name": "internalToken", "type": "string", "label": "Token", "secret": true},
						},
					},
				},
			},
		},
	}
	raw, _ := json.Marshal(man)
	return raw
}

const smartLightsTestJS = `
function firstHeader(headers, name) {
  if (!headers) return "";
  const target = name.toLowerCase();
  for (const k of Object.keys(headers)) {
    if (k.toLowerCase() === target) {
      const v = headers[k];
      return Array.isArray(v) ? String(v[0] || "") : String(v || "");
    }
  }
  return "";
}

function parseBody(raw) {
  if (!raw) return {};
  if (typeof raw === "string") return JSON.parse(raw);
  return raw;
}

function handle(req) {
  if (req.path === "/ether/health") {
    return { status: 200, body: JSON.stringify({ status: "ok" }) };
  }
  if (req.path === "/ether/event") {
    const body = parseBody(req.body);
    const ack = [];
    for (const ev of body.events || []) {
      ack.push(ev.id);
      if (ev.name === "playback.session.started") {
        ether.events.publish("scene.changed", {
          scene: "cinema",
          brightness: 15,
          room: (ev.payload && ev.payload.room) || "living-room",
          source: "playback"
        });
      }
    }
    return { status: 200, body: JSON.stringify({ ack }) };
  }
  if (req.path === "/ether/v1/flow-actions/set_scene") {
    const payload = parseBody(req.body);
    const idem = firstHeader(req.headers, "X-Ether-Idempotency-Key") || payload.idempotencyKey || "";
    if (idem) {
      const cached = ether.kv.get("idem:" + idem);
      if (cached) {
        const prev = JSON.parse(cached);
        prev.replayed = true;
        return { status: 200, body: JSON.stringify({ output: prev }) };
      }
    }
    const params = payload.params || {};
    const out = {
      scene: String(params.scene || "cinema"),
      brightness: Number(params.brightness ?? 20),
      room: String(params.room || "living-room"),
      replayed: false,
      idempotencyKey: idem,
      internalToken: "super-secret-value"
    };
    if (idem) {
      ether.kv.put("idem:" + idem, JSON.stringify(out));
    }
    if (params.emitFollowUp) {
      ether.events.publish("scene.changed", {
        scene: out.scene,
        brightness: out.brightness,
        room: out.room,
        source: "flow_action"
      });
    }
    return { status: 200, body: JSON.stringify({ output: out }) };
  }
  if (req.path === "/ether/test/publish-custom") {
    const p = parseBody(req.body);
    ether.events.publish(p.key, p.payload);
    return { status: 200, body: JSON.stringify({ ok: true }) };
  }
  return { status: 404, body: JSON.stringify({ error: "not found" }) };
}
globalThis.handle = handle;
`

func installFlowsPlugin(t *testing.T, e *flowsTestEnv, id, version, eventKey, actionKey, script string) Record {
	t.Helper()
	ctx := context.Background()
	files := map[string][]byte{
		"plugin.js":    []byte(script),
		"i18n/en.json": []byte(`{"summary":"Controls room lighting scenes","eventLabel":"Lighting scene changed","actionLabel":"Set lighting scene"}`),
	}
	src := Source{
		Kind:     "studio",
		Manifest: smartLightsTestManifest(id, version, eventKey, actionKey),
		Files:    files,
	}
	prev, err := e.m.PreviewInstall(ctx, src)
	if err != nil {
		t.Fatalf("PreviewInstall: %v", err)
	}
	rec, err := e.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	return rec
}

func TestFlowsHostedPluginEndToEnd(t *testing.T) {
	e := newFlowsTestEnv(t, true)
	ctx := context.Background()
	const pluginID = "community.example.smartlights"

	installFlowsPlugin(t, e, pluginID, "1.0.0", "scene.changed", "set_scene", smartLightsTestJS)

	eventType := events.PluginEventType(pluginID, "scene.changed")
	actionType := events.PluginEventType(pluginID, "set_scene")

	// 1. Registry entries are populated with resolved $t: copy.
	evSchema, ok := e.evReg.Get(eventType)
	if !ok {
		t.Fatalf("expected published event %s in events.Registry", eventType)
	}
	if evSchema.Owner != pluginID || evSchema.Label != "Lighting scene changed" {
		t.Fatalf("unexpected event schema: %+v", evSchema)
	}
	handler, ok := e.actions.Get(actionType)
	if !ok {
		t.Fatalf("expected flow action %s in flows.ActionRegistry", actionType)
	}
	spec := handler.Spec()
	if spec.Owner != pluginID || spec.Label != "Set lighting scene" || !spec.Idempotent || !spec.SideEffect {
		t.Fatalf("unexpected action spec: %+v", spec)
	}

	// 2. A Flow graph triggered by the plugin event and invoking the plugin action validates cleanly.
	flowGraph := flows.Graph{
		SchemaVersion: flows.SchemaVersion,
		Nodes: []flows.Node{
			{
				ID:     "trig_scene",
				Kind:   flows.NodeTriggerEvent,
				Config: flows.MustMarshalConfig(flows.EventTriggerConfig{Events: []string{eventType}}),
			},
			{
				ID:   "act_lights",
				Kind: flows.NodeAction,
				Config: flows.MustMarshalConfig(flows.ActionNodeConfig{
					ActionType: actionType,
					Params: map[string]any{
						"scene":      "bright",
						"brightness": 85,
						"room":       "den",
					},
				}),
			},
		},
		Edges: []flows.Edge{{ID: "e1", Source: "trig_scene", Target: "act_lights"}},
	}
	valRes := flows.ValidateGraph(flowGraph, flows.ValidateOptions{
		Events:   e.evReg,
		Actions:  e.actions,
		Triggers: flows.NewTriggerRegistry(),
	})
	if !valRes.Valid {
		t.Fatalf("ValidateGraph rejected plugin flow: %s (%+v)", valRes.Error(), valRes.Errors)
	}

	// 3. Start event bridge and subscribe a Flow trigger listener for the plugin's custom event.
	stopBridge := e.m.StartEventBridge(ctx)
	defer stopBridge()

	sub := e.bus.Subscribe(events.SubscriptionOptions{
		Name:     "test-flow-runner",
		Patterns: []string{eventType},
		Buffer:   16,
	})
	defer sub.Unsubscribe()

	// Emit canonical playback.session.started onto the bus.
	e.bus.Publish(events.Envelope{
		Type:    "playback.session.started",
		Source:  "playback",
		Payload: json.RawMessage(`{"title":"Arrival","room":"theater"}`),
	})

	// Wait until the bridge enqueues the event for the plugin, then deliver outbox.
	deadline := time.Now().Add(5 * time.Second)
	for {
		evs, _, err := e.m.store.outboxBatch(ctx, pluginID, 10)
		if err != nil {
			t.Fatalf("outboxBatch: %v", err)
		}
		if len(evs) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for event bridge to enqueue playback.session.started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := e.m.DeliverOutbox(ctx, pluginID); err != nil {
		t.Fatalf("DeliverOutbox: %v", err)
	}

	// 4. The plugin published scene.changed onto events.Bus; our Flow trigger receives it.
	var triggered events.Envelope
	select {
	case triggered = <-sub.C():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for plugin-published scene.changed event on events.Bus")
	}
	if triggered.Type != eventType || triggered.Source != "plugin:"+pluginID {
		t.Fatalf("unexpected triggered envelope: %+v", triggered)
	}
	var evPayload map[string]any
	if err := json.Unmarshal(triggered.Payload, &evPayload); err != nil {
		t.Fatalf("unmarshal plugin event payload: %v", err)
	}
	if evPayload["scene"] != "cinema" || evPayload["room"] != "theater" {
		t.Fatalf("unexpected plugin event payload: %+v", evPayload)
	}

	// 5. Execute the triggered Flow's plugin action through flows.ActionRegistry with idempotency key.
	actx := flows.ActionContext{
		FlowID:         "flow-lights-1",
		RunID:          "run-42",
		NodeID:         "act_lights",
		IdempotencyKey: "run-42:act_lights:0",
		FlowDepth:      1,
	}
	res1, err := handler.Execute(ctx, actx, map[string]any{
		"scene":        "bright",
		"brightness":   85,
		"room":         evPayload["room"],
		"emitFollowUp": true,
	})
	if err != nil {
		t.Fatalf("plugin action Execute: %v", err)
	}
	if res1.Output["scene"] != "bright" || res1.Output["room"] != "theater" || res1.Output["replayed"] != false {
		t.Fatalf("unexpected action output: %+v", res1.Output)
	}
	if res1.Output["idempotencyKey"] != "run-42:act_lights:0" {
		t.Fatalf("expected X-Ether-Idempotency-Key header to reach plugin, got %+v", res1.Output)
	}
	// Secret output field must be redacted before returning to flow context.
	if res1.Output["internalToken"] != "[redacted]" {
		t.Fatalf("expected secret output field to be redacted, got %v", res1.Output["internalToken"])
	}

	// 6. Follow-up event published inside the flow action carries incremented FlowDepth + OriginRunID.
	select {
	case followUp := <-sub.C():
		if followUp.Meta.FlowDepth != 2 {
			t.Fatalf("expected FlowDepth=2 on action-published event, got %d", followUp.Meta.FlowDepth)
		}
		if followUp.Meta.OriginRunID != "run-42" {
			t.Fatalf("expected OriginRunID=run-42, got %q", followUp.Meta.OriginRunID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for follow-up event from flow action")
	}

	// 7. Re-executing with the same IdempotencyKey hits the plugin's KV idempotency cache.
	res2, err := handler.Execute(ctx, actx, map[string]any{
		"scene":      "bright",
		"brightness": 85,
		"room":       "theater",
	})
	if err != nil {
		t.Fatalf("plugin action retry Execute: %v", err)
	}
	if res2.Output["replayed"] != true {
		t.Fatalf("expected idempotent replay on retry, got %+v", res2.Output)
	}
}

func TestPublishEventValidationAndRateLimit(t *testing.T) {
	e := newFlowsTestEnv(t, false)
	e.m.RegisterRuntime("hosted", func(inst Installed) (Runtime, error) {
		return &fakeRuntime{inst: inst}, nil
	})
	ctx := context.Background()
	const pluginID = "community.example.lights"
	installFlowsPlugin(t, e, pluginID, "1.0.0", "scene.changed", "set_scene", "// stub")

	h := e.m.HostFor(pluginID)

	// Undeclared event key rejected.
	if err := h.PublishEvent(ctx, "unknown.event", json.RawMessage(`{}`), events.Correlation{}); err == nil {
		t.Fatal("expected undeclared event key to fail")
	}
	// Non-object JSON payload rejected.
	if err := h.PublishEvent(ctx, "scene.changed", json.RawMessage(`["cinema"]`), events.Correlation{}); err == nil {
		t.Fatal("expected non-object payload to fail schema validation")
	}
	// Wrong field type rejected.
	if err := h.PublishEvent(ctx, "scene.changed", json.RawMessage(`{"scene":"cinema","brightness":"high"}`), events.Correlation{}); err == nil {
		t.Fatal("expected wrong field type to fail schema validation")
	}
	// Invalid enum value rejected.
	if err := h.PublishEvent(ctx, "scene.changed", json.RawMessage(`{"scene":"disco"}`), events.Correlation{}); err == nil {
		t.Fatal("expected invalid enum value to fail schema validation")
	}
	// Oversized payload (> 64 KiB) rejected.
	big := `{"room":"` + strings.Repeat("a", MaxPublishedEventPayloadBytes) + `"}`
	if err := h.PublishEvent(ctx, "scene.changed", json.RawMessage(big), events.Correlation{}); err == nil {
		t.Fatal("expected oversized payload to fail validation")
	}

	// Rate limit: DefaultEventPublishPerMin (30) publishes succeed, 31st returns ErrRateLimited.
	validPayload := json.RawMessage(`{"scene":"cinema","brightness":10}`)
	for i := 0; i < DefaultEventPublishPerMin; i++ {
		if err := h.PublishEvent(ctx, "scene.changed", validPayload, events.Correlation{}); err != nil {
			t.Fatalf("publish %d/%d failed unexpectedly: %v", i+1, DefaultEventPublishPerMin, err)
		}
	}
	if err := h.PublishEvent(ctx, "scene.changed", validPayload, events.Correlation{}); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited on publish %d, got %v", DefaultEventPublishPerMin+1, err)
	}

	// Advancing fake clock past 1 minute resets the sliding window.
	e.advance(61 * time.Second)
	if err := h.PublishEvent(ctx, "scene.changed", validPayload, events.Correlation{}); err != nil {
		t.Fatalf("expected publish after rate window reset to succeed, got %v", err)
	}
}

func TestFlowsShadowMode(t *testing.T) {
	e := newFlowsTestEnv(t, false)
	fake := &fakeRuntime{routes: map[string]func(Request) (Response, error){}}
	e.m.RegisterRuntime("hosted", func(inst Installed) (Runtime, error) {
		fake.inst = inst
		return fake, nil
	})
	ctx := context.Background()
	const pluginID = "community.example.shadowlights"
	installFlowsPlugin(t, e, pluginID, "1.0.0", "scene.changed", "set_scene", "// stub")

	if err := e.m.SetShadow(ctx, pluginID, true); err != nil {
		t.Fatalf("SetShadow: %v", err)
	}

	sub := e.bus.Subscribe(events.SubscriptionOptions{Name: "shadow-check", Buffer: 8})
	defer sub.Unsubscribe()

	// PublishEvent in shadow mode journals without publishing to events.Bus.
	h := e.m.HostFor(pluginID)
	if err := h.PublishEvent(ctx, "scene.changed", json.RawMessage(`{"scene":"cinema"}`), events.Correlation{}); err != nil {
		t.Fatalf("shadow PublishEvent: %v", err)
	}
	select {
	case ev := <-sub.C():
		t.Fatalf("shadow PublishEvent must not emit onto events.Bus, got %+v", ev)
	default:
	}

	// Flow action in shadow mode journals and returns {shadow: true} without calling runtime.
	actionType := events.PluginEventType(pluginID, "set_scene")
	handler, ok := e.actions.Get(actionType)
	if !ok {
		t.Fatalf("expected action %s registered in shadow mode", actionType)
	}
	callsBefore := len(fake.paths())
	res, err := handler.Execute(ctx, flows.ActionContext{
		FlowID:         "f1",
		RunID:          "r1",
		NodeID:         "n1",
		IdempotencyKey: "k1",
	}, map[string]any{"scene": "cinema"})
	if err != nil {
		t.Fatalf("shadow action Execute: %v", err)
	}
	if res.Output["shadow"] != true {
		t.Fatalf("expected shadow=true output, got %+v", res.Output)
	}
	if len(fake.paths()) != callsBefore {
		t.Fatalf("shadow mode must not invoke plugin runtime, calls=%v", fake.paths())
	}

	// Both operations recorded in journal with Shadow=true.
	entries, err := e.m.Journal(ctx, pluginID, 20, true)
	if err != nil {
		t.Fatalf("Journal: %v", err)
	}
	var sawPub, sawAct bool
	for _, entry := range entries {
		if entry.Shadow && entry.Op == "events.publish" {
			sawPub = true
		}
		if entry.Shadow && entry.Op == "flow_action" {
			sawAct = true
		}
	}
	if !sawPub || !sawAct {
		t.Fatalf("expected shadow journal entries for events.publish and flow_action, got %+v", entries)
	}
}

func TestFlowsAtomicLifecycleReplacement(t *testing.T) {
	e := newFlowsTestEnv(t, false)
	e.m.RegisterRuntime("hosted", func(inst Installed) (Runtime, error) {
		return &fakeRuntime{inst: inst}, nil
	})
	ctx := context.Background()
	const pluginID = "community.example.lifecycle"

	// Install v1 with scene.changed + set_scene.
	installFlowsPlugin(t, e, pluginID, "1.0.0", "scene.changed", "set_scene", "// v1")
	ev1 := events.PluginEventType(pluginID, "scene.changed")
	act1 := events.PluginEventType(pluginID, "set_scene")
	if _, ok := e.evReg.Get(ev1); !ok {
		t.Fatalf("v1 event %s not registered", ev1)
	}
	if _, ok := e.actions.Get(act1); !ok {
		t.Fatalf("v1 action %s not registered", act1)
	}

	// Update to v2 with dimmer.updated + set_dimmer: atomic replacement removes v1 and installs v2.
	installFlowsPlugin(t, e, pluginID, "2.0.0", "dimmer.updated", "set_dimmer", "// v2")
	ev2 := events.PluginEventType(pluginID, "dimmer.updated")
	act2 := events.PluginEventType(pluginID, "set_dimmer")
	if _, ok := e.evReg.Get(ev1); ok {
		t.Fatalf("stale v1 event %s remained after update", ev1)
	}
	if _, ok := e.actions.Get(act1); ok {
		t.Fatalf("stale v1 action %s remained after update", act1)
	}
	if _, ok := e.evReg.Get(ev2); !ok {
		t.Fatalf("v2 event %s missing after update", ev2)
	}
	act2Handler, ok := e.actions.Get(act2)
	if !ok {
		t.Fatalf("v2 action %s missing after update", act2)
	}

	// Revoke flow_actions grant: removed from registry, and a held handler fails live with ErrPermissionDenied.
	if err := e.m.SetGrants(ctx, pluginID, Grants{Revoked: []string{string(CapFlowActions)}}); err != nil {
		t.Fatalf("SetGrants revoke flow_actions: %v", err)
	}
	if _, ok := e.actions.Get(act2); ok {
		t.Fatal("revoked flow action must be removed from ActionRegistry")
	}
	if _, err := act2Handler.Execute(ctx, flows.ActionContext{FlowID: "f", RunID: "r", NodeID: "n"}, map[string]any{"scene": "cinema"}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied on revoked flow_actions, got %v", err)
	}

	// Revoke events grant: removed from EventRegistry, and PublishEvent fails with ErrPermissionDenied.
	if err := e.m.SetGrants(ctx, pluginID, Grants{Revoked: []string{string(CapEvents)}}); err != nil {
		t.Fatalf("SetGrants revoke events: %v", err)
	}
	if _, ok := e.evReg.Get(ev2); ok {
		t.Fatal("revoked published event must be removed from events.Registry")
	}
	if err := e.m.HostFor(pluginID).PublishEvent(ctx, "dimmer.updated", json.RawMessage(`{"scene":"cinema"}`), events.Correlation{}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied on revoked events, got %v", err)
	}
	// Restoring grants re-registers both.
	if err := e.m.SetGrants(ctx, pluginID, Grants{}); err != nil {
		t.Fatalf("SetGrants restore: %v", err)
	}
	if _, ok := e.evReg.Get(ev2); !ok {
		t.Fatal("restored event missing from events.Registry")
	}
	if _, ok := e.actions.Get(act2); !ok {
		t.Fatal("restored action missing from flows.ActionRegistry")
	}

	// Disable removes both; Enable restores both.
	if err := e.m.SetEnabled(ctx, pluginID, false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if _, ok := e.evReg.Get(ev2); ok {
		t.Fatal("disabled plugin event must be removed")
	}
	if _, ok := e.actions.Get(act2); ok {
		t.Fatal("disabled plugin action must be removed")
	}
	if err := e.m.SetEnabled(ctx, pluginID, true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	if _, ok := e.evReg.Get(ev2); !ok {
		t.Fatal("re-enabled plugin event must be restored")
	}
	if _, ok := e.actions.Get(act2); !ok {
		t.Fatal("re-enabled plugin action must be restored")
	}

	// Remove cleans up both.
	if err := e.m.Remove(ctx, pluginID, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := e.evReg.Get(ev2); ok {
		t.Fatal("removed plugin event must be unregistered")
	}
	if _, ok := e.actions.Get(act2); ok {
		t.Fatal("removed plugin action must be unregistered")
	}
}

func TestEventBridgeLegacy12AndRedaction(t *testing.T) {
	e := newFlowsTestEnv(t, false)

	var deliveredMu sync.Mutex
	deliveredByPlugin := map[string][]Event{}
	e.m.RegisterRuntime("hosted", func(inst Installed) (Runtime, error) {
		return &fakeRuntime{
			inst: inst,
			routes: map[string]func(Request) (Response, error){
				"/ether/event": func(req Request) (Response, error) {
					var body struct {
						Events []Event `json:"events"`
					}
					_ = json.Unmarshal(req.Body, &body)
					deliveredMu.Lock()
					deliveredByPlugin[inst.ID] = append(deliveredByPlugin[inst.ID], body.Events...)
					deliveredMu.Unlock()
					ack := make([]string, 0, len(body.Events))
					for _, ev := range body.Events {
						ack = append(ack, ev.ID)
					}
					raw, _ := json.Marshal(map[string]any{"ack": ack})
					return Response{Status: 200, Body: raw}, nil
				},
			},
		}, nil
	})
	ctx := context.Background()

	// Verify zero outbox writes when no plugin subscribes.
	e.m.HandleBusEvent(ctx, events.Envelope{
		Type:    "playback.session.started",
		Payload: json.RawMessage(`{"title":"Silent"}`),
	})
	var outboxCount int
	if err := e.m.store.db.QueryRow(`SELECT COUNT(*) FROM plugins_outbox`).Scan(&outboxCount); err != nil {
		t.Fatalf("query plugins_outbox: %v", err)
	}
	if outboxCount != 0 {
		t.Fatalf("expected zero outbox writes with no subscribers, got %d", outboxCount)
	}

	// Install Plugin A subscribing to ALL 12 legacy EPP/1 event names (no viewer grant).
	allLegacy := make([]string, 0, len(KnownEvents))
	for k := range KnownEvents {
		allLegacy = append(allLegacy, k)
	}
	installSub := func(id string, subs []string, wantViewers bool) {
		t.Helper()
		perms := map[string]any{}
		if wantViewers {
			perms["viewers"] = map[string]any{"identity": "granted_viewers"}
		}
		man, _ := json.Marshal(map[string]any{
			"id": id, "version": "1.0.0", "name": id,
			"ether": map[string]any{
				"protocol": 1, "kind": "extension", "summary": "Subscribes to events",
				"runtime":      map[string]any{"kind": "hosted", "entry": "plugin.js"},
				"capabilities": []string{"events"},
				"permissions":  perms,
				"contributes": map[string]any{
					"events": map[string]any{"subscribe": subs},
				},
			},
		})
		prev, err := e.m.PreviewInstall(ctx, Source{
			Kind: "studio", Manifest: man, Files: map[string][]byte{"plugin.js": []byte("// sub")},
		})
		if err != nil {
			t.Fatalf("PreviewInstall %s: %v", id, err)
		}
		if _, err := e.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true}); err != nil {
			t.Fatalf("Install %s: %v", id, err)
		}
	}

	installSub("community.example.legacy12", allLegacy, false)
	installSub("community.example.canonical", []string{"playback.session.started"}, true)
	if err := e.m.SetGrants(ctx, "community.example.canonical", Grants{Viewers: []int64{7}}); err != nil {
		t.Fatalf("SetGrants viewers: %v", err)
	}

	// Emit canonical events for all 12 legacy mappings through HandleBusEvent.
	if len(LegacyToCanonicalEvent) != 12 {
		t.Fatalf("expected 12 legacy-to-canonical mappings, got %d", len(LegacyToCanonicalEvent))
	}
	for legacy, canonical := range LegacyToCanonicalEvent {
		payload := json.RawMessage(`{"title":"Test","viewerId":7,"viewerName":"Alex","sessionToken":"tok_secret_123"}`)
		e.m.HandleBusEvent(ctx, events.Envelope{
			ID:      "ev-" + legacy,
			Type:    canonical,
			Payload: payload,
		})
	}

	if err := e.m.DeliverOutbox(ctx, "community.example.legacy12"); err != nil {
		t.Fatalf("DeliverOutbox legacy12: %v", err)
	}
	if err := e.m.DeliverOutbox(ctx, "community.example.canonical"); err != nil {
		t.Fatalf("DeliverOutbox canonical: %v", err)
	}

	deliveredMu.Lock()
	legacyGot := deliveredByPlugin["community.example.legacy12"]
	canonGot := deliveredByPlugin["community.example.canonical"]
	deliveredMu.Unlock()

	seenLegacy := map[string]Event{}
	for _, ev := range legacyGot {
		seenLegacy[ev.Name] = ev
	}
	for legacy := range KnownEvents {
		ev, ok := seenLegacy[legacy]
		if !ok {
			t.Errorf("legacy plugin did not receive bridged event %q", legacy)
			continue
		}
		if legacy == EventPlaybackStarted {
			// Without viewer grant, viewerId/viewerName/sessionToken must not leak.
			var d map[string]any
			_ = json.Unmarshal(ev.Payload, &d)
			if _, has := d["viewerId"]; has {
				t.Errorf("ungranted plugin received viewerId: %+v", d)
			}
			if _, has := d["viewerName"]; has {
				t.Errorf("ungranted plugin received viewerName: %+v", d)
			}
			if tok, has := d["sessionToken"]; has && tok != events.RedactedValue {
				t.Errorf("plugin received unredacted secret sessionToken: %+v", d)
			}
		}
	}

	// Canonical subscriber received canonical event type, keeps viewer fields (granted), redacts secret field.
	if len(canonGot) != 1 || canonGot[0].Name != "playback.session.started" {
		t.Fatalf("expected 1 canonical playback.session.started event, got %+v", canonGot)
	}
	var canonData map[string]any
	_ = json.Unmarshal(canonGot[0].Payload, &canonData)
	if canonData["viewerId"] == nil || canonData["viewerName"] != "Alex" {
		t.Fatalf("granted viewer plugin expected viewer fields, got %+v", canonData)
	}
	if tok, has := canonData["sessionToken"]; has && tok != events.RedactedValue {
		t.Fatalf("secret field sessionToken must be redacted even with viewer grant: %+v", canonData)
	}
}
