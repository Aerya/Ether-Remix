package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

func TestInstanceWaitDoesNotCancelJobs(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := env.installHello(t, "1.0.0")

	jobEntered := make(chan struct{})
	releaseJob := make(chan struct{})
	env.fake.mu.Lock()
	env.fake.routes["/ether/job/work"] = func(Request) (Response, error) {
		close(jobEntered)
		<-releaseJob
		return Response{Status: 200, Body: json.RawMessage(`{"done":true}`)}, nil
	}
	env.fake.routes["/ether/v1/kinds/books/resolve"] = func(Request) (Response, error) {
		return Response{}, &wasm.Error{
			Status:  wasm.StatusTimeout,
			Message: wasm.ErrPoolBusy.Error(),
			Err:     wasm.ErrPoolBusy,
		}
	}
	env.fake.mu.Unlock()

	job, err := env.m.startResolverJob(rec.ID, json.RawMessage(`{"job":"/ether/job/work"}`), nil, "")
	if err != nil || job == nil {
		t.Fatalf("startResolverJob: job=%v err=%v", job, err)
	}
	<-jobEntered

	_, callErr := env.m.Call(ctx, rec.ID, Request{
		Method: "POST",
		Path:   "/ether/v1/kinds/books/resolve",
		Body:   json.RawMessage(`{"item":{"ref":"p:books:1"}}`),
	})
	if !errors.Is(callErr, ErrBusy) || !errors.Is(callErr, wasm.ErrPoolBusy) {
		t.Fatalf("Call err = %v, want ErrBusy wrapping wasm.ErrPoolBusy", callErr)
	}
	if state := job.snapshot().State; state != "running" {
		t.Fatalf("job state after pool wait = %q, want running", state)
	}

	gotRec, err := env.m.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !gotRec.Enabled {
		t.Fatal("plugin was disabled after instance-wait timeout")
	}
	health, err := env.m.HealthOf(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if health.Status == "error" || health.Status == "disabled" || health.Restarts > 0 {
		t.Fatalf("instance-wait timeout recorded crash/failure: %+v", health)
	}

	close(releaseJob)
	<-job.done
	if state := job.snapshot().State; state != "done" {
		t.Fatalf("job final state = %q, want done (error=%q)", state, job.snapshot().Error)
	}

	// Verify poolSize manifest requests respect defaults, default ceiling (4),
	// raised owner ceilings (up to MaxPoolSize = 16), and per-plugin overrides.
	base := Manifest{
		ID:      "community.test.pool",
		Version: "1.0.0",
		Ether:   &EtherBlock{Runtime: RuntimeDecl{Kind: "hosted", Entry: "index.js"}},
	}
	if lim := runtimeLimitsWithCeilings(base, defaultRuntimeCeilings()); lim.PoolSize != wasm.DefaultPoolSize {
		t.Fatalf("default PoolSize = %d, want %d", lim.PoolSize, wasm.DefaultPoolSize)
	}
	base.Ether.Runtime.Limits = &RuntimeLimits{PoolSize: 8}
	if lim := runtimeLimitsWithCeilings(base, defaultRuntimeCeilings()); lim.PoolSize != wasm.DefaultPoolCeiling {
		t.Fatalf("default-capped PoolSize = %d, want %d", lim.PoolSize, wasm.DefaultPoolCeiling)
	}
	customCeilings := applyRuntimeCeilings(defaultRuntimeCeilings(), config.PluginRuntimeLimits{PoolSize: 3})
	if lim := runtimeLimitsWithCeilings(base, customCeilings); lim.PoolSize != 3 {
		t.Fatalf("owner-capped PoolSize = %d, want 3", lim.PoolSize)
	}
	raisedCeilings := applyRuntimeCeilings(defaultRuntimeCeilings(), config.PluginRuntimeLimits{PoolSize: 12})
	if lim := runtimeLimitsWithCeilings(base, raisedCeilings); lim.PoolSize != 8 {
		t.Fatalf("raised-ceiling PoolSize = %d, want 8", lim.PoolSize)
	}
	overCapCeilings := applyRuntimeCeilings(defaultRuntimeCeilings(), config.PluginRuntimeLimits{PoolSize: 99})
	if overCapCeilings.poolSize != wasm.MaxPoolSize {
		t.Fatalf("hard-capped ceiling PoolSize = %d, want %d", overCapCeilings.poolSize, wasm.MaxPoolSize)
	}
	baseNoReq := Manifest{
		ID:      "community.test.pool.override",
		Version: "1.0.0",
		Ether:   &EtherBlock{Runtime: RuntimeDecl{Kind: "hosted", Entry: "index.js"}},
	}
	env.m.deps.ReadRuntimeConfig = func() config.Plugins {
		return config.Plugins{
			RuntimeOverrides: map[string]config.PluginRuntimeLimits{
				baseNoReq.ID: {PoolSize: 10},
			},
		}
	}
	if granted := env.m.grantedRuntimeLimits(baseNoReq); granted == nil || granted.PoolSize != 10 {
		t.Fatalf("per-plugin override PoolSize = %+v, want 10", granted)
	}
}

func TestCallTimeoutKeepsPoolAndPluginAlive(t *testing.T) {
	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = eng.Close() })

	var cfg config.Plugins
	env := newTestEnv(t)
	env.m.deps.ReadRuntimeConfig = func() config.Plugins { return cfg }
	env.m.RegisterRuntime("hosted", HostedRuntimeFactory(eng))

	holdEntered := make(chan struct{})
	releaseHold := make(chan struct{})
	env.m.client = &http.Client{Transport: dataRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/hold":
			close(holdEntered)
			select {
			case <-releaseHold:
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader([]byte("ok"))), Header: make(http.Header), Request: r}, nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		case "/hang":
			<-r.Context().Done()
			return nil, r.Context().Err()
		default:
			return &http.Response{StatusCode: 404, Body: io.NopCloser(bytes.NewReader(nil)), Header: make(http.Header), Request: r}, nil
		}
	})}

	var manifestDoc map[string]any
	if err := json.Unmarshal(helloManifest("1.0.0"), &manifestDoc); err != nil {
		t.Fatal(err)
	}
	etherBlock := manifestDoc["ether"].(map[string]any)
	etherBlock["permissions"] = map[string]any{"network": []string{"upstream.example.test"}}
	etherBlock["runtime"].(map[string]any)["limits"] = map[string]any{"poolSize": 2}
	manifestRaw, err := json.Marshal(manifestDoc)
	if err != nil {
		t.Fatal(err)
	}

	files := helloFiles()
	files["dist/plugin.js"] = []byte(`
globalThis.handle = function(req) {
  if (req.path === "/ether/v1/frontend/hold") {
    const r = ether.http.fetch({ url: "https://upstream.example.test/hold" });
    return { status: r.status, body: r.body };
  }
  if (req.path === "/ether/options") {
    const r = ether.http.fetch({ url: "https://upstream.example.test/hang" });
    return { status: r.status, body: r.body };
  }
  return { status: 200, body: "alive" };
};
`)

	ctx := context.Background()
	prev, err := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: manifestRaw, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.m.SetEnabled(ctx, rec.ID, true); err != nil {
		t.Fatal(err)
	}

	// Start a concurrent in-flight call on one pool instance while another call
	// hits its route deadline on the second instance.
	holdDone := make(chan struct {
		resp Response
		err  error
	}, 1)
	go func() {
		resp, callErr := env.m.Call(ctx, rec.ID, Request{Method: "GET", Path: "/ether/v1/frontend/hold"})
		holdDone <- struct {
			resp Response
			err  error
		}{resp: resp, err: callErr}
	}()
	<-holdEntered

	env.m.mu.Lock()
	initialRT := env.m.runtimes[rec.ID]
	env.m.mu.Unlock()
	if initialRT == nil {
		t.Fatal("expected active runtime while call is in flight")
	}

	// /ether/options has a 2s route deadline while /ether/v1/frontend/hold has 30s.
	_, timeoutErr := env.m.Call(ctx, rec.ID, Request{Method: "GET", Path: "/ether/options"})
	var we *wasm.Error
	if !errors.As(timeoutErr, &we) || we.Status != wasm.StatusTimeout {
		t.Fatalf("timed-out call err = %v, want *wasm.Error with StatusTimeout", timeoutErr)
	}

	// The runtime pool stayed alive, so the other in-flight call finishes cleanly.
	close(releaseHold)
	holdRes := <-holdDone
	if holdRes.err != nil || holdRes.resp.Status != http.StatusOK {
		t.Fatalf("concurrent in-flight call failed after peer timeout: resp=%+v err=%v", holdRes.resp, holdRes.err)
	}

	// A follow-up call uses the same runtime and replaces the retired slot.
	okResp, err := env.m.Call(ctx, rec.ID, Request{Method: "GET", Path: "/ether/v1/frontend/ok"})
	if err != nil || okResp.Status != http.StatusOK {
		t.Fatalf("follow-up call after timeout: resp=%+v err=%v", okResp, err)
	}

	// Repeated timeouts past the five-failure auto-disable threshold must not
	// close the runtime or disable the plugin.
	cfg = config.Plugins{
		RuntimeOverrides: map[string]config.PluginRuntimeLimits{
			rec.ID: {Deadline: "60ms"},
		},
	}
	for i := 0; i < 6; i++ {
		if _, err := env.m.Call(ctx, rec.ID, Request{Method: "GET", Path: "/ether/options"}); err == nil {
			t.Fatalf("timeout iteration %d unexpectedly succeeded", i)
		}
	}

	env.m.mu.Lock()
	afterRT := env.m.runtimes[rec.ID]
	env.m.mu.Unlock()
	if afterRT != initialRT {
		t.Fatal("timeouts closed or replaced the plugin runtime")
	}

	gotRec, err := env.m.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !gotRec.Enabled {
		t.Fatal("plugin was disabled after call timeouts")
	}
	health, err := env.m.HealthOf(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if health.Status == "error" || health.Status == "disabled" || health.Restarts > 0 || len(health.Failures) > 0 {
		t.Fatalf("timeouts recorded crash/failure: %+v", health)
	}
	if health.Slow == 0 || health.LastError == "" {
		t.Fatalf("expected timeout health to record slowness and last error: %+v", health)
	}

	// Collateral plugin-closed errors must not count as crash failures.
	closedEnv := newTestEnv(t)
	closedRec := closedEnv.installHello(t, "1.0.0")
	closedEnv.fake.mu.Lock()
	closedEnv.fake.routes["/ether/v1/frontend/closed"] = func(Request) (Response, error) {
		return Response{}, &wasm.Error{Status: wasm.StatusError, Message: wasm.ErrClosed.Error(), Err: wasm.ErrClosed}
	}
	closedEnv.fake.mu.Unlock()
	for i := 0; i < 6; i++ {
		_, err := closedEnv.m.Call(ctx, closedRec.ID, Request{Method: "GET", Path: "/ether/v1/frontend/closed"})
		if !errors.Is(err, wasm.ErrClosed) {
			t.Fatalf("closed call err = %v, want wasm.ErrClosed", err)
		}
	}
	closedHealth, err := closedEnv.m.HealthOf(ctx, closedRec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closedHealth.Status == "error" || closedHealth.Status == "disabled" || closedHealth.Restarts > 0 || len(closedHealth.Failures) > 0 {
		t.Fatalf("collateral closed errors recorded crash/failure: %+v", closedHealth)
	}
}
