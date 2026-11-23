package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/store"
)

func fetchPluginJobs(t *testing.T, m *Manager, pluginID string) []ActionJobStatus {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/"+pluginID+"/jobs", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /%s/jobs = %d: %s", pluginID, rec.Code, rec.Body.String())
	}
	var out []ActionJobStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode jobs: %v", err)
	}
	return out
}

func TestScheduleStartsContinuationJobAndNeverBlocksPass(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	env.m.now = func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return now
	}
	env.m.store.now = env.m.now
	env.m.deps.Now = env.m.now
	advance := func(d time.Duration) {
		nowMu.Lock()
		now = now.Add(d)
		nowMu.Unlock()
	}

	// Plugin A has a slow scheduled route that blocks until released, then returns {job: "/ether/job/import"}.
	manA := []byte(`{
		"id": "community.example.games-sync",
		"version": "1.0.0",
		"name": "Games sync",
		"ether": {
			"protocol": 1,
			"kind": "extension",
			"summary": "Imports game catalogues",
			"runtime": {"kind": "hosted", "entry": "dist/plugin.js"},
			"schedule": [{"every": "15m", "call": "/ether/sync"}]
		}
	}`)
	prevA, err := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: manA, Files: map[string][]byte{"dist/plugin.js": []byte("// sync")}})
	if err != nil {
		t.Fatal(err)
	}
	recA, err := env.m.Install(ctx, prevA, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}

	// Plugin B is a healthy neighbour whose health check & outbox delivery must not stall on A's schedule call.
	recB := env.installHello(t, "1.0.0")
	env.m.Publish(ctx, Event{Name: "playback.started", Payload: json.RawMessage(`{"titleId":"tt0133093"}`)})

	callEntered := make(chan struct{})
	releaseCall := make(chan struct{})
	var syncCalls atomic.Int32

	jobStepEntered := make(chan struct{}, 4)
	releaseJobStep := make(chan struct{})
	var jobCalls atomic.Int32

	env.fake.routes["/ether/sync"] = func(Request) (Response, error) {
		n := syncCalls.Add(1)
		if n == 1 {
			close(callEntered)
			<-releaseCall
		}
		return Response{Status: 200, Body: json.RawMessage(`{"job":"import-platforms"}`)}, nil
	}
	env.fake.routes["/ether/job/import-platforms"] = func(req Request) (Response, error) {
		step := jobCalls.Add(1)
		select {
		case jobStepEntered <- struct{}{}:
		default:
		}
		if step == 1 {
			<-releaseJobStep
			return Response{Status: 200, Body: json.RawMessage(`{"continueInMs":0,"continuation":{"page":2},"progress":"Platform 1 of 2"}`)}, nil
		}
		var payload struct {
			Continuation struct {
				Page int `json:"page"`
			} `json:"continuation"`
		}
		_ = json.Unmarshal(req.Body, &payload)
		if payload.Continuation.Page != 2 {
			return Response{Status: 500, Body: json.RawMessage(`{"failed":"missing continuation"}`)}, nil
		}
		return Response{Status: 200, Body: json.RawMessage(`{"done":true,"progress":"Imported 2 platforms"}`)}, nil
	}

	// 1. runOnce returns promptly while /ether/sync is blocked, and still delivers B's outbox.
	lastHealth := map[string]time.Time{}
	donePass := make(chan struct{})
	go func() {
		env.m.runOnce(ctx, lastHealth)
		close(donePass)
	}()
	select {
	case <-callEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled call did not start")
	}
	select {
	case <-donePass:
	case <-time.After(2 * time.Second):
		t.Fatal("runOnce blocked on slow scheduled call")
	}
	if rem, _, err := env.m.store.outboxBatch(ctx, recB.ID, 10); err != nil || len(rem) != 0 {
		t.Fatalf("neighbour outbox not delivered during slow scheduled call: rem=%d err=%v", len(rem), err)
	}

	// 2. Next tick (even after 15m) must NOT overlap while the first /ether/sync call is still in flight.
	advance(16 * time.Minute)
	env.m.runOnce(ctx, lastHealth)
	if got := syncCalls.Load(); got != 1 {
		t.Fatalf("overlapping schedule call started while first call in flight: got %d", got)
	}

	// 3. Release /ether/sync: it returns {job: "import-platforms"}, starting a continuation job.
	close(releaseCall)
	env.m.waitSchedule()
	select {
	case <-jobStepEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("continuation job from scheduled call did not start")
	}

	// 4. While the continuation job is still running, another 15m tick must NOT start another call or job.
	advance(16 * time.Minute)
	env.m.runOnce(ctx, lastHealth)
	env.m.waitSchedule()
	if got := syncCalls.Load(); got != 1 {
		t.Fatalf("schedule fired again while continuation job was still running: got %d", got)
	}

	// 5. Release step 1 of the continuation job and wait for completion + progress snapshot.
	close(releaseJobStep)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		jobs := fetchPluginJobs(t, env.m, recA.ID)
		if len(jobs) == 1 && jobs[0].State == "done" {
			if jobs[0].Path != "/ether/job/import-platforms" || jobs[0].Progress != "Imported 2 platforms" || jobs[0].Attempts != 2 {
				t.Fatalf("unexpected completed scheduled job snapshot: %+v", jobs[0])
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if jobs := fetchPluginJobs(t, env.m, recA.ID); len(jobs) != 1 || jobs[0].State != "done" {
		t.Fatalf("scheduled continuation job did not finish cleanly: %+v", jobs)
	}

	// 6. Once the continuation job has finished, the next due tick runs the schedule entry again
	// and Cancel stops a running scheduled job cleanly.
	advance(16 * time.Minute)
	env.m.runOnce(ctx, lastHealth)
	env.m.waitSchedule()
	if got := syncCalls.Load(); got != 2 {
		t.Fatalf("schedule did not resume on due tick after job completion: got %d, want 2", got)
	}

	// Verify explicit job cancellation via POST /{id}/jobs/cancel works on scheduled jobs too.
	blockCancelJob := make(chan struct{})
	cancelEntered := make(chan struct{})
	env.fake.mu.Lock()
	env.fake.routes["/ether/job/cancellable"] = func(req Request) (Response, error) {
		close(cancelEntered)
		<-blockCancelJob
		return Response{Status: 200, Body: json.RawMessage(`{"continueInMs":1000}`)}, nil
	}
	env.fake.mu.Unlock()
	if _, err := env.m.startResolverJob(recA.ID, json.RawMessage(`{"job":"/ether/job/cancellable"}`), nil, ""); err != nil {
		t.Fatalf("start cancellable job: %v", err)
	}
	<-cancelEntered
	cancelReq := httptest.NewRequest(http.MethodPost, "/"+recA.ID+"/jobs/cancel", bytes.NewBufferString(`{"path":"/ether/job/cancellable"}`))
	cancelRec := httptest.NewRecorder()
	env.m.Handler().ServeHTTP(cancelRec, cancelReq)
	close(blockCancelJob)
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("cancel job status = %d", cancelRec.Code)
	}
}

func TestResolverJobsSeparateFromOwnerJobsAndJobsBusy(t *testing.T) {
	env := newTestEnv(t)
	rec := env.installHello(t, "1.0.0")
	defer env.m.cancelActionJobs(rec.ID)

	ownerBlock := make(chan struct{})
	ownerEntered := make(chan string, 4)
	defer close(ownerBlock)

	env.fake.routes["/ether/job/owner-"] = func(req Request) (Response, error) {
		ownerEntered <- req.Path
		<-ownerBlock
		return Response{Status: 200, Body: json.RawMessage(`{"done":true}`)}, nil
	}

	// Start 2 owner jobs; a 3rd owner job must still be refused at ActionJobConcurrency = 2.
	if err := env.m.startActionJob(rec.ID, json.RawMessage(`{"job":"/ether/job/owner-1"}`)); err != nil {
		t.Fatalf("owner-1: %v", err)
	}
	if err := env.m.startActionJob(rec.ID, json.RawMessage(`{"job":"/ether/job/owner-2"}`)); err != nil {
		t.Fatalf("owner-2: %v", err)
	}
	for range 2 {
		<-ownerEntered
	}
	if err := env.m.startActionJob(rec.ID, json.RawMessage(`{"job":"/ether/job/owner-3"}`)); err == nil {
		t.Fatal("expected 3rd owner job to be refused")
	}

	// Part A (#590): While 2 owner jobs are running, a Play resolve STILL starts its preparation job and succeeds!
	var quickReady atomic.Bool
	env.fake.routes["/ether/v1/kinds/game/resolve"] = func(req Request) (Response, error) {
		var body struct {
			Item struct {
				Ref string `json:"ref"`
			} `json:"item"`
		}
		_ = json.Unmarshal(req.Body, &body)
		if body.Item.Ref == "pk:game:quick" && quickReady.Load() {
			return Response{Status: 200, Body: json.RawMessage(`{"sources":[{"id":"rom-quick"}]}`)}, nil
		}
		return Response{Status: 200, Body: json.RawMessage(`{"pending":true,"retryInMs":3000,"job":"/ether/job/prepare"}`)}, nil
	}

	resolverBlock := make(chan struct{})
	resolverEntered := make(chan string, 8)
	defer close(resolverBlock)

	env.fake.routes["/ether/job/prepare"] = func(req Request) (Response, error) {
		var body struct {
			Item struct {
				Ref string `json:"ref"`
			} `json:"item"`
		}
		_ = json.Unmarshal(req.Body, &body)
		if body.Item.Ref == "pk:game:quick" {
			quickReady.Store(true)
			return Response{Status: 200, Body: json.RawMessage(`{"done":true}`)}, nil
		}
		resolverEntered <- body.Item.Ref
		<-resolverBlock
		return Response{Status: 200, Body: json.RawMessage(`{"done":true}`)}, nil
	}

	quickBody, _ := json.Marshal(map[string]any{"item": map[string]any{"ref": "pk:game:quick", "id": "quick"}})
	quickResp, err := env.m.Call(context.Background(), rec.ID, Request{
		Method: "POST",
		Path:   "/ether/v1/kinds/game/resolve",
		Body:   quickBody,
	})
	if err != nil || quickResp.Status != http.StatusOK {
		t.Fatalf("Play resolve starved by owner jobs: status=%d err=%v body=%s", quickResp.Status, err, string(quickResp.Body))
	}

	// Part B (#590): Fill all 4 resolver job slots (game-1..game-4).
	ctxHold, cancelHold := context.WithCancel(context.Background())
	defer cancelHold()
	for i := 1; i <= ResolverJobConcurrency; i++ {
		ref := fmt.Sprintf("pk:game:hold-%d", i)
		go func(ref string) {
			raw, _ := json.Marshal(map[string]any{"item": map[string]any{"ref": ref, "id": ref}})
			_, _ = env.m.Call(ctxHold, rec.ID, Request{
				Method: "POST",
				Path:   "/ether/v1/kinds/game/resolve",
				Body:   raw,
			})
		}(ref)
	}
	for range ResolverJobConcurrency {
		select {
		case <-resolverEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("expected 4 resolver jobs to start concurrently alongside 2 owner jobs")
		}
	}

	// Deduplication check: another Play for an item whose resolver job is ALREADY running (hold-1)
	// joins that job instead of failing with jobs_busy.
	shortCtx, cancelShort := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancelShort()
	dupBody, _ := json.Marshal(map[string]any{"item": map[string]any{"ref": "pk:game:hold-1", "id": "pk:game:hold-1"}})
	_, dupErr := env.m.Call(shortCtx, rec.ID, Request{
		Method: "POST",
		Path:   "/ether/v1/kinds/game/resolve",
		Body:   dupBody,
	})
	if errors.Is(dupErr, ErrJobsBusy) {
		t.Fatalf("duplicate Play for already-running item should deduplicate, got jobs_busy: %v", dupErr)
	}

	// Refusal check: a 5th distinct item (hold-5) must NOT swallow the refusal into 202 pending!
	// It returns 503 jobs_busy with Retry-After, X-Ether-Media-Error, honest message, and ErrJobsBusy,
	// AND records a visible error entry on GET /{id}/jobs.
	fifthBody, _ := json.Marshal(map[string]any{"item": map[string]any{"ref": "pk:game:hold-5", "id": "pk:game:hold-5"}})
	busyResp, busyErr := env.m.Call(context.Background(), rec.ID, Request{
		Method: "POST",
		Path:   "/ether/v1/kinds/game/resolve",
		Body:   fifthBody,
	})
	if !errors.Is(busyErr, ErrJobsBusy) || !errors.Is(busyErr, core.ErrJobsBusy) {
		t.Fatalf("5th resolver job err = %v, want ErrJobsBusy", busyErr)
	}
	if busyResp.Status != http.StatusServiceUnavailable {
		t.Fatalf("5th resolver job status = %d, want 503", busyResp.Status)
	}
	if got := busyResp.Headers["X-Ether-Media-Error"]; got != "jobs_busy" {
		t.Fatalf("X-Ether-Media-Error = %q, want jobs_busy", got)
	}
	if got := busyResp.Headers["Retry-After"]; got != "3" {
		t.Fatalf("Retry-After = %q, want 3 (from retryInMs=3000)", got)
	}
	var envErr struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(busyResp.Body, &envErr); err != nil {
		t.Fatalf("decode jobs_busy body: %v (%s)", err, string(busyResp.Body))
	}
	if envErr.Error.Code != "jobs_busy" || envErr.Error.Message == "" {
		t.Fatalf("unexpected jobs_busy payload: %+v", envErr)
	}

	jobs := fetchPluginJobs(t, env.m, rec.ID)
	foundRefused := false
	for _, j := range jobs {
		if j.Path == "/ether/job/prepare" && j.State == "error" && j.Error == envErr.Error.Message {
			foundRefused = true
			break
		}
	}
	if !foundRefused {
		t.Fatalf("expected refused resolver job on GET /%s/jobs, got %+v", rec.ID, jobs)
	}
}

func TestKVUsageMatchesPutAccountingAndHostedGuest(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng := wasm.NewEngine(log)
	t.Cleanup(func() { _ = eng.Close() })

	m, err := New(Deps{DB: db, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	m.RegisterRuntime("hosted", HostedRuntimeFactory(eng))

	guestJS := `
globalThis.handle = function(req) {
  if (req.path === "/ether/health") return { status: 200, body: { status: "ok" } };
  if (req.path === "/kv-check") {
    const empty = ether.kv.usage();
    ether.kv.put("index:snes", "0123456789");
    ether.kv.put("save:slot-1", "abcd");
    const afterTwo = ether.kv.usage();
    ether.kv.put("save:slot-1", "abcdefgh");
    const afterUpdate = ether.kv.usage();
    ether.kv.delete("index:snes");
    const afterDelete = ether.kv.usage();
    return { status: 200, body: { empty, afterTwo, afterUpdate, afterDelete } };
  }
  return { status: 404, body: {} };
};
`
	manA := []byte(`{
		"id": "community.example.kv-games",
		"version": "1.0.0",
		"name": "KV games",
		"ether": {
			"protocol": 1,
			"kind": "extension",
			"summary": "Tracks game save quota",
			"runtime": {"kind": "hosted", "entry": "plugin.js"}
		}
	}`)
	prevA, err := m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: manA, Files: map[string][]byte{"plugin.js": []byte(guestJS)}})
	if err != nil {
		t.Fatal(err)
	}
	recA, err := m.Install(ctx, prevA, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}

	// Install a second plugin and write KV data to verify per-plugin isolation in kvUsage.
	manB := []byte(`{
		"id": "community.example.other",
		"version": "1.0.0",
		"name": "Other plugin",
		"ether": {
			"protocol": 1,
			"kind": "extension",
			"summary": "Other plugin",
			"runtime": {"kind": "hosted", "entry": "plugin.js"}
		}
	}`)
	prevB, err := m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: manB, Files: map[string][]byte{"plugin.js": []byte(guestJS)}})
	if err != nil {
		t.Fatal(err)
	}
	recB, err := m.Install(ctx, prevB, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.HostFor(recB.ID).KVPut(ctx, "unrelated", []byte("1234567890123456")); err != nil {
		t.Fatal(err)
	}

	resp, err := m.Call(ctx, recA.ID, Request{Method: "POST", Path: "/kv-check"})
	if err != nil || resp.Status != 200 {
		t.Fatalf("Call /kv-check: status=%d err=%v body=%s", resp.Status, err, string(resp.Body))
	}
	var got struct {
		Empty       KVUsage `json:"empty"`
		AfterTwo    KVUsage `json:"afterTwo"`
		AfterUpdate KVUsage `json:"afterUpdate"`
		AfterDelete KVUsage `json:"afterDelete"`
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("decode /kv-check: %v", err)
	}

	wantEmpty := KVUsage{Bytes: 0, Keys: 0, QuotaBytes: KVQuotaBytes, QuotaKeys: KVQuotaKeys}
	wantTwo := KVUsage{Bytes: 10 + 4 + 2*KVKeyChargeBytes, Keys: 2, QuotaBytes: KVQuotaBytes, QuotaKeys: KVQuotaKeys}
	wantUpdate := KVUsage{Bytes: 10 + 8 + 2*KVKeyChargeBytes, Keys: 2, QuotaBytes: KVQuotaBytes, QuotaKeys: KVQuotaKeys}
	wantDelete := KVUsage{Bytes: 8 + 1*KVKeyChargeBytes, Keys: 1, QuotaBytes: KVQuotaBytes, QuotaKeys: KVQuotaKeys}

	if got.Empty != wantEmpty {
		t.Fatalf("empty = %+v, want %+v", got.Empty, wantEmpty)
	}
	if got.AfterTwo != wantTwo {
		t.Fatalf("afterTwo = %+v, want %+v", got.AfterTwo, wantTwo)
	}
	if got.AfterUpdate != wantUpdate {
		t.Fatalf("afterUpdate = %+v, want %+v", got.AfterUpdate, wantUpdate)
	}
	if got.AfterDelete != wantDelete {
		t.Fatalf("afterDelete = %+v, want %+v", got.AfterDelete, wantDelete)
	}
}
