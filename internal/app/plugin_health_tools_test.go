package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

type fakeHealthRuntime struct {
	failBackground bool
	healthBody     string
}

func (f *fakeHealthRuntime) Call(_ context.Context, req plugins.Request) (plugins.Response, error) {
	if req.Path == "/ether/health" {
		body := f.healthBody
		if body == "" {
			body = `{"status":"ok"}`
		}
		return plugins.Response{Status: 200, Body: json.RawMessage(body)}, nil
	}
	if f.failBackground {
		return plugins.Response{}, errors.New("wasm: timeout: call exceeded its deadline")
	}
	return plugins.Response{Status: 200, Body: json.RawMessage(`{}`)}, nil
}

func (f *fakeHealthRuntime) Close() error { return nil }

func TestPluginHealthToolsOmitStaleLastErrorWhenHealthy(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	rt := &fakeHealthRuntime{}
	mgr, err := plugins.New(plugins.Deps{
		DB:       db,
		Registry: extend.New(),
		Logger:   log,
		IsLeader: func() bool { return true },
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.RegisterRuntime("hosted", func(_ plugins.Installed) (plugins.Runtime, error) {
		return rt, nil
	})

	a := &App{
		db:        db,
		pluginsRT: &pluginRuntime{mgr: mgr},
	}
	cat := tools.NewCatalogue()
	if err := a.registerBuiltinTools(cat); err != nil {
		t.Fatal(err)
	}

	rawMan, err := json.Marshal(map[string]any{
		"id":      "community.ppvland.ether",
		"version": "1.0.0",
		"name":    "PPV Land",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "livetv_source",
			"summary":      "Live PPV events",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"livetv_channels"},
			"contributes": map[string]any{
				"channels": map[string]any{"refresh": "2h", "test": true},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	prev, err := mgr.PreviewInstall(ctx, plugins.Source{
		Kind:     "studio",
		Manifest: rawMan,
		Files:    map[string][]byte{"dist/plugin.js": []byte("// stub")},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := mgr.Install(ctx, prev, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}

	owner := tools.Principal{IsOwner: true}
	callTool := func(name string, in map[string]any) map[string]any {
		t.Helper()
		tl, ok := cat.Lookup(ctx, owner, name)
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		res, err := tl.Execute(ctx, owner, in)
		if err != nil {
			t.Fatalf("%s failed: %v", name, err)
		}
		return res.Data
	}

	// 1. Trigger a background call timeout -> health becomes "error" with detail.
	rt.failBackground = true
	if _, err := mgr.Call(ctx, rec.ID, plugins.Request{Method: "GET", Path: "/ether/channels.json"}); err == nil {
		t.Fatal("expected simulated background call failure")
	}
	errData := callTool("plugin_health", map[string]any{"pluginId": rec.ID})
	if errData["health"] != "error" || errData["detail"] != "wasm: timeout: call exceeded its deadline" {
		t.Fatalf("expected error health with timeout detail, got %+v", errData)
	}

	// 2. /ether/health succeeds -> stored Health keeps historical LastError,
	//    but both plugins_list and plugin_health must report "ok" with NO stale detail.
	if _, err := mgr.CheckHealth(ctx, rec.ID); err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}
	stored, err := mgr.HealthOf(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "ok" || stored.LastError == "" {
		t.Fatalf("expected stored status=ok with preserved historical LastError, got %+v", stored)
	}

	okHealth := callTool("plugin_health", map[string]any{"pluginId": rec.ID})
	if okHealth["health"] != "ok" {
		t.Fatalf("expected plugin_health status=ok, got %+v", okHealth)
	}
	if _, hasDetail := okHealth["detail"]; hasDetail {
		t.Fatalf("healthy plugin_health must not expose stale LastError as detail: %+v", okHealth)
	}

	listData := callTool("plugins_list", map[string]any{})
	items, _ := listData["plugins"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 plugin in plugins_list, got %+v", listData)
	}
	item := items[0].(map[string]any)
	if item["health"] != "ok" {
		t.Fatalf("expected plugins_list health=ok, got %+v", item)
	}
	if _, hasDetail := item["detail"]; hasDetail {
		t.Fatalf("healthy plugins_list entry must not expose stale LastError as detail: %+v", item)
	}

	// 3. Attention status exposes current Reason, never stale LastError.
	rt.healthBody = `{"status":"attention","reason":"Upstream feed is rate-limiting."}`
	if _, err := mgr.CheckHealth(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	attHealth := callTool("plugin_health", map[string]any{"pluginId": rec.ID})
	if attHealth["health"] != "attention" || attHealth["detail"] != "Upstream feed is rate-limiting." {
		t.Fatalf("expected attention reason, got %+v", attHealth)
	}

	// 4. Manual disable reports health="disabled" without leaking historical LastError.
	if err := mgr.SetEnabled(ctx, rec.ID, false); err != nil {
		t.Fatal(err)
	}
	disHealth := callTool("plugin_health", map[string]any{"pluginId": rec.ID})
	if disHealth["enabled"] != false || disHealth["health"] != "disabled" {
		t.Fatalf("expected disabled health on manual disable, got %+v", disHealth)
	}
	if _, hasDetail := disHealth["detail"]; hasDetail {
		t.Fatalf("manual disable must not surface stale detail: %+v", disHealth)
	}
}
