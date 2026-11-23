package playback

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestChannelProgrammeOverlapCountsAsOneBoundedStream(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 10000, time.Minute)}, []string{"A"}, func(h *harness, o *Options) { h.cfg.Server.DefaultStreamLimit = 1; h.cfg.Server.MaxStreams = 1 })
	ctx := context.Background()
	group := "0123456789abcdef0123456789abcdef"
	var sessions []*core.PlaySession
	for i, key := range []core.RatingKey{movieKey, movie2, movie3} {
		caps := defaultCaps(string(rune('a' + i)))
		ps, err := h.m.PrepareChannel(ctx, owner, key, caps, group)
		if err != nil {
			t.Fatal(err)
		}
		if ps.PlaybackGroup != group {
			t.Fatal("group not carried on snapshot")
		}
		sessions = append(sessions, ps)
	}
	if _, err := h.m.PrepareChannel(ctx, owner, epKey, defaultCaps("fourth"), group); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("unbounded overlap: %v", err)
	}
	if _, err := h.m.Prepare(ctx, owner, epKey, defaultCaps("another-viewer")); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("ordinary stream bypass: %v", err)
	}
	if _, err := h.m.Prepare(ctx, friend, movieKey, defaultCaps("friend")); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("server limit bypass: %v", err)
	}
	h.m.Stop(sessions[0].ID)
	if _, err := h.m.PrepareChannel(ctx, owner, epKey, defaultCaps("fourth"), group); err != nil {
		t.Fatalf("did not free programme overlap: %v", err)
	}
	if len(h.m.Sessions()) != 3 {
		t.Fatal("programmes replaced instead of overlapping")
	}
}

func TestPassthroughIPTVReservesAgainstLaterVOD(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 10000, time.Minute)}, []string{"A"}, func(h *harness, o *Options) { h.cfg.Server.DefaultStreamLimit = 1; h.cfg.Server.MaxStreams = 1 })
	group := "abcdef0123456789abcdef0123456789"
	if err := h.m.BeginChannel(owner, defaultCaps("iptv"), group); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("vod")); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("VOD bypassed IPTV reservation: %v", err)
	}
	if err := h.m.BeginChannel(friend, defaultCaps("other"), "1234567890abcdef1234567890abcdef"); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("server limit bypass: %v", err)
	}
	if _, err := h.m.PrepareChannel(context.Background(), owner, movieKey, defaultCaps("programme"), group); err != nil {
		t.Fatal("source in reserved group rejected", err)
	}
	h.m.EndChannel(group)
	for _, s := range h.m.Sessions() {
		h.m.Stop(s.ID)
	}
	if _, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("vod")); err != nil {
		t.Fatalf("reservation did not release: %v", err)
	}
}

func TestSharedBroadcastViewerAndRolloverBudget(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 10000, time.Minute)}, []string{"A"}, func(h *harness, o *Options) { h.cfg.Server.DefaultStreamLimit = 2; h.cfg.Server.MaxStreams = 2 })
	a, b, c := "broadcast-aaaaaaaaaaaaaaaa", "broadcast-bbbbbbbbbbbbbbbb", "broadcast-cccccccccccccccc"
	if err := h.m.JoinChannel(owner, defaultCaps("owner-a"), "viewer-aaaaaaaaaaaa", a); err != nil {
		t.Fatal(err)
	}
	if err := h.m.JoinChannel(friend, defaultCaps("friend-a"), "viewer-friend-aaaaa", a); err != nil {
		t.Fatal("second viewer should share output", err)
	}
	if err := h.m.JoinChannel(owner, defaultCaps("owner-b"), "viewer-bbbbbbbbbbbb", b); err != nil {
		t.Fatal("second channel should fit", err)
	}
	ctx := context.Background()
	if _, err := h.m.PrepareChannel(ctx, owner, movieKey, defaultCaps("source-a"), a); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.PrepareChannel(ctx, owner, movie2, defaultCaps("source-b"), b); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.PrepareChannel(ctx, owner, movie3, defaultCaps("rollover-a"), a); err != nil {
		t.Fatal("rollover double-counted", err)
	}
	if err := h.m.JoinChannel(friend, defaultCaps("friend-c"), "viewer-cccccccccccc", c); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatal("third distinct output admitted", err)
	}
	h.m.EndChannel("viewer-friend-aaaaa")
	h.m.EndChannel("viewer-aaaaaaaaaaaa")
	for _, ps := range h.m.Sessions() {
		if ps.PlaybackGroup == a {
			h.m.Stop(ps.ID)
		}
	}
	if err := h.m.JoinChannel(friend, defaultCaps("friend-c"), "viewer-cccccccccccc", c); err != nil {
		t.Fatal("retired output did not release budget", err)
	}
}

// The automatic limit (no MaxStreams) protects Usenet connections, so it
// counts only streams that resolve a release: passthrough IPTV neither
// takes one of those slots nor is refused by them.
func TestUsenetDerivedLimitLeavesPassthroughIPTV(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 10000, time.Minute)}, []string{"A"}, func(h *harness, o *Options) {
		h.cfg.Server.MaxStreams = 0
		h.cfg.Server.DefaultStreamLimit = 10
		h.cfg.Usenet.Providers = []config.UsenetProvider{{Enabled: true, Connections: 16}}
	})
	ctx := context.Background()
	if _, err := h.m.Prepare(ctx, owner, movieKey, defaultCaps("vod-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Prepare(ctx, friend, movieKey, defaultCaps("vod-2")); err != nil {
		t.Fatal(err)
	}
	if err := h.m.BeginChannel(owner, defaultCaps("iptv"), "abcdef0123456789abcdef0123456789"); err != nil {
		t.Fatalf("IPTV refused by the Usenet-derived limit: %v", err)
	}
	if _, err := h.m.Prepare(ctx, friend, movieKey, defaultCaps("vod-3")); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("third release-backed stream passed a 2-stream Usenet limit: %v", err)
	}
	for _, s := range h.m.Sessions() {
		if s.ClientID == "vod-2" {
			h.m.Stop(s.ID)
		}
	}
	if _, err := h.m.Prepare(ctx, friend, movieKey, defaultCaps("vod-3")); err != nil {
		t.Fatalf("an active IPTV channel took a Usenet slot: %v", err)
	}
}

func TestMultiviewGroupCountsAsOneStreamInPlaybackManager(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 10000, time.Minute)}, []string{"A"}, func(h *harness, o *Options) {
		h.cfg.Server.DefaultStreamLimit = 1
		h.cfg.Server.MaxStreams = 4
	})
	mvCaps := func(tile int) core.ClientCaps {
		c := defaultCaps("vela/tv")
		c.MultiviewGroup = "mv_group_alpha"
		c.MultiviewTile = tile
		return c
	}
	// 4 tiles in one multiview group (mixing passthrough + broadcast) count as 1 stream.
	if err := h.m.BeginChannel(owner, mvCaps(0), "mv-tile-0000000000000000"); err != nil {
		t.Fatalf("tile 0: %v", err)
	}
	if err := h.m.JoinChannel(owner, mvCaps(1), "mv-tile-1111111111111111", "broadcast-ch1"); err != nil {
		t.Fatalf("tile 1 (broadcast): %v", err)
	}
	if err := h.m.BeginChannel(owner, mvCaps(2), "mv-tile-2222222222222222"); err != nil {
		t.Fatalf("tile 2: %v", err)
	}
	if err := h.m.JoinChannel(owner, mvCaps(3), "mv-tile-3333333333333333", "broadcast-ch3"); err != nil {
		t.Fatalf("tile 3 (broadcast): %v", err)
	}

	// Another device for the same user is rejected under DefaultStreamLimit=1.
	if err := h.m.BeginChannel(owner, defaultCaps("vela/phone"), "other-dev-0000000000000000"); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("other device live bypassed limit: %v", err)
	}
	if _, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("vela/phone")); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("other device VOD bypassed limit: %v", err)
	}

	// Ordinary VOD on the SAME device supersedes the multiview group and is admitted.
	vodSameDev, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("vela/tv"))
	if err != nil {
		t.Fatalf("same device VOD should be admitted to supersede multiview: %v", err)
	}
	h.m.Stop(vodSameDev.ID)

	// Ending one tile keeps the group reserved until all 4 end.
	h.m.EndChannel("mv-tile-0000000000000000")
	if err := h.m.BeginChannel(owner, defaultCaps("vela/phone"), "other-dev-0000000000000000"); !errors.Is(err, core.ErrStreamLimit) {
		t.Fatalf("group freed prematurely after 1 of 4 tiles ended: %v", err)
	}
	h.m.EndChannel("mv-tile-1111111111111111")
	h.m.EndChannel("mv-tile-2222222222222222")
	h.m.EndChannel("mv-tile-3333333333333333")
	if err := h.m.BeginChannel(owner, defaultCaps("vela/phone"), "other-dev-0000000000000000"); err != nil {
		t.Fatalf("group did not release after all 4 tiles ended: %v", err)
	}
}
