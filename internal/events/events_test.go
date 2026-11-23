package events

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestValidateType(t *testing.T) {
	valid := []string{
		"playback.session.started",
		"livetv.input.lost",
		"plugin.package.installed",
		"plugin.health.degraded",
		"system.update.rolled_back",
		"plugin:com.acme.sync/list.synced",
		"plugin:community.trakt/scrobble",
		"plugin:io.github.foo-bar/job.state.changed",
	}
	for _, typ := range valid {
		if err := ValidateType(typ); err != nil {
			t.Errorf("ValidateType(%q) unexpected error: %v", typ, err)
		}
	}

	invalid := []string{
		"",
		"playback",
		"playback.started",
		"playback.session.started.extra",
		"Playback.session.started",
		"plugin:badid/list.synced",
		"plugin:com.acme.sync/",
		"plugin:com.acme.sync/UPPER",
		strings.Repeat("a", 129),
	}
	for _, typ := range invalid {
		if err := ValidateType(typ); !errors.Is(err, ErrInvalidEventType) {
			t.Errorf("ValidateType(%q) = %v, want ErrInvalidEventType", typ, err)
		}
	}
}

func TestEnvelopeAndCorrelation(t *testing.T) {
	type payload struct {
		TitleName   string `json:"titleName"`
		BitrateKbps int    `json:"bitrateKbps"`
	}
	ev, err := NewEnvelope("playback.session.started", payload{TitleName: "Dune", BitrateKbps: 18500},
		WithNode("node-a"),
		WithCorrelation(Correlation{SessionID: "s1", PlayID: "p1", ViewerID: 42}),
		WithDurable(),
	)
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	if got := ev.CorrelationKey(); got != "play:p1" {
		t.Fatalf("CorrelationKey = %q, want play:p1", got)
	}
	ev.Correlation.Key = "custom:key"
	if got := ev.CorrelationKey(); got != "custom:key" {
		t.Fatalf("explicit CorrelationKey = %q, want custom:key", got)
	}
	if !ev.Meta.Durable {
		t.Fatal("expected Durable=true")
	}

	got, err := DecodePayload[payload](ev)
	if err != nil || got.TitleName != "Dune" || got.BitrateKbps != 18500 {
		t.Fatalf("DecodePayload = %+v, err=%v", got, err)
	}
	m, err := ev.PayloadMap()
	if err != nil {
		t.Fatalf("PayloadMap: %v", err)
	}
	num, ok := m["bitrateKbps"].(json.Number)
	if !ok || num.String() != "18500" {
		t.Fatalf("PayloadMap bitrateKbps = %#v", m["bitrateKbps"])
	}

	if _, err := NewEnvelope("playback.session.started", []string{"not-an-object"}); err == nil {
		t.Fatal("expected error for non-object payload")
	}
}

type sampleClient struct {
	Platform string `json:"platform" event:"label=Platform;example=tvOS"`
	Token    string `json:"token" event:"secret"`
}

type samplePlaybackPayload struct {
	TitleName   string       `json:"titleName" event:"label=Title;example=The Matrix"`
	Username    string       `json:"username" event:"label=Viewer;pii;example=alex"`
	BitrateKbps int          `json:"bitrateKbps" event:"unit=kbps;example=12000"`
	Delivery    string       `json:"delivery" event:"enum=direct_play|remux|transcode;example=direct_play"`
	DurationMS  int64        `json:"durationMs" event:"duration_ms;unit=ms;example=7200000"`
	Tags        []string     `json:"tags" event:"example=4k,hdr"`
	StartedAt   time.Time    `json:"startedAt"`
	Client      sampleClient `json:"client"`
	Ignored     string       `json:"-"`
	unexported  string       //nolint:unused // verifies RegisterStruct skips unexported fields
}

func TestRegistryRegisterStructAndValidation(t *testing.T) {
	reg := NewRegistry()
	err := RegisterStruct[samplePlaybackPayload](reg, EventSchema{
		Type:        "playback.session.started",
		Label:       "Playback started",
		Description: "A viewer started playing media.",
		LegacyNames: []string{"playback.started", "playback.start"},
		Fields: []FieldSchema{
			{Name: "delivery", Description: "Resolved delivery strategy"},
		},
	})
	if err != nil {
		t.Fatalf("RegisterStruct: %v", err)
	}
	if reg.Version() != 1 {
		t.Fatalf("version = %d, want 1", reg.Version())
	}

	schema, ok := reg.Get("playback.session.started")
	if !ok {
		t.Fatal("schema not found")
	}
	if schema.Domain != "playback" || schema.Owner != OwnerBuiltin || schema.Version != 1 {
		t.Fatalf("unexpected schema meta: %+v", schema)
	}

	delivery, ok := schema.Field("delivery")
	if !ok || delivery.Type != FieldEnum || len(delivery.Enum) != 3 || delivery.Description != "Resolved delivery strategy" {
		t.Fatalf("unexpected delivery field: %+v", delivery)
	}
	userField, ok := schema.Field("username")
	if !ok || !userField.PII {
		t.Fatalf("expected username PII=true, got %+v", userField)
	}
	tokField, ok := schema.Field("client.token")
	if !ok || !tokField.Secret {
		t.Fatalf("expected client.token Secret=true, got %+v", tokField)
	}
	if _, exists := schema.Field("Ignored"); exists {
		t.Fatal("json:\"-\" field should be skipped")
	}
	if _, exists := schema.Field("unexported"); exists {
		t.Fatal("unexported field should be skipped")
	}

	// Legacy lookup
	if byLegacy, ok := reg.GetByLegacy("playback.start"); !ok || byLegacy.Type != "playback.session.started" {
		t.Fatalf("GetByLegacy = %+v, ok=%v", byLegacy, ok)
	}

	// Duplicate rejected
	if err := reg.Register(schema); !errors.Is(err, ErrDuplicateSchema) {
		t.Fatalf("duplicate Register = %v, want ErrDuplicateSchema", err)
	}

	// Payload validation: valid + additive extra field
	good := json.RawMessage(`{"titleName":"Alien","bitrateKbps":9000,"delivery":"remux","tags":["hdr"],"startedAt":"2026-11-19T23:00:00Z","extraFutureField":123}`)
	if err := reg.ValidatePayload("playback.session.started", good); err != nil {
		t.Fatalf("ValidatePayload(good) = %v", err)
	}
	// Bad enum
	badEnum := json.RawMessage(`{"delivery":"carrier_pigeon"}`)
	if err := reg.ValidatePayload("playback.session.started", badEnum); err == nil {
		t.Fatal("expected enum validation error")
	}
	// Bad int
	badInt := json.RawMessage(`{"bitrateKbps":"fast"}`)
	if err := reg.ValidatePayload("playback.session.started", badInt); err == nil {
		t.Fatal("expected int validation error")
	}

	// Redaction
	rawMap := map[string]any{
		"username": "alex",
		"client":   map[string]any{"platform": "tvOS", "token": "super-secret"},
	}
	redacted := schema.Redact(rawMap, RedactOptions{StripPII: false})
	if redacted["client"].(map[string]any)["token"] != RedactedValue {
		t.Fatalf("secret not redacted: %+v", redacted)
	}
	if redacted["username"] != "alex" {
		t.Fatalf("username should stay when StripPII=false: %+v", redacted)
	}
	stripped := schema.Redact(rawMap, RedactOptions{StripPII: true})
	if _, hasUser := stripped["username"]; hasUser {
		t.Fatalf("username should be stripped when StripPII=true: %+v", stripped)
	}
	// Original untouched
	if rawMap["client"].(map[string]any)["token"] != "super-secret" {
		t.Fatal("Redact mutated input map")
	}
}

func TestRegistryPluginLifecycle(t *testing.T) {
	reg := NewRegistry()
	err := reg.ReplacePlugin("com.acme.alerts", []EventSchema{
		{
			Type:  PluginEventType("com.acme.alerts", "door.opened"),
			Label: "Door opened",
			Fields: []FieldSchema{
				{Name: "door", Type: FieldString, Label: "Door"},
			},
		},
	})
	if err != nil {
		t.Fatalf("ReplacePlugin: %v", err)
	}
	if _, ok := reg.Get("plugin:com.acme.alerts/door.opened"); !ok {
		t.Fatal("plugin event not registered")
	}
	// Replacing with a new set removes old events atomically
	err = reg.ReplacePlugin("com.acme.alerts", []EventSchema{
		{
			Type:  PluginEventType("com.acme.alerts", "door.closed"),
			Label: "Door closed",
		},
	})
	if err != nil {
		t.Fatalf("ReplacePlugin 2: %v", err)
	}
	if _, ok := reg.Get("plugin:com.acme.alerts/door.opened"); ok {
		t.Fatal("old plugin event should be removed")
	}
	if _, ok := reg.Get("plugin:com.acme.alerts/door.closed"); !ok {
		t.Fatal("new plugin event missing")
	}
	reg.RemovePlugin("com.acme.alerts")
	if len(reg.List()) != 0 {
		t.Fatalf("expected empty registry after RemovePlugin, got %d", len(reg.List()))
	}
}

func TestBusPatternsNonBlockingAndDropCounters(t *testing.T) {
	var drops atomic.Int64
	reg := NewRegistry()
	reg.MustRegister(EventSchema{Type: "playback.session.started", Version: 2, Label: "Started"})

	bus := NewBus(BusOptions{
		NodeID:   "node-1",
		Registry: reg,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		OnDrop:   func(_ string, _ Envelope) { drops.Add(1) },
	})
	defer bus.Close()

	exact := bus.Subscribe(SubscriptionOptions{Name: "exact", Buffer: 4, Patterns: []string{"playback.session.started"}})
	prefix := bus.Subscribe(SubscriptionOptions{Name: "prefix", Buffer: 4, Patterns: []string{"playback.*"}})
	slow := bus.Subscribe(SubscriptionOptions{Name: "slow", Buffer: 1, Patterns: []string{"*"}})
	pluginSub := bus.Subscribe(SubscriptionOptions{Name: "plugin", Buffer: 4, Patterns: []string{"plugin:com.acme.x/*"}})

	// Publish 5 events without reading slow -> publisher must NEVER block and drops must increment.
	for range 5 {
		if err := bus.PublishTyped("playback.session.started", map[string]any{"ok": true}); err != nil {
			t.Fatalf("PublishTyped: %v", err)
		}
	}
	if err := bus.PublishTyped("plugin:com.acme.x/ping.sent", map[string]any{"n": 1}); err != nil {
		t.Fatalf("PublishTyped plugin: %v", err)
	}
	// Dry-run event must be ignored by ordinary subscribers.
	bus.Publish(Envelope{Type: "playback.session.started", Meta: EnvelopeMeta{DryRun: true}})

	ev := <-exact.C()
	if ev.Node != "node-1" || ev.Version != 2 || ev.ID == "" || ev.OccurredAt.IsZero() {
		t.Fatalf("envelope defaults not stamped: %+v", ev)
	}
	if got := exact.Stats().Delivered; got != 4 {
		t.Fatalf("exact delivered = %d, want 4 (1 dropped from cap=4)", got)
	}
	if got := exact.Stats().Dropped; got != 1 {
		t.Fatalf("exact dropped = %d, want 1", got)
	}
	if got := prefix.Stats().Delivered; got != 4 {
		t.Fatalf("prefix delivered = %d, want 4", got)
	}
	if got := slow.Stats().Dropped; got != 5 {
		t.Fatalf("slow dropped = %d, want 5 (1 buffered + 5 dropped of 6 real events)", got)
	}
	if got := pluginSub.Stats().Delivered; got != 1 {
		t.Fatalf("pluginSub delivered = %d, want 1", got)
	}
	if drops.Load() == 0 || bus.Stats().Dropped == 0 {
		t.Fatalf("expected non-zero drop count, stats=%+v", bus.Stats())
	}
}

func TestBusSubscribeFuncPanicRecoveryAndConcurrency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := NewBus(BusOptions{NodeID: "n1", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	defer bus.Close()

	var handled atomic.Int32
	bus.SubscribeFunc(ctx, SubscriptionOptions{Name: "panicky", Buffer: 32}, func(_ context.Context, ev Envelope) {
		n := handled.Add(1)
		if n == 1 {
			panic("boom")
		}
	})

	// Race test concurrent publish + subscribe/unsubscribe
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for range 25 {
				sub := bus.Subscribe(SubscriptionOptions{Name: "ephemeral", Buffer: 2})
				bus.Publish(Envelope{Type: "system.health.changed"})
				sub.Unsubscribe()
			}
		}(i)
	}
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for handled.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if handled.Load() < 2 {
		t.Fatalf("subscriber did not survive panic: handled=%d", handled.Load())
	}
}

func TestRecorderHelper(t *testing.T) {
	bus, rec := NewTestBus(t)
	if err := bus.PublishTyped("livetv.input.lost", map[string]any{"channelId": "ch1"}); err != nil {
		t.Fatal(err)
	}
	ev := rec.WaitFor(t, "livetv.input.*", time.Second)
	if ev.Type != "livetv.input.lost" {
		t.Fatalf("got %q", ev.Type)
	}
	rec.AssertNone(t, "playback.*", 20*time.Millisecond)
	rec.Reset()
	if len(rec.Events()) != 0 {
		t.Fatal("Reset did not clear events")
	}
}

func TestEventsOutboxMigration(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatalf("OpenMemory: %v", err)
	}
	defer db.Close()

	now := time.Now().UnixMilli()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO events_outbox(event_id,event_type,version,occurred_at_ms,node_id,correlation_key,envelope_json,created_at_ms) VALUES(?,?,?,?,?,?,?,?)`,
		"evt_1", "playback.session.started", 1, now, "node-1", "play:p1", `{}`, now,
	); err != nil {
		t.Fatalf("insert events_outbox: %v", err)
	}

	pg := store.TranslateDDL(eventsOutboxMigration1)
	if strings.Contains(strings.ToUpper(pg), "AUTOINCREMENT") {
		t.Fatalf("TranslateDDL left AUTOINCREMENT in postgres DDL:\n%s", pg)
	}
	if !strings.Contains(pg, "BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY") {
		t.Fatalf("TranslateDDL missing identity PK:\n%s", pg)
	}
}
