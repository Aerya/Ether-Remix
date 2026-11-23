package sessions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type fencedPlayback struct {
	*fakePlayback
	fenceCalls int
	fenceErr   error
	progress   int
}

func (f *fencedPlayback) WithSourceSelection(_ context.Context, id, _ string, fn func(core.MediaInfo) error) error {
	f.fenceCalls++
	if f.fenceErr != nil {
		return f.fenceErr
	}
	ps, ok := f.Session(id)
	if !ok {
		return core.ErrSourceChanged
	}
	return fn(ps.Info)
}

func (f *fencedPlayback) PlaybackProgress(int64, string, core.RatingKey, time.Duration, string) {
	f.progress++
}

func TestReportProgressSkipsFenceOnEchoAndToleratesSourceChanged(t *testing.T) {
	ctx := context.Background()
	fp := &fencedPlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}}
	viewer := &core.Viewer{ID: 7}
	dev := core.Device{ID: "shield-tv", Client: "Jellyfin Android TV"}
	key := core.MovieKey(42)

	playID := "play-1"
	fp.sessions[playID] = &core.PlaySession{
		ID:       playID,
		UserID:   viewer.ID,
		ClientID: clientKey("jellyfin", dev),
		Key:      key,
		Info: core.MediaInfo{
			Container: "mkv",
			Duration:  time.Hour,
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "hevc"},
				{Index: 1, Type: "audio", Codec: "eac3"},
				{Index: 2, Type: "subtitle", Codec: "subrip"},
			},
		},
	}

	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := NewEventBus(nil, active)
	defer events.Close()
	b, err := NewBroker(BrokerDeps{
		SurfacePolicy: testSessionPolicy,
		Events:        events,
		Playback:      fp,
		Transcoder:    fakeBrokerTranscoder{},
		Progress:      &fakeProgress{},
		ActiveViewer:  active,
		CanPlay:       func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit:  func(*core.Viewer) int { return 0 },
		OnTimeline:    func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: k}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)

	sid := "sess-1"
	b.mu.Lock()
	b.sessions[sid] = &brokerSession{
		public: frontend.Session{ID: sid, ViewerID: viewer.ID, Device: dev, Protocol: "jellyfin", Item: key},
		playID: playID,
		opts:   core.DecideOptions{AudioStream: 1, SubtitleStream: -1},
	}
	b.mu.Unlock()

	access := frontend.SessionAccess{Viewer: viewer, Device: dev, Protocol: "jellyfin"}
	audioEcho, subEcho := 1, -1
	if err := b.Report(ctx, access, sid, frontend.Timeline{
		State: "playing", Position: 10 * time.Second, Duration: time.Hour,
		AudioStreamIndex: &audioEcho, SubtitleStreamIndex: &subEcho,
	}); err != nil {
		t.Fatalf("routine echo failed: %v", err)
	}
	if fp.fenceCalls != 0 {
		t.Fatalf("routine echo took selection fence %d times, want 0", fp.fenceCalls)
	}
	if fp.progress != 1 {
		t.Fatalf("routine echo skipped PlaybackProgress (%d)", fp.progress)
	}

	// During failover, WithSourceSelection returns ErrSourceChanged immediately;
	// Report must still succeed and deliver playback telemetry instead of 503ing.
	fp.fenceErr = core.ErrSourceChanged
	subSwitch := 2
	if err := b.Report(ctx, access, sid, frontend.Timeline{
		State: "playing", Position: 15 * time.Second, Duration: time.Hour,
		AudioStreamIndex: &audioEcho, SubtitleStreamIndex: &subSwitch,
	}); err != nil {
		t.Fatalf("progress during failover returned error: %v", err)
	}
	if fp.fenceCalls != 1 || fp.progress != 2 {
		t.Fatalf("failover progress fenceCalls=%d progress=%d, want 1 and 2", fp.fenceCalls, fp.progress)
	}

	// When stable, an invalid track index still fails closed.
	fp.fenceErr = nil
	invalid := 99
	if err := b.Report(ctx, access, sid, frontend.Timeline{
		State: "playing", Position: 20 * time.Second, Duration: time.Hour,
		SubtitleStreamIndex: &invalid,
	}); !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("invalid track change = %v, want ErrNotAvailable", err)
	}
}
