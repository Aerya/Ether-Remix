package livetv

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type lifecycleTransport func(*http.Request) (*http.Response, error)

func (f lifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestIPTVStartupHasSharedDeadline(t *testing.T) {
	client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 10*time.Second {
			t.Error("IPTV startup has no shared 10-second deadline")
		}
		return nil, context.DeadlineExceeded
	})}
	s := testService(t, Options{HTTPClient: client})
	c, err := s.SaveChannel(context.Background(), Channel{Name: "Stalled", Number: "1", Kind: "iptv", Enabled: true, StreamURL: "http://upstream.invalid/live"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tune(context.Background(), &core.User{ID: 1}, c.ID, core.ClientCaps{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if len(s.ViewingSessions()) != 0 {
		t.Fatal("failed tune retained its reservation")
	}
}

func TestPendingTuneCanBeInspectedAndStoppedWithoutWaitingForUpstream(t *testing.T) {
	entered := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	}))
	defer up.Close()
	s := testService(t, Options{HTTPClient: up.Client()})
	c, err := s.SaveChannel(context.Background(), Channel{Name: "Stalled", Number: "1", Kind: "iptv", Enabled: true, StreamURL: up.URL})
	if err != nil {
		t.Fatal(err)
	}
	u := &core.User{ID: 1}
	done := make(chan error, 1)
	go func() { _, err := s.Tune(context.Background(), u, c.ID, core.ClientCaps{}); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("tune did not reach upstream")
	}
	stopped := make(chan error, 1)
	go func() {
		views := s.ViewingSessions()
		if len(views) != 1 {
			stopped <- fmt.Errorf("pending reservations = %d", len(views))
			return
		}
		stopped <- s.Stop(context.Background(), views[0].ID, u)
	}()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("monitor/stop blocked behind upstream I/O")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stopped tune kept running")
	}
}

type slowProbeTranscoder struct {
	fakeTranscoder
	probeDelay time.Duration
	openDelay  time.Duration
}

func (f *slowProbeTranscoder) ProbeLive(ctx context.Context, _ string) (*core.MediaInfo, error) {
	select {
	case <-time.After(f.probeDelay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &core.MediaInfo{
		Container: "hls",
		Streams: []core.StreamInfo{
			{Type: "video", Codec: "h264", Width: 1280, Height: 720},
			{Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000},
		},
	}, nil
}

func (f *slowProbeTranscoder) StartDecoder(_ context.Context, r transcode.DecoderRequest) (transcode.Decoder, error) {
	return &slowOpenDecoder{
		broadcastFakeDecoder: broadcastFakeDecoder{format: r.Format, origin: time.Now(), closed: make(chan struct{})},
		delay:                f.openDelay,
	}, nil
}

func (f *slowProbeTranscoder) StartHLS(ctx context.Context, r core.HLSRequest) (core.HLSSession, error) {
	select {
	case <-time.After(f.openDelay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return f.fakeTranscoder.StartHLS(ctx, r)
}

type slowProbeSteadyTranscoder struct {
	*slowProbeTranscoder
}

func (slowProbeSteadyTranscoder) ContinuousBudget() (float64, bool) { return 100, false }

type slowOpenDecoder struct {
	broadcastFakeDecoder
	delay time.Duration
	once  sync.Once
}

func (d *slowOpenDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	var waitErr error
	d.once.Do(func() {
		select {
		case <-time.After(d.delay):
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	})
	if waitErr != nil {
		return transcode.RawFrame{}, waitErr
	}
	return d.broadcastFakeDecoder.Next(ctx)
}

// A slower working route (6 s probe, 2 s provider tune gap, 2 s stream open)
// must still tune: provider pacing waits before the probe clock, and post-probe
// startup gets its own bounded budget rather than the probe's remainder.
func TestIPTVSlowProbeStartupBudget(t *testing.T) {
	for _, steady := range []bool{true, false} {
		name := "direct"
		if steady {
			name = "steady"
		}
		t.Run(name, func(t *testing.T) {
			// SQLite always: a PostgreSQL connection's timers belong to
			// no synctest bubble, so closing one inside it panics.
			db, err := store.OpenPath(context.Background(), filepath.Join(t.TempDir(), "ether.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			synctest.Test(t, func(t *testing.T) {
				client := &http.Client{Transport: lifecycleTransport(func(r *http.Request) (*http.Response, error) {
					h := make(http.Header)
					h.Set("Content-Type", "application/vnd.apple.mpegurl")
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     h,
						Body:       io.NopCloser(strings.NewReader("#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nseg.ts\n")),
						Request:    r,
					}, nil
				})}
				base := &slowProbeTranscoder{probeDelay: 6 * time.Second, openDelay: 2 * time.Second}
				var tc core.Transcoder = base
				if steady {
					tc = slowProbeSteadyTranscoder{base}
				}
				s := testService(t, Options{DB: db, HTTPClient: client, Transcoder: tc})
				// Keep the loopback relay in-memory inside synctest so no OS
				// listener goroutine blocks virtual clock advancement.
				s.inputRelay.host = "127.0.0.1:1"
				s.inputRelay.grants = map[[sha256.Size]byte]*inputGrant{}
				s.inputRelay.bySess = map[*session]*inputGrant{}

				ctx := context.Background()
				if _, err := s.ImportSource(ctx, Source{
					ID:             "provider",
					Name:           "Provider",
					MaxConnections: 1,
					Playlist:       "#EXTM3U\n#EXTINF:-1,Proxied\nhttps://provider.invalid/live.m3u8\n",
				}); err != nil {
					s.Close()
					t.Fatal(err)
				}
				origin := time.Now()
				if _, err := s.inputRelay.taps.reserveOpen("provider", defaultTuneGap, 0, origin); err != nil {
					s.Close()
					t.Fatal(err)
				}
				channels, err := s.Channels(ctx)
				if err != nil || len(channels) != 1 {
					s.Close()
					t.Fatalf("channels = %d, err = %v", len(channels), err)
				}
				c := channels[0]
				c.Transcode = true
				if !steady {
					c.Screens = &ChannelScreens{NoSignal: "error"}
				}
				if _, err := s.SaveChannel(ctx, c); err != nil {
					s.Close()
					t.Fatal(err)
				}
				u := &core.User{ID: 1}
				info, err := s.Tune(ctx, u, c.ID, core.ClientCaps{})
				if err != nil {
					s.Close()
					t.Fatalf("slow working route failed to tune: %v", err)
				}
				if got := time.Since(origin); got < 10*time.Second {
					s.Close()
					t.Fatalf("virtual elapsed = %s, want >= 10s (2s gap + 6s probe + 2s open)", got)
				}
				s.inputRelay.taps.mu.Lock()
				opens := len(s.inputRelay.taps.pace["provider"].opens)
				s.inputRelay.taps.mu.Unlock()
				if opens != 2 {
					s.Close()
					t.Fatalf("provider opens booked = %d, want 2 (no self-pacing)", opens)
				}
				if err := s.Stop(ctx, info.ID, u); err != nil {
					s.Close()
					t.Fatal(err)
				}
				s.Close()
				// Advance virtual time past any background slate retry timer so
				// every bubble goroutine exits cleanly.
				time.Sleep(31 * time.Second)
				synctest.Wait()
			})
		})
	}
}
