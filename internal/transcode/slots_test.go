package transcode

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/config"
	"testing"
	"time"
)

// Priority, newest-holder preemption, exact accounting, and viewer precedence
// are one shared admission invariant (including late releases after preemption).
func TestSlotPriority(t *testing.T) {
	m := newTestManager(t, newFakeRunner(), config.Transcode{MaxTranscodes: 2})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	old, oldRelease, err := m.AcquireTranscodeSlot(ctx, SlotBackground)
	if err != nil {
		t.Fatal(err)
	}
	defer oldRelease()
	newest, newRelease, err := m.AcquireTranscodeSlot(ctx, SlotBackground)
	if err != nil {
		t.Fatal(err)
	}
	defer newRelease()
	viewer, viewerRelease, err := m.AcquireTranscodeSlot(ctx, SlotViewer)
	if err != nil {
		t.Fatal(err)
	}
	defer viewerRelease()
	if !errors.Is(newest.Err(), context.Canceled) || old.Err() != nil || viewer.Err() != nil {
		t.Fatal("viewer did not preempt only newest background holder")
	}
	newRelease()
	newRelease()
	m.mu.Lock()
	count := m.encodingLocked("")
	m.mu.Unlock()
	if count != 2 {
		t.Fatalf("late release changed budget: %d", count)
	}
	// Fill with viewers, then queue a viewer and background behind them.
	_, otherRelease, err := m.AcquireTranscodeSlot(ctx, SlotViewer)
	if err != nil {
		t.Fatal(err)
	}
	defer otherRelease()
	if old.Err() != context.Canceled {
		t.Fatal("second viewer did not preempt older background")
	}
	viewerAcquired := make(chan func(), 1)
	go func() {
		_, r, e := m.AcquireTranscodeSlot(ctx, SlotViewer)
		if e == nil {
			viewerAcquired <- r
		}
	}()
	for {
		m.mu.Lock()
		waiting := m.slotViewers
		m.mu.Unlock()
		if waiting > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	backgroundAcquired := make(chan func(), 1)
	go func() {
		_, r, e := m.AcquireTranscodeSlot(ctx, SlotBackground)
		if e == nil {
			backgroundAcquired <- r
		}
	}()
	viewerRelease()
	var queuedRelease func()
	select {
	case queuedRelease = <-viewerAcquired:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case release := <-backgroundAcquired:
		release()
		t.Fatal("background overtook waiting viewer")
	default:
	}
	queuedRelease()
	select {
	case release := <-backgroundAcquired:
		release()
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// Imminent masters retain one bounded lane even while every continuous output
// slot is occupied. Ordinary work still waits and viewing never preempts prep.
func TestLivePreparationLaneUnderContinuousLoad(t *testing.T) {
	m := newTestManager(t, newFakeRunner(), config.Transcode{MaxTranscodes: 1})
	ctx := t.Context()
	_, viewerRelease, err := m.AcquireTranscodeSlot(ctx, SlotViewer)
	if err != nil {
		t.Fatal(err)
	}
	defer viewerRelease()
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, release, err := m.AcquireTranscodeSlot(short, SlotBackground); err == nil {
		release()
		t.Fatal("ordinary work bypassed continuous budget")
	}
	prep, release, err := m.AcquireTranscodeSlot(ctx, SlotLivePrep)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	short2, cancel2 := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel2()
	if _, r, err := m.AcquireTranscodeSlot(short2, SlotLivePrep); err == nil {
		r()
		t.Fatal("live prep exceeded one job")
	}
	m.mu.Lock()
	viewerCount, prepCount := m.encodingLocked(""), m.livePrepEncodes
	m.preemptBackgroundLocked()
	m.mu.Unlock()
	if prep.Err() != nil || viewerCount != 1 || prepCount != 1 {
		t.Fatalf("prep was preempted or charged to viewers: %v %d %d", prep.Err(), viewerCount, prepCount)
	}
	release()
	release()
	_, again, err := m.AcquireTranscodeSlot(ctx, SlotLivePrep)
	if err != nil {
		t.Fatal(err)
	}
	again()
}

func TestMultiviewGroupSharesSingleTranscodeSlotAndFailsFastWhenFull(t *testing.T) {
	m := newTestManager(t, newFakeRunner(), config.Transcode{MaxTranscodes: 2})
	ctx := t.Context()

	// Occupy 1 slot with an ungrouped viewer.
	_, soloRelease, err := m.AcquireTranscodeSlot(ctx, SlotViewer)
	if err != nil {
		t.Fatal(err)
	}
	defer soloRelease()

	// All 4 tiles + 1 handover encoder of one multiview group share 1 slot.
	var releases []func()
	for i := 0; i < 5; i++ {
		_, _, rel, err := m.acquirePrioritySlotForGroup(ctx, SlotViewer, false, "mv-group-1")
		if err != nil {
			t.Fatalf("group tile %d failed under MaxTranscodes=2: %v", i, err)
		}
		releases = append(releases, rel)
	}
	if got := m.EncodingCount(); got != 2 {
		t.Fatalf("EncodingCount = %d, want 2 (1 solo + 1 multiview group)", got)
	}

	// An unrelated viewer or second multiview group fails fast with ErrTooManyTranscodes
	// instead of blocking until context cancellation.
	if _, _, _, err := m.acquirePrioritySlotForGroup(ctx, SlotViewer, false, ""); !errors.Is(err, ErrTooManyTranscodes) {
		t.Fatalf("unrelated viewer when full: err=%v, want ErrTooManyTranscodes", err)
	}
	if _, _, _, err := m.acquirePrioritySlotForGroup(ctx, SlotViewer, false, "mv-group-2"); !errors.Is(err, ErrTooManyTranscodes) {
		t.Fatalf("second multiview group when full: err=%v, want ErrTooManyTranscodes", err)
	}

	// StartEncoder must also fail fast with ErrTooManyTranscodes (never hang).
	_, err = m.StartEncoder(ctx, EncoderRequest{
		Source:          RawFormat{Width: 640, Height: 360, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"},
		SegmentDuration: 2 * time.Second,
	})
	if !errors.Is(err, ErrTooManyTranscodes) {
		t.Fatalf("StartEncoder when full: err=%v, want ErrTooManyTranscodes", err)
	}

	// Releasing the group frees its single shared slot.
	for _, rel := range releases {
		rel()
	}
	if got := m.EncodingCount(); got != 1 {
		t.Fatalf("EncodingCount after group release = %d, want 1", got)
	}
	_, _, g2Rel, err := m.acquirePrioritySlotForGroup(ctx, SlotViewer, false, "mv-group-2")
	if err != nil {
		t.Fatalf("mv-group-2 after mv-group-1 released: %v", err)
	}
	g2Rel()

	// Reassigning a warm ungrouped slot to a multiview group lets sibling tiles
	// join under MaxTranscodes=1.
	m1 := newTestManager(t, newFakeRunner(), config.Transcode{MaxTranscodes: 1})
	_, h, hRel, err := m1.acquirePrioritySlotForGroup(ctx, SlotViewer, false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer hRel()
	m1.setSlotGroup(h, "mv-adopted")
	_, _, sibRel, err := m1.acquirePrioritySlotForGroup(ctx, SlotViewer, false, "mv-adopted")
	if err != nil {
		t.Fatalf("sibling tile after setSlotGroup under MaxTranscodes=1: %v", err)
	}
	defer sibRel()
	if got := m1.EncodingCount(); got != 1 {
		t.Fatalf("m1 EncodingCount = %d, want 1", got)
	}
}
