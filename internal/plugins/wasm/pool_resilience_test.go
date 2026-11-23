package wasm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type fakeInstance struct {
	handleFn func(ctx context.Context, cs *callState, reqJSON []byte) ([]byte, *Error)
	closed   atomic.Bool
}

func (f *fakeInstance) handle(ctx context.Context, cs *callState, reqJSON []byte) ([]byte, *Error) {
	return f.handleFn(ctx, cs, reqJSON)
}
func (f *fakeInstance) rankOne(context.Context, *callState, []byte, []byte) (int64, *Error) {
	return 0, nil
}
func (f *fakeInstance) condOne(context.Context, *callState, []byte, string, []byte) (bool, *Error) {
	return false, nil
}
func (f *fakeInstance) caps() caps     { return caps{Handle: true} }
func (f *fakeInstance) closeInstance() { f.closed.Store(true) }

func TestPoolReturnsInstanceOnCancelAndTimeoutDuringBlockingHostCall(t *testing.T) {
	t.Parallel()
	eng := NewEngine(slog.New(slog.NewTextHandler(&lockedBuffer{}, nil)))
	defer eng.Close()

	unblockHost := make(chan struct{})
	defer close(unblockHost)
	started := make(chan struct{}, 2)

	// Host ignores ctx completely to verify dispatch never leaves an instance stuck.
	host := HostFunc(func(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
		if name == "http.fetch" {
			started <- struct{}{}
			<-unblockHost
			return json.RawMessage(`{"status":200}`), nil
		}
		return json.RawMessage(`null`), nil
	})

	js := `function handle(req) {
		if (req.path === "/block") {
			ether.http.fetch({ url: "https://example.test" });
			return { status: 200, body: { ok: true } };
		}
		return { status: 200, body: { ok: true } };
	}`
	p, err := eng.LoadJS("blocking-host", "1.0.0", js, host, Limits{
		PoolSize: 1,
		Deadline: 5 * time.Second, // also bounds runtime creation, slow on loaded CI runners
	})
	if err != nil {
		t.Fatalf("LoadJS: %v", err)
	}
	defer p.Close()

	// 1. Caller cancellation mid-blocking-host-call must return/replace the instance.
	cancelCtx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := p.Call(cancelCtx, Request{Method: "GET", Path: "/block"})
		errCh <- err
	}()
	<-started
	cancel()
	if err := <-errCh; err == nil {
		t.Fatal("expected cancelled call to return an error")
	}

	// Pool slot must be available again immediately for a fresh call.
	resp, err := p.Call(context.Background(), Request{Method: "GET", Path: "/ok"})
	if err != nil || resp.Status != 200 {
		t.Fatalf("post-cancel call failed: resp=%+v err=%v pool=%+v", resp, err, p.PoolStats())
	}

	// 2. Call deadline expiry mid-blocking-host-call must return/replace the instance.
	timeoutCtx, tcancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer tcancel()
	_, err = p.Call(timeoutCtx, Request{Method: "GET", Path: "/block"})
	if err == nil {
		t.Fatal("expected timed-out call to return an error")
	}

	resp, err = p.Call(context.Background(), Request{Method: "GET", Path: "/ok"})
	if err != nil || resp.Status != 200 {
		t.Fatalf("post-timeout call failed: resp=%+v err=%v pool=%+v", resp, err, p.PoolStats())
	}

	snap := p.PoolStats()
	if snap.InUse != 0 {
		t.Fatalf("PoolStats.InUse = %d, want 0", snap.InUse)
	}
}

func TestPoolReturnsInstanceAndSlotOnPanic(t *testing.T) {
	t.Parallel()
	eng := NewEngine(slog.New(slog.NewTextHandler(&lockedBuffer{}, nil)))
	defer eng.Close()

	var builds atomic.Int32
	var created []*fakeInstance
	var mu sync.Mutex

	p := &Plugin{
		id:      "panic-plugin",
		version: "1.0.0",
		eng:     eng,
		lim:     Limits{PoolSize: 1, Deadline: 2 * time.Second},
		caps:    caps{Handle: true},
	}
	p.lim.normalize()
	p.initPools(func(context.Context) (instance, error) {
		n := builds.Add(1)
		if n == 1 {
			// Panic during instance construction must release the slot.
			panic("boom during makeFn")
		}
		fi := &fakeInstance{}
		if n == 2 {
			fi.handleFn = func(context.Context, *callState, []byte) ([]byte, *Error) {
				panic("boom during handle")
			}
		} else {
			fi.handleFn = func(context.Context, *callState, []byte) ([]byte, *Error) {
				return []byte(`{"status":200}`), nil
			}
		}
		mu.Lock()
		created = append(created, fi)
		mu.Unlock()
		return fi, nil
	}, func(context.Context) (instance, error) {
		return &fakeInstance{}, nil
	})
	defer p.Close()

	// Build panic: error returned, slot not leaked.
	if _, err := p.Call(context.Background(), Request{Method: "GET", Path: "/a"}); err == nil {
		t.Fatal("expected build panic error")
	}
	if snap := p.PoolStats(); snap.InUse != 0 || snap.Building != 0 {
		t.Fatalf("after build panic: pool=%+v, want InUse=0 Building=0", snap)
	}

	// Handle panic: recovered into trap error, poisoned instance closed, slot returned.
	_, out, err := p.CallDetailed(context.Background(), Request{Method: "GET", Path: "/b"})
	if err == nil || !out.Trap {
		t.Fatalf("handle panic: out=%+v err=%v, want Trap=true error", out, err)
	}
	mu.Lock()
	firstInst := created[0]
	mu.Unlock()
	if !firstInst.closed.Load() {
		t.Fatal("panicked instance was not closed")
	}

	// Subsequent call succeeds on replacement instance.
	resp, err := p.Call(context.Background(), Request{Method: "GET", Path: "/c"})
	if err != nil || resp.Status != 200 {
		t.Fatalf("post-panic call failed: resp=%+v err=%v pool=%+v", resp, err, p.PoolStats())
	}
	if snap := p.PoolStats(); snap.InUse != 0 || snap.Idle != 1 {
		t.Fatalf("final pool stats = %+v, want InUse=0 Idle=1", snap)
	}
}

func TestBackgroundJobWaitingOnBusyDoesNotHoldPoolInstance(t *testing.T) {
	t.Parallel()
	eng := NewEngine(slog.New(slog.NewTextHandler(&lockedBuffer{}, nil)))
	defer eng.Close()

	var busy atomic.Bool
	busy.Store(true)

	js := `function handle(req) {
		return { status: 200, body: { path: req.path } };
	}`
	p, err := eng.LoadJS("busy-job", "1.0.0", js, nil, Limits{
		PoolSize: 1, // even with a single instance, a paused job must not hold it
		Deadline: 2 * time.Second,
		Busy:     busy.Load,
	})
	if err != nil {
		t.Fatalf("LoadJS: %v", err)
	}
	defer p.Close()

	jobCtx, jobCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer jobCancel()
	jobDone := make(chan error, 1)
	go func() {
		_, err := p.Call(jobCtx, Request{Method: "POST", Path: "/ether/job/sync"})
		jobDone <- err
	}()

	// Interactive resolve must succeed immediately while the background job waits on Busy().
	resp, err := p.Call(context.Background(), Request{Method: "POST", Path: "/ether/v1/kinds/music/resolve"})
	if err != nil || resp.Status != 200 {
		t.Fatalf("interactive resolve blocked by paused background job: resp=%+v err=%v pool=%+v", resp, err, p.PoolStats())
	}
	if snap := p.PoolStats(); snap.InUse != 0 {
		t.Fatalf("paused background job held an instance: pool=%+v", snap)
	}

	busy.Store(false)
	if err := <-jobDone; err != nil {
		t.Fatalf("background job failed after Busy cleared: %v", err)
	}
}

func TestPoolExhaustionLogsHoldingRoutesAndUpdatesStats(t *testing.T) {
	t.Parallel()
	var logs lockedBuffer
	eng := NewEngine(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer eng.Close()

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	defer close(release)

	host := HostFunc(func(ctx context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
		if name == "http.fetch" {
			entered <- struct{}{}
			select {
			case <-release:
				return json.RawMessage(`{"status":200}`), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return json.RawMessage(`null`), nil
	})

	js := `function handle(req) {
		ether.http.fetch({ url: "https://example.test" });
		return { status: 200, body: { ok: true } };
	}`
	p, err := eng.LoadJS("music-plugin", "1.0.0", js, host, Limits{
		PoolSize: 1,
		Deadline: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("LoadJS: %v", err)
	}
	defer p.Close()

	holderDone := make(chan error, 1)
	go func() {
		_, err := p.Call(context.Background(), Request{Method: "POST", Path: "/ether/v1/kinds/music/expand"})
		holderDone <- err
	}()
	<-entered

	waitCtx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err = p.Call(waitCtx, Request{Method: "POST", Path: "/ether/v1/kinds/music/resolve"})
	if !errors.Is(err, ErrPoolBusy) {
		t.Fatalf("expected ErrPoolBusy, got %v", err)
	}

	snap := p.PoolStats()
	if snap.Exhaustions != 1 {
		t.Fatalf("PoolStats.Exhaustions = %d, want 1", snap.Exhaustions)
	}
	if snap.InUse != 1 || len(snap.Holders) != 1 || snap.Holders[0].Route != "/ether/v1/kinds/music/expand" {
		t.Fatalf("PoolStats holders = %+v, want [/ether/v1/kinds/music/expand]", snap)
	}

	logOut := logs.String()
	if !strings.Contains(logOut, "plugin instance pool exhausted") ||
		!strings.Contains(logOut, "/ether/v1/kinds/music/resolve") ||
		!strings.Contains(logOut, "/ether/v1/kinds/music/expand") {
		t.Fatalf("exhaustion log missing expected route details:\n%s", logOut)
	}

	release <- struct{}{}
	if err := <-holderDone; err != nil {
		t.Fatalf("holder call failed: %v", err)
	}
}

func TestResolveLaneProtectedFromSlowFrontendAdapters(t *testing.T) {
	eng := NewEngine(slog.New(slog.NewTextHandler(&lockedBuffer{}, nil)))
	defer eng.Close()

	t.Run("poolSize2", func(t *testing.T) {
		feEntered := make(chan struct{}, 2)
		releaseFE := make(chan struct{})
		p := &Plugin{
			id:      "fe-pool2",
			version: "1.0.0",
			eng:     eng,
			lim:     Limits{PoolSize: 2, Deadline: 2 * time.Second},
			caps:    caps{Handle: true},
		}
		p.lim.normalize()
		p.initPools(func(context.Context) (instance, error) {
			return &fakeInstance{
				handleFn: func(ctx context.Context, _ *callState, reqJSON []byte) ([]byte, *Error) {
					var req Request
					_ = json.Unmarshal(reqJSON, &req)
					if FrontendAdapterPath(req.Path) {
						feEntered <- struct{}{}
						select {
						case <-releaseFE:
							return []byte(`{"status":200}`), nil
						case <-ctx.Done():
							return nil, &Error{Status: StatusTimeout, Message: ctx.Err().Error()}
						}
					}
					return []byte(`{"status":200}`), nil
				},
			}, nil
		}, func(context.Context) (instance, error) { return &fakeInstance{}, nil })
		defer p.Close()

		fe1Done := make(chan error, 1)
		go func() {
			_, err := p.Call(context.Background(), Request{Method: "GET", Path: "/ether/v1/frontend/subsonic/getCoverArt"})
			fe1Done <- err
		}()
		<-feEntered

		// A second slow frontend-adapter call queues on the shared work lane
		// rather than taking the second pool instance.
		fe2Done := make(chan error, 1)
		go func() {
			_, err := p.Call(context.Background(), Request{Method: "GET", Path: "/ether/v1/frontend/subsonic/getAlbum"})
			fe2Done <- err
		}()

		// Playback resolve runs immediately on the free second instance.
		if _, err := p.Call(context.Background(), Request{Method: "POST", Path: "/ether/v1/kinds/music/resolve"}); err != nil {
			t.Fatalf("resolve blocked by slow frontend adapters: %v", err)
		}
		if _, err := p.Call(context.Background(), Request{Method: "POST", Path: "/ether/v1/kinds/music/match"}); err != nil {
			t.Fatalf("match blocked by slow frontend adapters: %v", err)
		}

		close(releaseFE)
		if err := <-fe1Done; err != nil {
			t.Fatalf("fe1 failed: %v", err)
		}
		if err := <-fe2Done; err != nil {
			t.Fatalf("fe2 failed: %v", err)
		}
	})

	t.Run("poolSize3", func(t *testing.T) {
		jobEntered := make(chan struct{}, 1)
		feEntered := make(chan struct{}, 1)
		releaseWork := make(chan struct{})
		p := &Plugin{
			id:      "fe-pool3",
			version: "1.0.0",
			eng:     eng,
			lim:     Limits{PoolSize: 3, Deadline: 2 * time.Second},
			caps:    caps{Handle: true},
		}
		p.lim.normalize()
		p.initPools(func(context.Context) (instance, error) {
			return &fakeInstance{
				handleFn: func(ctx context.Context, _ *callState, reqJSON []byte) ([]byte, *Error) {
					var req Request
					_ = json.Unmarshal(reqJSON, &req)
					switch {
					case BackgroundPoolPath(req.Path):
						jobEntered <- struct{}{}
						<-releaseWork
					case FrontendAdapterPath(req.Path):
						feEntered <- struct{}{}
						<-releaseWork
					}
					return []byte(`{"status":200}`), nil
				},
			}, nil
		}, func(context.Context) (instance, error) { return &fakeInstance{}, nil })
		defer p.Close()

		jobDone := make(chan error, 1)
		go func() {
			_, err := p.Call(context.Background(), Request{Method: "POST", Path: "/ether/job/sync"})
			jobDone <- err
		}()
		<-jobEntered

		feDone := make(chan error, 1)
		go func() {
			_, err := p.Call(context.Background(), Request{Method: "GET", Path: "/ether/v1/frontend/subsonic/search"})
			feDone <- err
		}()
		<-feEntered

		// Both non-resolve slots (PoolSize-1 = 2) are occupied; an extra
		// non-resolve call queues, while resolve still has its reserved instance.
		extraDone := make(chan error, 1)
		go func() {
			_, err := p.Call(context.Background(), Request{Method: "GET", Path: "/ether/v1/frontend/subsonic/getArtist"})
			extraDone <- err
		}()

		if _, err := p.Call(context.Background(), Request{Method: "POST", Path: "/ether/v1/kinds/music/resolve"}); err != nil {
			t.Fatalf("resolve starved on poolSize=3: %v", err)
		}

		close(releaseWork)
		for _, ch := range []chan error{jobDone, feDone, extraDone} {
			if err := <-ch; err != nil {
				t.Fatalf("work call failed: %v", err)
			}
		}
	})
}

func TestResolvePriorityAndPostWaitExecutionDeadline(t *testing.T) {
	eng := NewEngine(slog.New(slog.NewTextHandler(&lockedBuffer{}, nil)))
	defer eng.Close()

	holderEntered := make(chan struct{}, 1)
	releaseHolder := make(chan struct{})
	var mu sync.Mutex
	var order []string
	var resolveRemaining time.Duration

	p := &Plugin{
		id:      "prio-plugin",
		version: "1.0.0",
		eng:     eng,
		lim:     Limits{PoolSize: 1, Deadline: 300 * time.Millisecond},
		caps:    caps{Handle: true},
	}
	p.lim.normalize()
	p.initPools(func(context.Context) (instance, error) {
		return &fakeInstance{
			handleFn: func(ctx context.Context, _ *callState, reqJSON []byte) ([]byte, *Error) {
				var req Request
				_ = json.Unmarshal(reqJSON, &req)
				if req.Path == "/ether/v1/frontend/hold" {
					holderEntered <- struct{}{}
					<-releaseHolder
					return []byte(`{"status":200}`), nil
				}
				mu.Lock()
				order = append(order, req.Path)
				if req.Path == "/ether/v1/kinds/music/resolve" {
					if dl, ok := ctx.Deadline(); ok {
						resolveRemaining = time.Until(dl)
					}
				}
				mu.Unlock()
				return []byte(`{"status":200}`), nil
			},
		}, nil
	}, func(context.Context) (instance, error) { return &fakeInstance{}, nil })
	defer p.Close()

	holderDone := make(chan error, 1)
	go func() {
		_, err := p.Call(context.Background(), Request{Method: "GET", Path: "/ether/v1/frontend/hold"})
		holderDone <- err
	}()
	<-holderEntered

	// Queue a normal non-resolve waiter first, then a resolve waiter behind it.
	normalDone := make(chan error, 1)
	go func() {
		_, err := p.Call(context.Background(), Request{Method: "GET", Path: "/ether/v1/frontend/next"})
		normalDone <- err
	}()
	for p.PoolStats().Waiting < 1 {
		time.Sleep(time.Millisecond)
	}

	resolveDone := make(chan error, 1)
	go func() {
		_, err := p.Call(context.Background(), Request{
			Method:   "POST",
			Path:     "/ether/v1/kinds/music/resolve",
			Deadline: 200 * time.Millisecond,
		})
		resolveDone <- err
	}()
	for p.PoolStats().Waiting < 2 {
		time.Sleep(time.Millisecond)
	}

	// Keep the holder busy for > half the resolve's 200ms execution deadline
	// before releasing, then verify the resolve ran ahead of the normal waiter
	// AND received a fresh 200ms execution deadline after acquiring the instance.
	time.Sleep(120 * time.Millisecond)
	close(releaseHolder)

	for _, ch := range []chan error{holderDone, resolveDone, normalDone} {
		if err := <-ch; err != nil {
			t.Fatalf("call failed: %v", err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "/ether/v1/kinds/music/resolve" || order[1] != "/ether/v1/frontend/next" {
		t.Fatalf("execution order = %v, want resolve ahead of normal waiter", order)
	}
	if resolveRemaining < 120*time.Millisecond {
		t.Fatalf("resolve remaining execution deadline = %s after 120ms wait, want fresh ~200ms deadline", resolveRemaining)
	}
}
