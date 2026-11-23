package usenet

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/usenet/nntptest"
)

func TestSimultaneousIPLimitImmediateBackoffAndWait(t *testing.T) {
	srv := nntptest.Start(t, nntptest.Options{Username: "u", Password: "p"})
	want := []byte("simultaneous-ip-backoff-payload")
	addSeg(t, srv, "iplock@test", want)

	var ipLocked atomic.Bool
	ipLocked.Store(true)
	srv.SetFault(func(cmd, _ string) nntptest.Fault {
		if cmd == "AUTHINFO" && ipLocked.Load() {
			return nntptest.Fault{Code: 481, Text: "max number of simultaneous IP addresses reached"}
		}
		return nntptest.Fault{}
	})

	f := newFetcher(t, []config.UsenetProvider{srv.Provider("primary", 4)}, Options{
		BackoffMin:    25 * time.Millisecond,
		BackoffMax:    100 * time.Millisecond,
		FailThreshold: DefaultFailThreshold,
		Retries:       0,
	})

	_, err := f.Body(context.Background(), "iplock@test", nil, core.PriorityLive)
	if !errors.Is(err, ErrProviderBackoff) {
		t.Fatalf("Body during 481 IP lock = %v, want ErrProviderBackoff", err)
	}
	if f.HasServingProvider() {
		t.Fatal("HasServingProvider = true while provider is in simultaneous-IP backoff")
	}
	if st := statsFor(f, "primary"); st.State != StateBackoff {
		t.Fatalf("provider state = %q, want %q", st.State, StateBackoff)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ipLocked.Store(false)
	ok, werr := f.WaitProviderBackoff(ctx, "iplock@test", err)
	if werr != nil || !ok {
		t.Fatalf("WaitProviderBackoff = (%v, %v), want (true, nil)", ok, werr)
	}

	got, err := f.Body(ctx, "iplock@test", nil, core.PriorityLive)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("Body after backoff cleared = (%q, %v), want payload", got, err)
	}
	if !f.HasServingProvider() {
		t.Fatal("HasServingProvider = false after provider recovered")
	}
}

func TestWaitProviderBackoffWakesWhenIdleSocketReturns(t *testing.T) {
	srv := nntptest.Start(t, nntptest.Options{Username: "u", Password: "p"})
	wantA := []byte("first-article")
	wantB := []byte("second-article")
	addSeg(t, srv, "hold@test", wantA)
	addSeg(t, srv, "wait@test", wantB)

	var ipLocked atomic.Bool
	inBody := make(chan struct{})
	releaseBody := make(chan struct{})
	var bodyOnce sync.Once
	t.Cleanup(func() {
		select {
		case <-releaseBody:
		default:
			close(releaseBody)
		}
	})

	srv.SetFault(func(cmd, arg string) nntptest.Fault {
		if cmd == "AUTHINFO" && ipLocked.Load() {
			return nntptest.Fault{Code: 481, Text: "max number of simultaneous IP addresses reached"}
		}
		if cmd == "BODY" && arg == "hold@test" {
			bodyOnce.Do(func() { close(inBody) })
			<-releaseBody
		}
		return nntptest.Fault{}
	})

	f := newFetcher(t, []config.UsenetProvider{srv.Provider("primary", 2)}, Options{
		BodyPipelineDepth: 1,
		BackoffMin:        30 * time.Second,
		BackoffMax:        time.Minute,
		Retries:           0,
	})

	firstDone := make(chan error, 1)
	go func() {
		_, err := f.Body(context.Background(), "hold@test", nil, core.PriorityLive)
		firstDone <- err
	}()
	<-inBody

	// Lock out any new dial while the first connection is still active.
	ipLocked.Store(true)
	_, err := f.Body(context.Background(), "wait@test", nil, core.PriorityLive)
	if !errors.Is(err, ErrProviderBackoff) {
		t.Fatalf("second Body while new dials are IP-locked = %v, want ErrProviderBackoff", err)
	}
	if !f.HasServingProvider() {
		t.Fatal("HasServingProvider = false while pool still has an established socket")
	}

	waitRes := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ok, werr := f.WaitProviderBackoff(ctx, "wait@test", err)
		if werr != nil {
			waitRes <- werr
			return
		}
		if !ok {
			waitRes <- errors.New("WaitProviderBackoff returned ok=false")
			return
		}
		waitRes <- nil
	}()

	// Returning the active socket to idle wakes WaitProviderBackoff immediately
	// without waiting for the 30s connect backoff timer.
	close(releaseBody)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Body: %v", err)
	}
	if err := <-waitRes; err != nil {
		t.Fatalf("WaitProviderBackoff: %v", err)
	}

	got, err := f.Body(context.Background(), "wait@test", nil, core.PriorityLive)
	if err != nil || !bytes.Equal(got, wantB) {
		t.Fatalf("Body on returned idle socket = (%q, %v)", got, err)
	}
}
