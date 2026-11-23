package templates_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/flows/actions"
	"github.com/braedonsaunders/ether/internal/flows/templates"
)

func canonicalTestEventRegistry(t *testing.T) *events.Registry {
	t.Helper()
	reg := events.NewRegistry()

	playbackFields := []events.FieldSchema{
		{Name: "sessionId", Type: events.FieldString},
		{Name: "playId", Type: events.FieldString},
		{Name: "titleKey", Type: events.FieldString},
		{Name: "titleName", Type: events.FieldString},
		{Name: "showTitle", Type: events.FieldString},
		{Name: "season", Type: events.FieldInt},
		{Name: "episode", Type: events.FieldInt},
		{Name: "mediaType", Type: events.FieldEnum, Enum: []events.EnumOption{{Value: "movie"}, {Value: "episode"}, {Value: "live"}, {Value: "trailer"}}},
		{Name: "year", Type: events.FieldInt},
		{Name: "posterUrl", Type: events.FieldString},
		{Name: "source.videoHeight", Type: events.FieldInt},
		{Name: "source.videoResolution", Type: events.FieldString},
		{Name: "source.videoDynamicRange", Type: events.FieldString},
		{Name: "stream.delivery", Type: events.FieldEnum, Enum: []events.EnumOption{{Value: "direct_play"}, {Value: "remux"}, {Value: "transcode"}}},
		{Name: "stream.videoDecision", Type: events.FieldEnum, Enum: []events.EnumOption{{Value: "direct_play"}, {Value: "copy"}, {Value: "transcode"}}},
		{Name: "stream.bitrateKbps", Type: events.FieldInt},
		{Name: "viewer.id", Type: events.FieldString},
		{Name: "viewer.username", Type: events.FieldString, PII: true},
		{Name: "viewer.tags", Type: events.FieldStringList},
		{Name: "client.name", Type: events.FieldString},
		{Name: "client.product", Type: events.FieldString},
		{Name: "network.location", Type: events.FieldEnum, Enum: []events.EnumOption{{Value: "lan"}, {Value: "wan"}}},
	}

	mustReg := func(typ, domain string, extra ...events.FieldSchema) {
		if err := reg.Register(events.EventSchema{
			Type:    typ,
			Version: 1,
			Domain:  domain,
			Owner:   events.OwnerBuiltin,
			Label:   typ,
			Fields:  extra,
		}); err != nil {
			t.Fatalf("register %s: %v", typ, err)
		}
	}

	mustReg("playback.session.started", "playback", playbackFields...)
	mustReg("playback.session.paused", "playback", playbackFields...)
	mustReg("playback.session.resumed", "playback", playbackFields...)
	mustReg("playback.session.stopped", "playback", playbackFields...)
	mustReg("playback.session.failed", "playback", append(append([]events.FieldSchema(nil), playbackFields...), events.FieldSchema{Name: "error", Type: events.FieldString})...)
	mustReg("playback.source.failover", "playback", append(append([]events.FieldSchema(nil), playbackFields...), events.FieldSchema{Name: "reason", Type: events.FieldString})...)
	mustReg("playback.stream.stalled", "playback", playbackFields...)
	mustReg("playback.stream.decision", "playback", playbackFields...)
	mustReg("playback.marker.entered", "playback", append(append([]events.FieldSchema(nil), playbackFields...), events.FieldSchema{Name: "markerKind", Type: events.FieldEnum, Enum: []events.EnumOption{{Value: "intro"}, {Value: "credits"}, {Value: "commercial"}, {Value: "recap"}}})...)
	mustReg("playback.concurrency.exceeded", "playback", append(append([]events.FieldSchema(nil), playbackFields...),
		events.FieldSchema{Name: "limit", Type: events.FieldInt},
		events.FieldSchema{Name: "activeStreams", Type: events.FieldInt},
		events.FieldSchema{Name: "distinctActiveIPs", Type: events.FieldInt},
	)...)

	mustReg("system.server.crashed", "system", events.FieldSchema{Name: "exitReason", Type: events.FieldString})
	mustReg("system.health.changed", "system", events.FieldSchema{Name: "component", Type: events.FieldString}, events.FieldSchema{Name: "summary", Type: events.FieldString})
	mustReg("system.reachability.changed", "system", events.FieldSchema{Name: "error", Type: events.FieldString})
	mustReg("system.backup.failed", "system", events.FieldSchema{Name: "error", Type: events.FieldString})
	mustReg("system.disk.low", "system", events.FieldSchema{Name: "volume", Type: events.FieldString})
	mustReg("system.update.available", "system", events.FieldSchema{Name: "newVersion", Type: events.FieldString})
	mustReg("system.update.installed", "system", events.FieldSchema{Name: "toVersion", Type: events.FieldString})
	mustReg("system.update.rolled_back", "system", events.FieldSchema{Name: "restoredVersion", Type: events.FieldString}, events.FieldSchema{Name: "reason", Type: events.FieldString})
	mustReg("provider.service.outage", "provider", events.FieldSchema{Name: "providerId", Type: events.FieldString}, events.FieldSchema{Name: "providerName", Type: events.FieldString}, events.FieldSchema{Name: "error", Type: events.FieldString})
	mustReg("provider.service.recovered", "provider", events.FieldSchema{Name: "providerId", Type: events.FieldString}, events.FieldSchema{Name: "providerName", Type: events.FieldString})
	mustReg("provider.auth.rejected", "provider", events.FieldSchema{Name: "providerId", Type: events.FieldString}, events.FieldSchema{Name: "providerName", Type: events.FieldString}, events.FieldSchema{Name: "error", Type: events.FieldString})
	mustReg("indexer.service.outage", "indexer", events.FieldSchema{Name: "indexerId", Type: events.FieldString}, events.FieldSchema{Name: "indexerName", Type: events.FieldString}, events.FieldSchema{Name: "error", Type: events.FieldString})
	mustReg("indexer.service.recovered", "indexer", events.FieldSchema{Name: "indexerId", Type: events.FieldString}, events.FieldSchema{Name: "indexerName", Type: events.FieldString})
	mustReg("indexer.budget.exhausted", "indexer", events.FieldSchema{Name: "indexerId", Type: events.FieldString}, events.FieldSchema{Name: "indexerName", Type: events.FieldString})
	mustReg("cache.budget.full", "cache", events.FieldSchema{Name: "cacheKind", Type: events.FieldString})
	mustReg("cluster.database.unavailable", "cluster", events.FieldSchema{Name: "error", Type: events.FieldString})
	mustReg("flows.flow.auto_disabled", "flows", events.FieldSchema{Name: "flowId", Type: events.FieldString}, events.FieldSchema{Name: "lastError", Type: events.FieldString})
	mustReg("auth.certificate.renewed", "auth", events.FieldSchema{Name: "issuer", Type: events.FieldString})
	mustReg("auth.session.new_ip", "auth",
		events.FieldSchema{Name: "viewerId", Type: events.FieldString},
		events.FieldSchema{Name: "username", Type: events.FieldString, PII: true},
		events.FieldSchema{Name: "remoteIP", Type: events.FieldString, PII: true},
		events.FieldSchema{Name: "city", Type: events.FieldString},
		events.FieldSchema{Name: "country", Type: events.FieldString},
		events.FieldSchema{Name: "client", Type: events.FieldString},
		events.FieldSchema{Name: "platform", Type: events.FieldString},
	)
	mustReg("auth.device.registered", "auth",
		events.FieldSchema{Name: "deviceId", Type: events.FieldString},
		events.FieldSchema{Name: "viewerId", Type: events.FieldString},
		events.FieldSchema{Name: "username", Type: events.FieldString, PII: true},
		events.FieldSchema{Name: "remoteIP", Type: events.FieldString, PII: true},
		events.FieldSchema{Name: "name", Type: events.FieldString},
		events.FieldSchema{Name: "platform", Type: events.FieldString},
	)
	mustReg("resolver.pin.unhealthy", "resolver",
		events.FieldSchema{Name: "titleKey", Type: events.FieldString},
		events.FieldSchema{Name: "titleName", Type: events.FieldString},
		events.FieldSchema{Name: "releaseId", Type: events.FieldString},
		events.FieldSchema{Name: "status", Type: events.FieldString},
		events.FieldSchema{Name: "reason", Type: events.FieldString},
	)
	return reg
}

func TestAllStarterTemplatesValidateAndRenderCleanly(t *testing.T) {
	actReg, err := actions.NewRegistry(actions.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	evReg := canonicalTestEventRegistry(t)
	valOpts := flows.ValidateOptions{
		Events:   evReg,
		Actions:  actReg,
		Triggers: flows.NewTriggerRegistry(),
	}

	all := templates.All()
	if len(all) < 15 {
		t.Fatalf("expected at least 15 starter templates, got %d", len(all))
	}

	// Ensure all 5 notification presets are registered.
	for _, preset := range []string{"critical", "playback", "providers", "updates", "newsletter"} {
		if _, ok := templates.ByNotifyPreset(preset); !ok {
			t.Errorf("missing notification preset %q", preset)
		}
	}

	seenIDs := map[string]bool{}
	sampleEvalCtx := flows.EvalContext{
		Event: map[string]any{
			"id":         "evt_test",
			"type":       "playback.session.started",
			"occurredAt": time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			"node":       "primary",
		},
		Payload: map[string]any{
			"sessionId":  "sess_1",
			"titleKey":   "movie:tmdb:550",
			"titleName":  "Fight Club",
			"mediaType":  "movie",
			"posterUrl":  "https://img.example.test/poster.jpg",
			"markerKind": "credits",
			"source": map[string]any{
				"videoHeight":       2160,
				"videoResolution":   "4k",
				"videoDynamicRange": "hdr10",
			},
			"stream": map[string]any{
				"delivery":      "transcode",
				"videoDecision": "transcode",
				"bitrateKbps":   12000,
			},
			"viewer": map[string]any{
				"id":       "v1",
				"username": "alex",
				"tags":     []any{"family"},
			},
			"client": map[string]any{
				"name":    "Living Room Apple TV",
				"product": "Apple TV",
			},
			"network": map[string]any{
				"location": "wan",
			},
		},
		Steps: map[string]map[string]any{
			"digest": {
				"output": map[string]any{
					"subject":  "Weekly Digest",
					"text":     "2 new movies",
					"html":     "<h1>Digest</h1>",
					"shareUrl": "https://ether.example.test/digest.html",
				},
			},
			"export": {
				"output": map[string]any{
					"format":      "csv",
					"itemCount":   120,
					"sizeBytes":   4096,
					"filename":    "export.csv",
					"downloadUrl": "/api/flows/exports/export.csv",
				},
			},
		},
	}

	for _, tpl := range all {
		t.Run(tpl.ID, func(t *testing.T) {
			if seenIDs[tpl.ID] {
				t.Fatalf("duplicate template ID %q", tpl.ID)
			}
			seenIDs[tpl.ID] = true
			if tpl.Name == "" || tpl.Description == "" || tpl.Category == "" || tpl.Icon == "" {
				t.Fatalf("template %s missing required metadata", tpl.ID)
			}

			// Validate default template graph cleanly (0 errors, 0 warnings!).
			f, err := templates.Instantiate(tpl.ID, nil)
			if err != nil {
				t.Fatalf("Instantiate(%s, nil): %v", tpl.ID, err)
			}
			res := flows.ValidateFlow(f, valOpts)
			if !res.Valid {
				t.Fatalf("template %s failed validation: %+v", tpl.ID, res.Errors)
			}
			if len(res.Warnings) > 0 {
				t.Fatalf("template %s produced validation warnings: %+v", tpl.ID, res.Warnings)
			}

			// Verify every action node's templated params render cleanly.
			for _, n := range f.Graph.Nodes {
				if n.Kind != flows.NodeAction {
					continue
				}
				cfg, err := flows.DecodeConfig[flows.ActionNodeConfig](n)
				if err != nil {
					t.Fatalf("decode action node %s: %v", n.ID, err)
				}
				if _, err := flows.RenderParams(cfg.Params, sampleEvalCtx); err != nil {
					t.Fatalf("node %s RenderParams error: %v", n.ID, err)
				}
			}
		})
	}
}

func TestInstantiateCustomOverridesAndIsolation(t *testing.T) {
	actReg, err := actions.NewRegistry(actions.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	evReg := canonicalTestEventRegistry(t)
	valOpts := flows.ValidateOptions{Events: evReg, Actions: actReg, Triggers: flows.NewTriggerRegistry()}

	// 1. Custom remote transcode blocker (block all remote transcodes, custom channel + message).
	f, err := templates.Instantiate("jbops-kill-remote-video-transcodes", map[string]any{
		"name":           "No Remote Transcodes",
		"minVideoHeight": 0,
		"exemptTag":      "admin",
		"viewerMessage":  "No remote transcoding allowed.",
		"channelIds":     []string{"ops-discord"},
	})
	if err != nil {
		t.Fatalf("Instantiate: %v", err)
	}
	if f.Name != "No Remote Transcodes" || f.TemplateID != "jbops-kill-remote-video-transcodes" {
		t.Fatalf("flow metadata = %+v", f)
	}
	if res := flows.ValidateFlow(f, valOpts); !res.Valid {
		t.Fatalf("custom transcode flow invalid: %+v", res.Errors)
	}

	// 2. Cinema lights webhook with empty clientName (applies to all players -> omits filter node).
	f2, err := templates.Instantiate("jbops-cinema-lights-webhook", map[string]any{
		"webhookUrl":  "https://ha.example.test/api/webhook/lights",
		"clientName":  "",
		"bearerToken": "secret-token",
	})
	if err != nil {
		t.Fatalf("Instantiate cinema webhook: %v", err)
	}
	if res := flows.ValidateFlow(f2, valOpts); !res.Valid {
		t.Fatalf("custom cinema webhook invalid: %+v", res.Errors)
	}

	// 3. Buffering alert with stopLowestPriority=false omits relief node cleanly.
	f3, err := templates.Instantiate("jbops-buffering-alert-relieve", map[string]any{
		"stallThreshold":     5,
		"stallWindow":        "3m",
		"stopLowestPriority": false,
		"channelIds":         []string{"disc"},
	})
	if err != nil {
		t.Fatalf("Instantiate buffering alert: %v", err)
	}
	if res := flows.ValidateFlow(f3, valOpts); !res.Valid || len(res.Warnings) > 0 {
		t.Fatalf("custom buffering flow invalid: errors=%+v warnings=%+v", res.Errors, res.Warnings)
	}

	// 4. Unknown template and invalid enum parameter rejected.
	if _, err := templates.Instantiate("does-not-exist", nil); err == nil {
		t.Fatal("expected error for unknown template")
	}
	if _, err := templates.Instantiate("jbops-scheduled-library-export", map[string]any{"format": "pdf"}); err == nil {
		t.Fatal("expected error for invalid export format enum")
	}

	// 5. Mutating returned template does not pollute catalog.
	tpl, ok := templates.ByID("notify-critical-alerts")
	if !ok {
		t.Fatal("expected notify-critical-alerts")
	}
	origJSON, _ := json.Marshal(tpl.Graph)
	tpl.Graph.Nodes[0].Label = "MUTATED"
	tpl2, _ := templates.ByID("notify-critical-alerts")
	afterJSON, _ := json.Marshal(tpl2.Graph)
	if string(origJSON) != string(afterJSON) {
		t.Fatal("catalog template graph was mutated across lookups")
	}
}
