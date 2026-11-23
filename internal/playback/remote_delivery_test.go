package playback

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type steppingWriter struct {
	advance func(time.Duration)
	step    time.Duration
}

func (w *steppingWriter) Write(p []byte) (int, error) {
	if w.advance != nil && w.step > 0 {
		w.advance(w.step)
	}
	return len(p), nil
}

func TestRemoteDeliveryMeterFillSeekAndPause(t *testing.T) {
	now := time.Date(2026, 11, 22, 16, 42, 0, 0, time.UTC)
	advance := func(d time.Duration) { now = now.Add(d) }

	m := &Manager{now: func() time.Time { return now }}
	var events []RemoteDeliveryEvent
	m.SetRemoteDeliveryObserver(func(ev RemoteDeliveryEvent) {
		events = append(events, ev)
	})

	s := newSession(
		"sess-1",
		identity{userID: 1, clientID: "tv", key: 42},
		&core.User{ID: 1},
		core.ClientCaps{Remote: true, NoPlaybackTelemetry: true},
		&core.Title{Key: 42, Title: "Movie"},
		"",
		now,
	)
	s.virtual.Size = 10 << 30 // 10 GiB file

	runRange := func(start, length int64, chunks int, chunkSize int, perWrite time.Duration) {
		t.Helper()
		s.mu.Lock()
		isMediaRange := s.beginDirectRangeLocked(start, length, now)
		s.mu.Unlock()

		meter := &remoteDeliveryMeter{
			m:            m,
			ctx:          context.Background(),
			s:            s,
			w:            &steppingWriter{advance: advance, step: perWrite},
			pos:          start,
			isMediaRange: isMediaRange,
			mediaKbps:    19168,
			lastFlush:    now,
		}
		buf := make([]byte, chunkSize)
		for i := 0; i < chunks; i++ {
			if _, err := meter.Write(buf); err != nil {
				t.Fatalf("Write: %v", err)
			}
		}
		meter.flush(now)
	}

	// 1. Initial media range starts in buffer-fill mode (first 10s), then exits
	//    buffer-fill mode for steady-state delivery past 10s.
	runRange(0, 10<<30, 14, 1<<20, 1*time.Second)
	if len(events) < 6 {
		t.Fatalf("expected >= 6 meter windows, got %d", len(events))
	}
	if !events[0].BufferFilling {
		t.Fatalf("initial window should have BufferFilling=true: %+v", events[0])
	}
	if events[len(events)-1].BufferFilling {
		t.Fatalf("steady-state window past fill window should have BufferFilling=false: %+v", events[len(events)-1])
	}

	// 2. Sequential Range continuation (no jump) must NOT re-arm BufferFilling.
	beforeSeq := len(events)
	seqStart := s.directRangeEnd.Load()
	runRange(seqStart, 4<<20, 3, 1<<20, 1*time.Second)
	if len(events) <= beforeSeq {
		t.Fatalf("expected sequential range to emit a window")
	}
	for _, ev := range events[beforeSeq:] {
		if ev.BufferFilling {
			t.Fatalf("sequential continuation must not re-arm BufferFilling: %+v", ev)
		}
	}

	// 3. Non-sequential seek jumps past slack and MUST re-arm BufferFilling.
	beforeSeek := len(events)
	runRange(2<<30, 8<<30, 3, 1<<20, 1*time.Second)
	if len(events) <= beforeSeek || !events[beforeSeek].BufferFilling {
		t.Fatalf("post-seek window must set BufferFilling=true, got %+v", events[beforeSeek:])
	}

	// 4. Paused session must NOT emit delivery events.
	beforePause := len(events)
	s.mu.Lock()
	s.setViewerStateLocked("paused", now)
	s.mu.Unlock()
	runRange(3<<30, 7<<30, 3, 1<<20, 1*time.Second)
	if len(events) != beforePause {
		t.Fatalf("paused session must not emit delivery events, got %d new events", len(events)-beforePause)
	}
}
