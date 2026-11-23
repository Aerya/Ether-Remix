package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

func mediaManifestJSON(mediaPerm string, schedule []map[string]any) []byte {
	etherBlock := map[string]any{
		"protocol":     1,
		"kind":         "extension",
		"summary":      "Probes media metadata in background jobs",
		"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
		"capabilities": []string{},
	}
	if mediaPerm != "" {
		etherBlock["permissions"] = map[string]any{"media": mediaPerm}
	}
	if len(schedule) > 0 {
		etherBlock["schedule"] = schedule
	}
	raw, _ := json.Marshal(map[string]any{
		"id":      "community.example.mediaprobe",
		"version": "1.0.0",
		"name":    "Media Prober",
		"ether":   etherBlock,
	})
	return raw
}

func TestMediaPermissionManifestSheetAndDiff(t *testing.T) {
	bad, err := ParseManifest(mediaManifestJSON("write", nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "ether.permissions.media") {
		t.Fatalf("expected validation error for permissions.media=write, got %v", err)
	}

	noneMan, err := ParseManifest(mediaManifestJSON("none", nil))
	if err != nil || noneMan.Validate() != nil {
		t.Fatalf("permissions.media=none failed: %v %v", err, noneMan.Validate())
	}
	noneLines, _, _ := PermissionSheet(noneMan)
	if slices.Contains(noneLines, "Probes media container metadata") {
		t.Fatalf("permissions.media=none should not appear on PermissionSheet: %v", noneLines)
	}

	probeMan, err := ParseManifest(mediaManifestJSON("probe", nil))
	if err != nil || probeMan.Validate() != nil {
		t.Fatalf("permissions.media=probe failed: %v %v", err, probeMan.Validate())
	}
	probeLines, _, _ := PermissionSheet(probeMan)
	if !slices.Contains(probeLines, "Probes media container metadata") {
		t.Fatalf("PermissionSheet missing media probe line: %v", probeLines)
	}
	added := PermissionDiff(noneMan, probeMan)
	if len(added) != 1 || added[0] != "Probes media container metadata" {
		t.Fatalf("PermissionDiff(none -> probe) = %v", added)
	}
}

func TestHostMediaProbePermissionsRoutePolicyAndRateLimit(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	var backendCalls int
	env.m.SetKindBackends(KindBackends{
		MediaProbe: func(ctx context.Context, pluginID string, src core.PlayableSource) (core.MediaProbeResult, error) {
			backendCalls++
			if _, ok := ctx.Deadline(); !ok {
				return core.MediaProbeResult{}, errors.New("expected bounded probe deadline")
			}
			return core.MediaProbeResult{
				DurationSec: 2400,
				Container:   "mkv",
				Width:       1920,
				Height:      1080,
				SizeBytes:   123456,
			}, nil
		},
	})

	// Install plugin with permissions.media: "probe" and a declared schedule route.
	prev, err := env.m.PreviewInstall(ctx, Source{
		Kind:     "studio",
		Manifest: mediaManifestJSON("probe", []map[string]any{{"every": "1h", "call": "/ether/verify-packs"}}),
		Files:    map[string][]byte{"dist/plugin.js": []byte(`globalThis.handle = function(){ return { status: 200, body: "{}" }; };`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	h := env.m.HostFor(rec.ID)

	sample := core.PlayableSource{
		Pattern: "range",
		MIME:    "video/x-matroska",
		URL:     "https://media.example/ep1.mkv",
	}

	// Allowed job/action/sync/scheduled/background routes succeed.
	for _, path := range []string{
		"",
		"/ether/job/verify",
		"/ether/action/check",
		"/ether/v1/flow-actions/verify",
		"/ether/tool/inspect",
		"/ether/event",
		"/ether/v1/setup/test",
		"/ether/test",
		"/ether/v1/kinds/recordings/browse",
		"/ether/verify-packs",
	} {
		res, err := h.MediaProbe(withCallPath(ctx, path), sample)
		if err != nil {
			t.Fatalf("allowed path %q failed: %v", path, err)
		}
		if res.DurationSec != 2400 || res.Container != "mkv" || res.Width != 1920 || res.Height != 1080 || res.SizeBytes != 123456 {
			t.Fatalf("path %q result = %+v", path, res)
		}
	}

	// Viewer / client request paths are strictly refused.
	for _, path := range []string{
		"/ether/v1/kinds/recordings/resolve",
		"/ether/v1/kinds/recordings/search",
		"/ether/v1/kinds/recordings/match",
		"/ether/v1/kinds/recordings/items",
		"/ether/v1/kinds/recordings/live",
		"/stream/movie/tt123.json",
		"/catalog/movie/top.json",
		"/catalog/movie/top/search=foo.json",
		"/meta/movie/tt123.json",
		"/subtitles/movie/tt123.json",
		"/ether/v1/frontend/opds/feed",
		"/ether/view/page",
		"/ether/health",
		"/ether/configure",
	} {
		if _, err := h.MediaProbe(withCallPath(ctx, path), sample); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("disallowed request path %q: got %v, want ErrPermissionDenied", path, err)
		}
	}

	// Live revocation blocks media.probe immediately.
	if err := env.m.SetGrants(ctx, rec.ID, Grants{Revoked: []string{"media"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.MediaProbe(withCallPath(ctx, "/ether/job/verify"), sample); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("revoked media grant: got %v, want ErrPermissionDenied", err)
	}
	if err := env.m.SetGrants(ctx, rec.ID, Grants{}); err != nil {
		t.Fatal(err)
	}

	// Per-plugin rate limit enforces cap.
	env.m.SetMediaProbeRateLimit(2)
	// Clear prior calls from the allowed-path loop above so we measure fresh window.
	env.m.mu.Lock()
	delete(env.m.mediaProbeTimes, rec.ID)
	env.m.mu.Unlock()
	for i := 0; i < 2; i++ {
		if _, err := h.MediaProbe(withCallPath(ctx, "/ether/job/verify"), sample); err != nil {
			t.Fatalf("rate-limit call %d failed: %v", i+1, err)
		}
	}
	if _, err := h.MediaProbe(withCallPath(ctx, "/ether/job/verify"), sample); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("3rd call under limit 2: got %v, want ErrRateLimited", err)
	}
}

func TestHostedWasmMediaProbeRoutePolicy(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = eng.Close() })
	env.m.RegisterRuntime("hosted", HostedRuntimeFactory(eng))
	env.m.SetKindBackends(KindBackends{
		MediaProbe: func(_ context.Context, _ string, _ core.PlayableSource) (core.MediaProbeResult, error) {
			return core.MediaProbeResult{
				DurationSec: 1800,
				Container:   "mp4",
				Width:       1920,
				Height:      1080,
				SizeBytes:   999999,
			}, nil
		},
	})

	js := `globalThis.handle = function(req) {
  try {
    const res = ether.media.probe({
      id: "ep-1",
      pattern: "range",
      mime: "video/mp4",
      url: "https://media.example/ep1.mp4",
    });
    return { status: 200, body: res };
  } catch (err) {
    return { status: 403, body: { code: err.code || "err", message: String(err) } };
  }
};`

	prev, err := env.m.PreviewInstall(ctx, Source{
		Kind:     "studio",
		Manifest: mediaManifestJSON("probe", nil),
		Files:    map[string][]byte{"dist/plugin.js": []byte(js)},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}

	// Job route succeeds through hosted QuickJS ether.media.probe(source).
	resp, err := env.m.Call(ctx, rec.ID, Request{Method: "POST", Path: "/ether/job/verify"})
	if err != nil {
		t.Fatalf("job Call: %v", err)
	}
	var got core.MediaProbeResult
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("decode job resp %s: %v", resp.Body, err)
	}
	if resp.Status != 200 || got.DurationSec != 1800 || got.Container != "mp4" || got.Width != 1920 || got.Height != 1080 || got.SizeBytes != 999999 {
		t.Fatalf("unexpected hosted job media.probe resp: status=%d got=%+v", resp.Status, got)
	}

	// Resolve request path is refused inside hosted QuickJS with forbidden code.
	resp, err = env.m.Call(ctx, rec.ID, Request{Method: "POST", Path: "/ether/v1/kinds/recordings/resolve"})
	if err != nil {
		t.Fatalf("resolve Call: %v", err)
	}
	var errBody struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(resp.Body, &errBody)
	if resp.Status != 403 || errBody.Code != "forbidden" {
		t.Fatalf("resolve media.probe: status=%d body=%s, want 403 forbidden", resp.Status, resp.Body)
	}
}
