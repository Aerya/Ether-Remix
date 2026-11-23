package config

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPolicyLeaseFencesLocalPublication(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bumps := 0
	s.SetPolicyEpoch(func() error { bumps++; return nil })
	release, err := s.AcquirePolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		done <- s.Update(func(c *Config) error { c.Catalog.IncludeAdult = !c.Catalog.IncludeAdult; return nil })
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("writer passed live lease: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if bumps != 1 {
		t.Fatalf("bumps=%d", bumps)
	}
	if err := s.Update(func(c *Config) error { c.Server.Port++; return nil }); err != nil {
		t.Fatal(err)
	}
	if bumps != 1 {
		t.Fatalf("unrelated settings bumped=%d", bumps)
	}
}

func TestPolicyLeaseRefreshesClusterLagOnce(t *testing.T) {
	a, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b.credentialKey = append([]byte(nil), a.credentialKey...)
	backend := &memShared{}
	ctx := context.Background()
	if err := a.AttachShared(ctx, backend, SharedOptions{PollInterval: -1}); err != nil {
		t.Fatal(err)
	}
	if err := b.AttachShared(ctx, backend, SharedOptions{PollInterval: -1}); err != nil {
		t.Fatal(err)
	}
	want := !a.Get().Catalog.IncludeAdult
	if err := a.Update(func(c *Config) error { c.Catalog.IncludeAdult = want; return nil }); err != nil {
		t.Fatal(err)
	}
	if b.SharedVersion() == a.SharedVersion() {
		t.Fatal("fixture has no lag")
	}
	release, err := b.AcquirePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if b.Get().Catalog.IncludeAdult != want || b.SharedVersion() != a.SharedVersion() {
		t.Fatal("lease retained stale policy")
	}
	release()
}

func TestPolicyWriterProgressesUnderReceiptLoad(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				release, err := s.AcquirePolicy(ctx)
				if err != nil {
					return
				}
				_ = s.Get()
				release()
			}
		}()
	}
	done := make(chan error, 1)
	go func() {
		done <- s.Update(func(c *Config) error { c.Catalog.IncludeAdult = !c.Catalog.IncludeAdult; return nil })
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-ctx.Done():
		t.Error("config writer starved by receipt leases")
	}
	cancel()
	wg.Wait()
}

func TestPolicyLeaseWaitsForDurablePublication(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	publishing, finish := make(chan struct{}), make(chan struct{})
	s.SetPolicyEpoch(func() error { close(publishing); <-finish; return nil })
	updated := make(chan error, 1)
	go func() {
		updated <- s.Update(func(c *Config) error { c.Catalog.IncludeAdult = !c.Catalog.IncludeAdult; return nil })
	}()
	<-publishing
	acquired := make(chan error, 1)
	go func() {
		release, err := s.AcquirePolicy(context.Background())
		if release != nil {
			release()
		}
		acquired <- err
	}()
	select {
	case err := <-acquired:
		t.Fatalf("lease passed uncommitted epoch: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(finish)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if err := <-acquired; err != nil {
		t.Fatal(err)
	}
}

func TestPolicyEpochBumpsOnVelaLivePreviewChange(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bumps := 0
	s.SetPolicyEpoch(func() error { bumps++; return nil })

	setVela := func(settings map[string]any) {
		t.Helper()
		if err := s.Update(func(c *Config) error {
			if c.Frontends == nil {
				c.Frontends = map[string]Frontend{}
			}
			fe := c.Frontends["vela"]
			fe.Settings = settings
			c.Frontends["vela"] = fe
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Default is "auto", so saving "auto" or unrelated skin settings does not bump.
	setVela(map[string]any{"livePreview": "auto"})
	setVela(map[string]any{"livePreview": "auto", "defaultSkinId": "lounge"})
	if bumps != 0 {
		t.Fatalf("no-op livePreview/skin save bumped policy epoch %d times", bumps)
	}

	// Changing to "on-click" bumps once; repeating "on-click" does not.
	setVela(map[string]any{"livePreview": "on-click", "defaultSkinId": "lounge"})
	if bumps != 1 {
		t.Fatalf("expected 1 bump after switching to on-click, got %d", bumps)
	}
	setVela(map[string]any{"livePreview": "on-click", "defaultSkinId": "cinematic"})
	if bumps != 1 {
		t.Fatalf("repeating on-click bumped policy epoch to %d", bumps)
	}

	// Changing to "off" bumps again.
	setVela(map[string]any{"livePreview": "off"})
	if bumps != 2 {
		t.Fatalf("expected 2 bumps after switching to off, got %d", bumps)
	}

	// Removing livePreview returns to default "auto", which bumps again.
	setVela(map[string]any{"defaultSkinId": "cinematic"})
	if bumps != 3 {
		t.Fatalf("expected 3 bumps after returning to implicit auto, got %d", bumps)
	}
}
