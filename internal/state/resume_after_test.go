package state

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestNotStartedAtZeroAndAtN(t *testing.T) {
	dur := 100 * time.Minute // 5% = 5m

	// Default (0): 5% of duration.
	if !notStartedWith(4*time.Minute+59*time.Second, dur, 0) {
		t.Fatal("4m59s of 100m should be notStarted at default 0")
	}
	if notStartedWith(5*time.Minute, dur, 0) {
		t.Fatal("5m of 100m should be started at default 0")
	}
	if notStartedWith(30*time.Second, 0, 0) {
		t.Fatal("unknown duration with default 0 should not be treated as notStarted")
	}

	// Custom N = 30s overrides 5% (both shorter than 5% and when duration is 0).
	if !notStartedWith(29*time.Second, dur, 30*time.Second) {
		t.Fatal("29s should be notStarted when resumeAfter=30s")
	}
	if notStartedWith(30*time.Second, dur, 30*time.Second) {
		t.Fatal("30s should be started when resumeAfter=30s")
	}
	if !notStartedWith(20*time.Second, 0, 30*time.Second) {
		t.Fatal("20s with unknown duration should be notStarted when resumeAfter=30s")
	}

	// Custom N = 10m overrides 5% upward too.
	if !notStartedWith(6*time.Minute, dur, 10*time.Minute) {
		t.Fatal("6m of 100m should be notStarted when resumeAfter=10m")
	}
	if notStartedWith(10*time.Minute, dur, 10*time.Minute) {
		t.Fatal("10m of 100m should be started when resumeAfter=10m")
	}
}

func TestStateResumeAfterOptionIsolationAndReload(t *testing.T) {
	db1, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db1.Close()
	db2, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	// Two independent State instances must not share a package-level threshold.
	sDefault := New(db1, Options{ResumeAfterSeconds: 0})
	sThirty := New(db2, Options{ResumeAfterSeconds: 30})
	k := core.MovieKey(77)

	if err := sDefault.Progress(ctx, 1, k, 45*time.Second, time.Hour, "paused"); err != nil {
		t.Fatal(err)
	}
	if err := sThirty.Progress(ctx, 1, k, 45*time.Second, time.Hour, "paused"); err != nil {
		t.Fatal(err)
	}

	gotDefault, err := sDefault.Get(ctx, 1, k)
	if err != nil {
		t.Fatal(err)
	}
	gotThirty, err := sThirty.Get(ctx, 1, k)
	if err != nil {
		t.Fatal(err)
	}
	if gotDefault.ViewOffset != 0 {
		t.Fatalf("default state kept 45s/1h resume = %v, want 0", gotDefault.ViewOffset)
	}
	if gotThirty.ViewOffset != 45*time.Second {
		t.Fatalf("30s state resume = %v, want 45s", gotThirty.ViewOffset)
	}

	// Dynamic config update via SetResumeAfter applies immediately.
	sDefault.SetResumeAfter(20)
	if err := sDefault.Progress(ctx, 1, k, 25*time.Second, time.Hour, "paused"); err != nil {
		t.Fatal(err)
	}
	gotUpdated, err := sDefault.Get(ctx, 1, k)
	if err != nil {
		t.Fatal(err)
	}
	if gotUpdated.ViewOffset != 25*time.Second {
		t.Fatalf("updated state resume = %v, want 25s", gotUpdated.ViewOffset)
	}
}

func TestApplyRemoteResumeJournalFloor(t *testing.T) {
	s, now := newTestState(t)
	k1 := core.MovieKey(81)
	k2 := core.MovieKey(82)

	// At default 0: the 1-minute remote resume floor clears <= 60s even when duration is unknown.
	if err := s.ApplyRemoteResume(ctx, 1, k1, 45*time.Second, *now); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, 1, k1)
	if err != nil {
		t.Fatal(err)
	}
	if got.ViewOffset != 0 {
		t.Fatalf("default ApplyRemoteResume 45s = %v, want 0", got.ViewOffset)
	}
	if err := s.ApplyRemoteResume(ctx, 1, k1, 61*time.Second, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, 1, k1)
	if err != nil {
		t.Fatal(err)
	}
	if got.ViewOffset != 61*time.Second {
		t.Fatalf("default ApplyRemoteResume 61s = %v, want 61s", got.ViewOffset)
	}

	// At N = 30s (< 1m): remote floor follows N instead of 1m, so 45s is kept and 20s clears.
	s.SetResumeAfter(30)
	if err := s.ApplyRemoteResume(ctx, 1, k2, 45*time.Second, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, 1, k2)
	if err != nil {
		t.Fatal(err)
	}
	if got.ViewOffset != 45*time.Second {
		t.Fatalf("N=30s ApplyRemoteResume 45s = %v, want 45s", got.ViewOffset)
	}
	if err := s.ApplyRemoteResume(ctx, 1, k2, 20*time.Second, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, 1, k2)
	if err != nil {
		t.Fatal(err)
	}
	if got.ViewOffset != 0 {
		t.Fatalf("N=30s ApplyRemoteResume 20s = %v, want 0", got.ViewOffset)
	}

	// At N = 120s (> 1m): 90s is cleared and 150s is kept.
	s.SetResumeAfter(120)
	if err := s.ApplyRemoteResume(ctx, 1, k2, 90*time.Second, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, 1, k2)
	if err != nil {
		t.Fatal(err)
	}
	if got.ViewOffset != 0 {
		t.Fatalf("N=120s ApplyRemoteResume 90s = %v, want 0", got.ViewOffset)
	}
	if err := s.ApplyRemoteResume(ctx, 1, k2, 150*time.Second, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, 1, k2)
	if err != nil {
		t.Fatal(err)
	}
	if got.ViewOffset != 150*time.Second {
		t.Fatalf("N=120s ApplyRemoteResume 150s = %v, want 150s", got.ViewOffset)
	}
}
