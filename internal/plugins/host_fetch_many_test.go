package plugins

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

func setupFetchManyTestHost(t *testing.T, network []string, rt http.RoundTripper) (*testEnv, Record, *host) {
	t.Helper()
	ctx := context.Background()
	env := newTestEnv(t)
	rec := installContent(t, env)
	rec.Manifest.Ether.Permissions.Network = network
	raw, err := json.Marshal(rec.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	rec.ManifestRaw = raw
	if err := env.m.store.upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	env.m.client = &http.Client{Transport: rt}
	return env, rec, env.m.HostFor(rec.ID).(*host)
}

func TestHTTPFetchManyHostedOverlapOrderAndEgress(t *testing.T) {
	ctx := context.Background()

	// Barrier proves two requests from one hosted QuickJS ether.http.fetchMany
	// call are simultaneously in-flight on the host, AND releases request 1
	// before request 0 to prove output ordering matches input order.
	bothArrived := make(chan struct{})
	releaseZero := make(chan struct{})
	var arrived atomic.Int32
	binaryBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0xff, 0x00}

	rt := dataRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/slow":
			if arrived.Add(1) == 2 {
				close(bothArrived)
			}
			select {
			case <-bothArrived:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			select {
			case <-releaseZero:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
				Body:       io.NopCloser(strings.NewReader("first-slow")),
				Request:    r,
			}, nil
		case "/fast":
			if arrived.Add(1) == 2 {
				close(bothArrived)
			}
			select {
			case <-bothArrived:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			defer close(releaseZero)
			return &http.Response{
				StatusCode: http.StatusCreated,
				Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
				Body:       io.NopCloser(bytes.NewReader(binaryBytes)),
				Request:    r,
			}, nil
		default:
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("missing")),
				Request:    r,
			}, nil
		}
	})

	env, rec, h := setupFetchManyTestHost(t, []string{"a.example.test", "b.example.test"}, rt)
	eng := wasm.NewEngine(nil)
	defer func() { _ = eng.Close() }()

	jsSrc := `
function handle(req) {
  const res = ether.http.fetchMany([
    { url: "https://a.example.test/slow", method: "POST", body: "req0" },
    { url: "https://b.example.test/fast" }
  ], { concurrency: 2 });
  return { status: 200, body: res };
}
`
	plug, err := eng.LoadJS(rec.ID, "1.0.0", jsSrc, hostBridge{h: h}, wasm.Limits{
		Deadline:   5 * time.Second,
		UnitBudget: 50_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plug.Close()

	resp, meta, err := plug.CallDetailed(ctx, wasm.Request{Method: "GET", Path: "/ether/v1/kinds/recording/resolve"})
	if err != nil {
		t.Fatalf("hosted fetchMany failed: %v (meta=%+v)", err, meta)
	}
	if resp.Status != 200 {
		t.Fatalf("status=%d body=%s", resp.Status, string(resp.Body))
	}

	var got []struct {
		Status     int               `json:"status"`
		Headers    map[string]string `json:"headers"`
		Body       string            `json:"body"`
		BodyBase64 string            `json:"bodyBase64"`
		Error      *FetchManyError   `json:"error"`
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		t.Fatalf("decode response: %v (raw=%s)", err, string(resp.Body))
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
	if got[0].Error != nil || got[0].Status != 200 || got[0].Body != "first-slow" {
		t.Fatalf("item[0]=%+v, want status=200 body=first-slow", got[0])
	}
	if got[1].Error != nil || got[1].Status != 201 || got[1].Body != "" {
		t.Fatalf("item[1]=%+v, want status=201 binary bodyBase64", got[1])
	}
	decoded, err := base64.StdEncoding.DecodeString(got[1].BodyBase64)
	if err != nil || !bytes.Equal(decoded, binaryBytes) {
		t.Fatalf("item[1].bodyBase64=%q decoded=%x err=%v, want %x", got[1].BodyBase64, decoded, err, binaryBytes)
	}

	hh, err := env.m.store.health(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hh.Observed["a.example.test"] != 1 || hh.Observed["b.example.test"] != 1 {
		t.Fatalf("observed hosts = %+v, want a=1 b=1", hh.Observed)
	}
	wantEgress := int64(len("req0") + len("first-slow") + len(binaryBytes))
	if hh.EgressBytes != wantEgress {
		t.Fatalf("egressBytes = %d, want %d", hh.EgressBytes, wantEgress)
	}
}

func TestHTTPFetchManyConcurrencyCapPolitenessAndOriginInterleaving(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Saturate host A up to DefaultMaxFetchPerHost (6) while holding all 6
	// open; host B requests placed AFTER 8 host A requests in input order must
	// still start immediately (no head-of-line blocking), and peak concurrency
	// must stay <= DefaultMaxFetchPerHost on A and <= MaxFetchManyConcurrency
	// overall even when caller asks for concurrency: 32.
	var (
		activeTotal atomic.Int32
		peakTotal   atomic.Int32
		activeA     atomic.Int32
		peakA       atomic.Int32
		bDone       atomic.Int32
	)
	recordPeak := func(cur int32, peak *atomic.Int32) {
		for {
			prev := peak.Load()
			if cur <= prev || peak.CompareAndSwap(prev, cur) {
				return
			}
		}
	}
	aFull := make(chan struct{})
	bFinished := make(chan struct{})
	var aFullOnce sync.Once
	var bFinishedOnce sync.Once

	rt := dataRoundTripper(func(r *http.Request) (*http.Response, error) {
		curTotal := activeTotal.Add(1)
		recordPeak(curTotal, &peakTotal)
		defer activeTotal.Add(-1)

		switch r.URL.Hostname() {
		case "a.example.test":
			curA := activeA.Add(1)
			recordPeak(curA, &peakA)
			defer activeA.Add(-1)
			if curA == DefaultMaxFetchPerHost {
				aFullOnce.Do(func() { close(aFull) })
			}
			// Hold host A until both host B requests complete, proving host B
			// was never head-of-line blocked behind host A's 8 queued items.
			select {
			case <-bFinished:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		case "b.example.test":
			// Wait until host A has filled its 6 politeness slots so we observe
			// peak total concurrency = 6 (host A) + 2 (host B) = 8 simultaneously.
			select {
			case <-aFull:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			if bDone.Add(1) == 2 {
				bFinishedOnce.Do(func() { close(bFinished) })
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(r.URL.Path)),
			Request:    r,
		}, nil
	})

	_, _, h := setupFetchManyTestHost(t, []string{"a.example.test", "b.example.test"}, rt)

	reqs := make([]FetchRequest, 0, 10)
	for i := range 8 {
		reqs = append(reqs, FetchRequest{URL: "https://a.example.test/a-" + string(rune('0'+i))})
	}
	reqs = append(reqs,
		FetchRequest{URL: "https://b.example.test/b-0"},
		FetchRequest{URL: "https://b.example.test/b-1"},
	)

	res, err := h.HTTPFetchMany(ctx, reqs, FetchManyOptions{Concurrency: 32})
	if err != nil {
		t.Fatalf("HTTPFetchMany: %v", err)
	}
	if len(res) != len(reqs) {
		t.Fatalf("len=%d, want %d", len(res), len(reqs))
	}
	for i, item := range res {
		if item.Error != nil {
			t.Fatalf("item[%d] unexpected error: %+v", i, item.Error)
		}
	}
	if got := peakA.Load(); got > DefaultMaxFetchPerHost {
		t.Fatalf("peak in-flight on single origin = %d, exceeds politeness limit %d", got, DefaultMaxFetchPerHost)
	}
	if got := peakTotal.Load(); got > MaxFetchManyConcurrency {
		t.Fatalf("peak total in-flight = %d, exceeds cap %d", got, MaxFetchManyConcurrency)
	}
}

func TestHTTPFetchManySharedPerHostPolitenessAcrossConcurrentCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var active atomic.Int32
	var peak atomic.Int32
	gate := make(chan struct{})
	reachedCap := make(chan struct{})
	var reachedOnce sync.Once

	rt := dataRoundTripper(func(r *http.Request) (*http.Response, error) {
		cur := active.Add(1)
		for {
			prev := peak.Load()
			if cur <= prev || peak.CompareAndSwap(prev, cur) {
				break
			}
		}
		defer active.Add(-1)
		if cur == DefaultMaxFetchPerHost {
			reachedOnce.Do(func() { close(reachedCap) })
		}
		select {
		case <-gate:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    r,
		}, nil
	})

	_, _, h := setupFetchManyTestHost(t, []string{"shared.example.test"}, rt)

	reqs := make([]FetchRequest, 6)
	for i := range reqs {
		reqs[i] = FetchRequest{URL: "https://shared.example.test/item"}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.HTTPFetchMany(ctx, reqs, FetchManyOptions{Concurrency: 6})
			errs <- err
		}()
	}

	select {
	case <-reachedCap:
	case <-ctx.Done():
		t.Fatal("timed out waiting for politeness cap to fill")
	}
	// Release all blocked requests and wait for both concurrent batches to finish.
	close(gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent HTTPFetchMany: %v", err)
		}
	}
	if got := peak.Load(); got > DefaultMaxFetchPerHost {
		t.Fatalf("shared per-origin peak = %d, exceeds limit %d", got, DefaultMaxFetchPerHost)
	}
}

func TestHTTPFetchManyPerItemErrorsAndRedirectPolicy(t *testing.T) {
	ctx := context.Background()

	var mu sync.Mutex
	seenHeaders := map[string]http.Header{}
	rt := dataRoundTripper(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		seenHeaders[r.URL.String()] = r.Header.Clone()
		mu.Unlock()

		switch r.URL.String() {
		case "https://example.org/ok":
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
		case "https://example.org/not-found":
			return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("nope")), Request: r}, nil
		case "https://example.org/hang":
			<-r.Context().Done()
			return nil, r.Context().Err()
		case "https://example.org/redir-partner-strip":
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://cdn.partner.net/stripped"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		case "https://cdn.partner.net/stripped":
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("stripped-ok")), Request: r}, nil
		case "https://example.org/redir-partner-keep":
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://cdn.partner.net/kept"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		case "https://cdn.partner.net/kept":
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("kept-ok")), Request: r}, nil
		case "https://example.org/redir-denied":
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://evil.example.net/leak"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		default:
			return &http.Response{StatusCode: http.StatusInternalServerError, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("unexpected")), Request: r}, nil
		}
	})

	_, _, h := setupFetchManyTestHost(t, []string{"example.org", "cdn.partner.net"}, rt)

	reqs := []FetchRequest{
		{URL: "https://example.org/ok"},
		{URL: "https://example.org/not-found"},
		{URL: "https://undeclared.example.net/secret"},
		{URL: "https://example.org/hang", TimeoutMS: 40},
		{
			URL:     "https://example.org/redir-partner-strip",
			Headers: map[string]string{"Authorization": "Bearer secret-token", "Cookie": "sid=1"},
		},
		{
			URL:                   "https://example.org/redir-partner-keep",
			Headers:               map[string]string{"Authorization": "Bearer secret-token"},
			KeepHeadersOnRedirect: []string{"cdn.partner.net"},
		},
		{URL: "https://example.org/redir-denied"},
		{URL: "https://example.org/ok", KeepHeadersOnRedirect: []string{"*"}},
	}

	res, err := h.HTTPFetchMany(ctx, reqs, FetchManyOptions{Concurrency: 8})
	if err != nil {
		t.Fatalf("HTTPFetchMany: %v", err)
	}
	if len(res) != len(reqs) {
		t.Fatalf("len=%d, want %d", len(res), len(reqs))
	}

	// 0: 200 OK
	if res[0].Error != nil || res[0].Response.Status != 200 || string(res[0].Response.Body) != "ok" {
		t.Fatalf("item[0]=%+v", res[0])
	}
	// 1: 404 Not Found is a valid HTTP response, NOT a transport error.
	if res[1].Error != nil || res[1].Response.Status != 404 || string(res[1].Response.Body) != "nope" {
		t.Fatalf("item[1]=%+v, want 404 without error", res[1])
	}
	// 2: undeclared host -> forbidden
	if res[2].Error == nil || res[2].Error.Code != "forbidden" {
		t.Fatalf("item[2]=%+v, want forbidden", res[2])
	}
	// 3: per-item timeoutMs -> timeout while siblings succeed
	if res[3].Error == nil || res[3].Error.Code != string(wasm.StatusTimeout) {
		t.Fatalf("item[3]=%+v, want timeout", res[3])
	}
	// 4: cross-origin redirect without keepHeadersOnRedirect strips credentials (#688)
	if res[4].Error != nil || string(res[4].Response.Body) != "stripped-ok" {
		t.Fatalf("item[4]=%+v", res[4])
	}
	mu.Lock()
	strippedHdr := seenHeaders["https://cdn.partner.net/stripped"]
	keptHdr := seenHeaders["https://cdn.partner.net/kept"]
	mu.Unlock()
	if strippedHdr.Get("Authorization") != "" || strippedHdr.Get("Cookie") != "" {
		t.Fatalf("credentials leaked across redirect: auth=%q cookie=%q", strippedHdr.Get("Authorization"), strippedHdr.Get("Cookie"))
	}
	// 5: cross-origin redirect with granted keepHeadersOnRedirect preserves credentials (#688)
	if res[5].Error != nil || string(res[5].Response.Body) != "kept-ok" {
		t.Fatalf("item[5]=%+v", res[5])
	}
	if keptHdr.Get("Authorization") != "Bearer secret-token" {
		t.Fatalf("expected Authorization kept on granted redirect target, got %q", keptHdr.Get("Authorization"))
	}
	// 6: redirect to ungranted host -> forbidden
	if res[6].Error == nil || res[6].Error.Code != "forbidden" {
		t.Fatalf("item[6]=%+v, want forbidden", res[6])
	}
	// 7: wildcard "*" keepHeadersOnRedirect -> invalid_input
	if res[7].Error == nil || res[7].Error.Code != string(wasm.StatusInvalidInput) {
		t.Fatalf("item[7]=%+v, want invalid_input", res[7])
	}
}

func TestHTTPFetchManyCancellationAndPoolLaneHealth(t *testing.T) {
	started := make(chan struct{}, 2)
	aborted := make(chan struct{}, 2)

	rt := dataRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/quick" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("quick-ok")),
				Request:    r,
			}, nil
		}
		started <- struct{}{}
		<-r.Context().Done()
		aborted <- struct{}{}
		return nil, r.Context().Err()
	})

	_, rec, h := setupFetchManyTestHost(t, []string{"cancel.example.test"}, rt)
	eng := wasm.NewEngine(nil)
	defer func() { _ = eng.Close() }()

	jsSrc := `
function handle(req) {
  if (req.path === "/ether/v1/kinds/recording/resolve") {
    const quick = ether.http.fetchMany([{ url: "https://cancel.example.test/quick" }]);
    return { status: 200, body: quick };
  }
  const res = ether.http.fetchMany([
    { url: "https://cancel.example.test/hang-1" },
    { url: "https://cancel.example.test/hang-2" }
  ], { concurrency: 2 });
  return { status: 200, body: res };
}
`
	plug, err := eng.LoadJS(rec.ID, "1.0.0", jsSrc, hostBridge{h: h}, wasm.Limits{
		PoolSize:   3, // exercises #683 reserved resolve lane + non-resolve/job lanes
		Deadline:   5 * time.Second,
		UnitBudget: 50_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plug.Close()

	jobCtx, jobCancel := context.WithCancel(context.Background())
	jobDone := make(chan error, 1)
	go func() {
		_, _, callErr := plug.CallDetailed(jobCtx, wasm.Request{Method: "POST", Path: "/ether/job/sweep"})
		jobDone <- callErr
	}()

	// Wait until both in-flight requests from the single job call have reached upstream.
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for in-flight fetchMany requests to start")
		}
	}

	// While the background job is blocked in http.fetchMany, the #683 reserved
	// resolve lane must still serve a playback resolve immediately.
	resResp, _, err := plug.CallDetailed(context.Background(), wasm.Request{
		Method: "POST",
		Path:   "/ether/v1/kinds/recording/resolve",
	})
	if err != nil || resResp.Status != 200 {
		t.Fatalf("resolve on reserved lane while job blocked in fetchMany: status=%d err=%v", resResp.Status, err)
	}

	// Cancel the job call and confirm BOTH in-flight upstream fetches abort promptly.
	jobCancel()
	for range 2 {
		select {
		case <-aborted:
		case <-time.After(3 * time.Second):
			t.Fatal("in-flight upstream fetch was not canceled when parent request ended")
		}
	}
	select {
	case callErr := <-jobDone:
		if callErr == nil {
			t.Fatal("expected canceled job call to return error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled job call did not exit promptly")
	}

	// Pool lanes must still be healthy after cancellation.
	resResp2, _, err := plug.CallDetailed(context.Background(), wasm.Request{
		Method: "POST",
		Path:   "/ether/v1/kinds/recording/resolve",
	})
	if err != nil || resResp2.Status != 200 {
		t.Fatalf("resolve after canceled fetchMany: status=%d err=%v", resResp2.Status, err)
	}
}

func TestHTTPFetchManyGovernanceBudgetAndAggregateByteCap(t *testing.T) {
	ctx := context.Background()

	t.Run("charges per-request governance units before touching network", func(t *testing.T) {
		var hits atomic.Int32
		rt := dataRoundTripper(func(r *http.Request) (*http.Response, error) {
			hits.Add(1)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
		})
		_, rec, h := setupFetchManyTestHost(t, []string{"budget.example.test"}, rt)
		eng := wasm.NewEngine(nil)
		defer func() { _ = eng.Close() }()

		jsSrc := `
function handle(req) {
  const res = ether.http.fetchMany([
    { url: "https://budget.example.test/1" },
    { url: "https://budget.example.test/2" },
    { url: "https://budget.example.test/3" }
  ]);
  return { status: 200, body: res };
}
`
		// 3 requests require 10 (base) + 100 (call) + 2*100 (extra) = 310 units.
		tight, err := eng.LoadJS(rec.ID, "1.0.0", jsSrc, hostBridge{h: h}, wasm.Limits{UnitBudget: 250})
		if err != nil {
			t.Fatal(err)
		}
		defer tight.Close()

		_, meta, err := tight.CallDetailed(ctx, wasm.Request{Method: "POST", Path: "/ether/action/run"})
		if err == nil || meta.Status != wasm.StatusGovernance {
			t.Fatalf("expected governance fault with UnitBudget=250, got err=%v status=%s", err, meta.Status)
		}
		if got := hits.Load(); got != 0 {
			t.Fatalf("upstream received %d hits despite governance exhaustion before dispatch", got)
		}
	})

	t.Run("enforces MaxFetchManyBytes across batch and refunds oversized bodies", func(t *testing.T) {
		big := bytes.Repeat([]byte("a"), 6<<20) // 6 MiB each -> 2 fit? no, 6+6=12 > 10 MiB
		small := []byte("small-fits")
		rt := dataRoundTripper(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/big1", "/big2":
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(big)), Request: r}, nil
			default:
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(small)), Request: r}, nil
			}
		})
		_, _, h := setupFetchManyTestHost(t, []string{"size.example.test"}, rt)

		// Concurrency 1 makes ordering deterministic: big1 takes 6 MiB, big2 overflows
		// the 10 MiB aggregate budget and refunds its partial reservation, then small
		// (10 bytes) still fits inside the remaining 4 MiB.
		res, err := h.HTTPFetchMany(ctx, []FetchRequest{
			{URL: "https://size.example.test/big1"},
			{URL: "https://size.example.test/big2"},
			{URL: "https://size.example.test/small"},
		}, FetchManyOptions{Concurrency: 1})
		if err != nil {
			t.Fatalf("HTTPFetchMany: %v", err)
		}
		if res[0].Error != nil || len(res[0].Response.Body) != len(big) {
			t.Fatalf("big1 should succeed: err=%+v len=%d", res[0].Error, len(res[0].Response.Body))
		}
		if res[1].Error == nil || !strings.Contains(res[1].Error.Message, "fetchMany batch response larger than") {
			t.Fatalf("big2 should fail batch byte cap, got %+v", res[1])
		}
		if res[2].Error != nil || string(res[2].Response.Body) != string(small) {
			t.Fatalf("small should still fit after big2 refunded partial bytes, got %+v", res[2])
		}
	})

	t.Run("rejects batch exceeding MaxFetchManyRequests and negative concurrency", func(t *testing.T) {
		_, _, h := setupFetchManyTestHost(t, []string{"v.example.test"}, dataRoundTripper(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("should not run")
		}))
		tooMany := make([]FetchRequest, MaxFetchManyRequests+1)
		for i := range tooMany {
			tooMany[i] = FetchRequest{URL: "https://v.example.test/x"}
		}
		if _, err := h.HTTPFetchMany(ctx, tooMany, FetchManyOptions{}); err == nil {
			t.Fatal("expected error for > MaxFetchManyRequests")
		}
		if _, err := h.HTTPFetchMany(ctx, []FetchRequest{{URL: "https://v.example.test/x"}}, FetchManyOptions{Concurrency: -1}); err == nil {
			t.Fatal("expected error for negative concurrency")
		}
	})
}

func TestPreviewFetchManyAppliesAllowlistAndRedirectPolicy(t *testing.T) {
	ctx := context.Background()
	rt := dataRoundTripper(func(r *http.Request) (*http.Response, error) {
		switch r.URL.String() {
		case "https://allowed.example.test/one":
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("one")), Request: r}, nil
		case "https://allowed.example.test/redir-bad":
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://other.example.test/x"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
	})
	env, _, _ := setupFetchManyTestHost(t, []string{"allowed.example.test"}, rt)

	res, err := env.m.PreviewFetchMany(ctx, []string{"allowed.example.test"}, []FetchRequest{
		{URL: "https://allowed.example.test/one"},
		{URL: "https://blocked.example.test/two"},
		{URL: "https://allowed.example.test/redir-bad"},
	}, FetchManyOptions{Concurrency: 4})
	if err != nil {
		t.Fatalf("PreviewFetchMany: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("len=%d, want 3", len(res))
	}
	if res[0].Error != nil || string(res[0].Response.Body) != "one" {
		t.Fatalf("item[0]=%+v", res[0])
	}
	if res[1].Error == nil || res[1].Error.Code != "forbidden" {
		t.Fatalf("item[1]=%+v, want forbidden", res[1])
	}
	if res[2].Error == nil || res[2].Error.Code != "forbidden" {
		t.Fatalf("item[2]=%+v, want forbidden on redirect", res[2])
	}
}
