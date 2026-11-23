package flows

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTemplateValidationAndRendering(t *testing.T) {
	ctx := EvalContext{
		Event: map[string]any{"type": "playback.session.failed", "node": "node-1"},
		Payload: map[string]any{
			"titleName":    "  Blade Runner 2049  ",
			"showTitle":    "Severance",
			"mediaType":    "episode",
			"season":       2,
			"episode":      5,
			"year":         2025,
			"sizeBytes":    json.Number("10737418240"),
			"latencyMs":    2500,
			"watchedRatio": 0.875,
			"viewerId":     42,
			"actors":       []string{"adam scott", "britt lower", "zach cherry", "john turturro"},
			"tags":         []string{"4k", "hdr"},
			"stream": map[string]any{
				"delivery":          "transcode",
				"videoDynamicRange": "hdr10",
				"hwEncode":          true,
			},
			"network": map[string]any{
				"location": "wan",
			},
		},
		Correlation: map[string]any{"sessionId": "sess_9"},
		Vars:        map[string]any{"reason": "bad source"},
	}

	got, err := RenderTemplate(
		`[{{ event.node | upper }}] {{ payload.titleName | trim | truncate 14 }} ({{ payload.sizeBytes | bytes }}, {{ payload.latencyMs | duration }}, {{ payload.watchedRatio | percent 1 }}) - {{ payload.missing | default "n/a | fallback" }}`,
		ctx,
	)
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	want := "[NODE-1] Blade Runner … (10.00 GiB, 2.5s, 87.5%) - n/a | fallback"
	if got != want {
		t.Fatalf("RenderTemplate =\n %q\nwant:\n %q", got, want)
	}

	// List slicing, join, title, first, last, count, percent
	castOut, err := RenderTemplate(
		`Cast ({{ payload.actors | count }}): {{ payload.actors | slice 0 3 | join " / " | title }} (lead: {{ payload.actors | first | title }}, last: {{ payload.actors | last | title }})`,
		ctx,
	)
	if err != nil {
		t.Fatalf("cast RenderTemplate: %v", err)
	}
	wantCast := "Cast (4): Adam Scott / Britt Lower / Zach Cherry (lead: Adam Scott, last: John Turturro)"
	if castOut != wantCast {
		t.Fatalf("castOut =\n %q\nwant:\n %q", castOut, wantCast)
	}

	// Inline conditional blocks (#if, #else, #unless, nested) — covers movie/episode/HDR/transcode cleanly.
	condTmpl := `{{#if payload.mediaType == "episode"}}{{ payload.showTitle }} S{{ payload.season }}E{{ payload.episode }}{{#else}}{{ payload.titleName | trim }} ({{ payload.year }}){{/if}}{{#if payload.stream.videoDynamicRange != "sdr"}} [{{ payload.stream.videoDynamicRange | upper }}]{{/if}}{{#if payload.stream.delivery == "transcode"}} • Transcode{{#if payload.stream.hwEncode}} (HW){{/if}}{{/if}}{{#unless payload.network.location == "lan"}} • Remote{{/unless}}`
	condOut, err := RenderTemplate(condTmpl, ctx)
	if err != nil {
		t.Fatalf("cond RenderTemplate: %v", err)
	}
	wantCond := "Severance S2E5 [HDR10] • Transcode (HW) • Remote"
	if condOut != wantCond {
		t.Fatalf("condOut =\n %q\nwant:\n %q", condOut, wantCond)
	}

	// Flip to movie / SDR / Direct Play / LAN and verify the same single template renders cleanly.
	movieCtx := EvalContext{
		Payload: map[string]any{
			"titleName": "Dune",
			"mediaType": "movie",
			"year":      2021,
			"stream": map[string]any{
				"delivery":          "direct_play",
				"videoDynamicRange": "sdr",
				"hwEncode":          false,
			},
			"network": map[string]any{"location": "lan"},
		},
	}
	movieOut, err := RenderTemplate(condTmpl, movieCtx)
	if err != nil {
		t.Fatalf("movie RenderTemplate: %v", err)
	}
	if movieOut != "Dune (2021)" {
		t.Fatalf("movieOut = %q, want %q", movieOut, "Dune (2021)")
	}

	jsonOut, err := RenderTemplate(`{{ payload.tags | json }}`, ctx)
	if err != nil || jsonOut != `["4k","hdr"]` {
		t.Fatalf("json filter = %q, err=%v", jsonOut, err)
	}

	// RenderParams preserves native type for standalone "{{ path }}".
	rendered, err := RenderParams(map[string]any{
		"viewerId": "{{ payload.viewerId }}",
		"message":  "Stopped {{ payload.titleName | trim }}: {{ vars.reason }}",
		"nested": map[string]any{
			"session": "{{ correlation.sessionId }}",
		},
		"list": []any{"{{ payload.tags | lower }}"},
	}, ctx)
	if err != nil {
		t.Fatalf("RenderParams: %v", err)
	}
	if id, ok := rendered["viewerId"].(int); !ok || id != 42 {
		t.Fatalf("expected native int 42, got %#v", rendered["viewerId"])
	}
	if rendered["message"] != "Stopped Blade Runner 2049: bad source" {
		t.Fatalf("unexpected message: %v", rendered["message"])
	}
	if rendered["nested"].(map[string]any)["session"] != "sess_9" {
		t.Fatalf("unexpected nested session: %#v", rendered["nested"])
	}

	// Invalid templates
	badTemplates := []string{
		"{{ unclosed",
		"extra }}",
		"{{ }}",
		"{{ payload.title | unknownFilter }}",
		"{{ payload.title | truncate abc }}",
		"{{ payload.title | truncate 0 }}",
		"{{ payload.actors | slice -1 2 }}",
		"{{ payload.actors | slice 3 1 }}",
		"{{ payload.watchedRatio | percent 9 }}",
		"{{#if payload.mediaType == \"movie\"}}missing end",
		"{{#if payload.mediaType}}a{{#else}}b{{#else}}c{{/if}}",
		"{{#unless payload.x}}mismatch{{/if}}",
		"{{/if}}",
		"{{#else}}",
		"{{#if payload.a ~= \"b\"}}bad op{{/if}}",
		"{{#if payload.a == unquoted}}bad literal{{/if}}",
		"{{#loop payload.actors}}no loops{{/loop}}",
		strings.Repeat("{{#if payload.x}}", 10) + "deep" + strings.Repeat("{{/if}}", 10),
	}
	for _, tmpl := range badTemplates {
		if err := ValidateTemplate(tmpl); err == nil {
			t.Errorf("ValidateTemplate(%q) expected error, got nil", tmpl)
		}
	}
}
