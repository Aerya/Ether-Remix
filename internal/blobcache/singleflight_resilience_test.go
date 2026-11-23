package blobcache

import (
	"bytes"
	"context"
	"testing"
)

func TestSingleFlightPutRecoversAfterPanicAndAbandonedLeader(t *testing.T) {
	t.Parallel()
	c, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer c.Close()
	ns := c.Namespace("lib", Quota{})

	// 1. A panic inside fn must return an error and remove the flight so the next caller succeeds.
	if _, err := ns.SingleFlightPut(context.Background(), "panic-key", func(context.Context) (Ref, error) {
		panic("boom")
	}); err == nil {
		t.Fatal("expected error after singleflight panic")
	}

	ref, err := ns.SingleFlightPut(context.Background(), "panic-key", func(ctx context.Context) (Ref, error) {
		data := []byte("hello")
		return ns.Put(ctx, "panic-key", bytes.NewReader(data), int64(len(data)))
	})
	if err != nil || ref == "" {
		t.Fatalf("post-panic SingleFlightPut failed: ref=%q err=%v", ref, err)
	}

	// 2. When a leader cancels, a concurrent waiter with a live ctx retries instead of inheriting cancellation.
	leaderStarted := make(chan struct{})
	leaderCtx, leaderCancel := context.WithCancel(context.Background())

	leaderDone := make(chan error, 1)
	go func() {
		_, err := ns.SingleFlightPut(leaderCtx, "cancel-key", func(ctx context.Context) (Ref, error) {
			close(leaderStarted)
			<-ctx.Done()
			return "", ctx.Err()
		})
		leaderDone <- err
	}()
	<-leaderStarted

	waiterDone := make(chan struct {
		ref Ref
		err error
	}, 1)
	go func() {
		r, err := ns.SingleFlightPut(context.Background(), "cancel-key", func(ctx context.Context) (Ref, error) {
			data := []byte("recovered")
			return ns.Put(ctx, "cancel-key", bytes.NewReader(data), int64(len(data)))
		})
		waiterDone <- struct {
			ref Ref
			err error
		}{r, err}
	}()

	// Wait until waiter has joined or is racing, then cancel leader.
	leaderCancel()
	if err := <-leaderDone; err == nil {
		t.Fatal("expected leader cancellation error")
	}
	got := <-waiterDone
	if got.err != nil || got.ref == "" {
		t.Fatalf("waiter after abandoned leader failed: ref=%q err=%v", got.ref, got.err)
	}
}
