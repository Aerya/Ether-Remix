package sessions

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestOpenSidecarResolvesActivePlaybackSourceForSharedWireID(t *testing.T) {
	ctx := context.Background()
	pb := &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := NewEventBus(nil, active)
	defer events.Close()

	var fromSource string
	b, err := NewBroker(BrokerDeps{
		SurfacePolicy: testSessionPolicy,
		Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
			return &core.Title{Key: k}, nil
		},
		Playback:     pb,
		Transcoder:   fakeBrokerTranscoder{},
		Progress:     &fakeProgress{},
		ActiveViewer: active,
		CanPlay:      func(*core.Viewer, bool, time.Time) bool { return true },
		BitrateLimit: func(*core.Viewer) int { return 0 },
		OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
		Events:       events,
		OpenSubtitle: func(id, format string) (io.ReadCloser, string, error) {
			return io.NopCloser(strings.NewReader("default:" + id)), format, nil
		},
		OpenSubtitleFrom: func(id, format, source string) (io.ReadCloser, string, error) {
			fromSource = source
			return io.NopCloser(strings.NewReader("aligned:" + source)), format, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	item := core.MovieKey(77)
	viewer := &core.Viewer{ID: 5}
	device := core.Device{ID: "plex-tv"}
	access := frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "plex"}

	// Shared library metadata wire ID has no SessionID attached.
	wireID, err := b.BindStream(ctx, access, StreamBinding{
		Key:   item,
		Index: 4,
		Info: core.StreamInfo{
			Index:       4,
			Type:        "subtitle",
			Codec:       "srt",
			Language:    "en",
			External:    true,
			ExternalURL: "ether-subtitle:aligned-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// A PreparedOnly reservation must not be treated as active playback.
	pb.mu.Lock()
	pb.sessions["prep-1"] = &core.PlaySession{
		ID:           "prep-1",
		UserID:       viewer.ID,
		ClientID:     device.ID,
		Key:          item,
		PreparedOnly: true,
		Resolution:   &core.Resolution{Release: core.Release{ID: "rel-prepared"}},
	}
	pb.mu.Unlock()

	rc, _, err := b.OpenSidecar(ctx, access, auth.Acceptance{}, wireID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if fromSource != "" || string(body) != "default:ether-subtitle:aligned-1" {
		t.Fatalf("PreparedOnly leaked into OpenSidecar: source=%q body=%q", fromSource, string(body))
	}

	// Once playback is active, OpenSidecar passes the active release's source key.
	pb.mu.Lock()
	pb.sessions["play-1"] = &core.PlaySession{
		ID:           "play-1",
		UserID:       viewer.ID,
		ClientID:     device.ID,
		Key:          item,
		PreparedOnly: false,
		Resolution:   &core.Resolution{Release: core.Release{ID: "rel-active"}},
	}
	pb.mu.Unlock()

	rc, _, err = b.OpenSidecar(ctx, access, auth.Acceptance{}, wireID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(rc)
	_ = rc.Close()
	if fromSource != "rel-active" || string(body) != "aligned:rel-active" {
		t.Fatalf("active playback source not used: source=%q body=%q", fromSource, string(body))
	}
}
