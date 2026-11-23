package playback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type realDecideTranscoder struct {
	*fakeTranscoder
	videoOff bool
}

func (r realDecideTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	return transcode.Decide(info, caps, opts, transcode.Policy{})
}

func (r realDecideTranscoder) VideoTranscodingOff() bool { return r.videoOff }

func TestVideoTranscodingOffSkipsProbedEncodeCandidateAndFailsClearly(t *testing.T) {
	hevcRelease := mkv("HEVC", 100_000, 100*time.Second)
	hevcRelease.Codec = "hevc"
	hevcRelease.Height = 1080
	hevcRelease.BitrateKbps = 6000
	// H.264 1080p SDR: video copies (remux + audio conversion allowed when off).
	h264Release := mkv("H264", 100_000, 100*time.Second)
	h264Release.Codec = "h264"
	h264Release.Height = 1080
	h264Release.BitrateKbps = 6000

	h264OnlyCaps := core.ClientCaps{
		Product:     "Plex Web",
		Platform:    "Chrome",
		MaxHeight:   1080,
		VideoCodecs: []string{"h264"},
		AudioCodecs: []string{"aac"},
		Containers:  []string{"mp4"},
	}

	t.Run("skips probed HEVC and selects probed H264", func(t *testing.T) {
		h := newHarness(t, []*fakeRelease{hevcRelease, h264Release}, []string{"HEVC", "H264"}, func(h *harness, _ *Options) {
			h.cfg.Transcode.VideoTranscoding = "off"
			h.testTranscode = realDecideTranscoder{fakeTranscoder: h.tr, videoOff: true}
		})
		ps, err := h.m.Prepare(context.Background(), owner, movieKey, h264OnlyCaps)
		if err != nil {
			t.Fatalf("Prepare() error = %v", err)
		}
		if ps.Resolution == nil || ps.Resolution.Release.ID != "H264" {
			t.Fatalf("selected release = %+v, want H264", ps.Resolution)
		}
	})

	t.Run("fails with ErrVideoTranscodingDisabled when every probed candidate needs video encode", func(t *testing.T) {
		h := newHarness(t, []*fakeRelease{hevcRelease}, []string{"HEVC"}, func(h *harness, _ *Options) {
			h.cfg.Transcode.VideoTranscoding = "off"
			h.testTranscode = realDecideTranscoder{fakeTranscoder: h.tr, videoOff: true}
		})
		_, err := h.m.Prepare(context.Background(), owner, movieKey, h264OnlyCaps)
		if !errors.Is(err, core.ErrVideoTranscodingDisabled) {
			t.Fatalf("Prepare() err = %v, want ErrVideoTranscodingDisabled", err)
		}
	})
}

func TestPerViewerNoVideoTranscodeSkipsProbedAndReplacesHotSession(t *testing.T) {
	hevcRelease := mkv("HEVC", 100_000, 100*time.Second)
	hevcRelease.Codec = "hevc"
	hevcRelease.Height = 1080
	hevcRelease.BitrateKbps = 6000
	heavyH264 := mkv("HEAVY", 100_000, 100*time.Second)
	heavyH264.Codec = "h264"
	heavyH264.Height = 1080
	heavyH264.BitrateKbps = 31000
	lightH264 := mkv("LIGHT", 100_000, 100*time.Second)
	lightH264.Codec = "h264"
	lightH264.Height = 1080
	lightH264.BitrateKbps = 1500

	h264OnlyCaps := core.ClientCaps{
		ClientID:         "web-1",
		Product:          "Plex Web",
		Platform:         "Chrome",
		MaxHeight:        1080,
		VideoCodecs:      []string{"h264"},
		AudioCodecs:      []string{"aac"},
		Containers:       []string{"mp4"},
		NoVideoTranscode: true,
	}

	t.Run("skips probed HEVC and selects probed H264 with server switch on", func(t *testing.T) {
		h := newHarness(t, []*fakeRelease{hevcRelease, lightH264}, []string{"HEVC", "LIGHT"}, func(h *harness, _ *Options) {
			h.cfg.Transcode.VideoTranscoding = "on"
			h.testTranscode = realDecideTranscoder{fakeTranscoder: h.tr, videoOff: false}
		})
		ps, err := h.m.Prepare(context.Background(), owner, movieKey, h264OnlyCaps)
		if err != nil {
			t.Fatalf("Prepare() error = %v", err)
		}
		if ps.Resolution == nil || ps.Resolution.Release.ID != "LIGHT" {
			t.Fatalf("selected release = %+v, want LIGHT", ps.Resolution)
		}
	})

	t.Run("fails with ErrVideoTranscodingDisabled when every candidate needs encode", func(t *testing.T) {
		h := newHarness(t, []*fakeRelease{hevcRelease}, []string{"HEVC"}, func(h *harness, _ *Options) {
			h.cfg.Transcode.VideoTranscoding = "on"
			h.testTranscode = realDecideTranscoder{fakeTranscoder: h.tr, videoOff: false}
		})
		_, err := h.m.Prepare(context.Background(), owner, movieKey, h264OnlyCaps)
		if !errors.Is(err, core.ErrVideoTranscodingDisabled) {
			t.Fatalf("Prepare() err = %v, want ErrVideoTranscodingDisabled", err)
		}
	})

	t.Run("replaces hot high-bitrate session when NoVideoTranscode is enabled", func(t *testing.T) {
		h := newHarness(t, []*fakeRelease{heavyH264, lightH264}, []string{"HEAVY", "LIGHT"}, func(h *harness, _ *Options) {
			h.cfg.Transcode.VideoTranscoding = "on"
			h.testTranscode = realDecideTranscoder{fakeTranscoder: h.tr, videoOff: false}
		})
		// First request: switch off -> opens and holds hot 31 Mbps release.
		openCaps := h264OnlyCaps
		openCaps.NoVideoTranscode = false
		first, err := h.m.Prepare(context.Background(), owner, movieKey, openCaps)
		if err != nil {
			t.Fatalf("first Prepare() error = %v", err)
		}
		if first.Resolution == nil || first.Resolution.Release.ID != "HEAVY" {
			t.Fatalf("first release = %+v, want HEAVY", first.Resolution)
		}

		// Second request: switch ON + 4 Mbps cap -> must NOT reuse HEAVY, must re-resolve to LIGHT.
		cappedCaps := h264OnlyCaps
		cappedCaps.MaxBitrateKbps = 4000
		second, err := h.m.Prepare(context.Background(), owner, movieKey, cappedCaps)
		if err != nil {
			t.Fatalf("second Prepare() error = %v, want re-resolved LIGHT", err)
		}
		if second.Resolution == nil || second.Resolution.Release.ID != "LIGHT" {
			t.Fatalf("second release = %+v, want LIGHT", second.Resolution)
		}
		if second.ID == first.ID {
			t.Fatalf("expected hot session %s to be replaced, got same session ID", first.ID)
		}
	})

	t.Run("NoVideoTranscode overrides AlwaysEncodeVideo so copyable H264 is accepted", func(t *testing.T) {
		h := newHarness(t, []*fakeRelease{lightH264}, []string{"LIGHT"}, func(h *harness, _ *Options) {
			h.cfg.Transcode.VideoTranscoding = "on"
			h.testTranscode = realDecideTranscoder{fakeTranscoder: h.tr, videoOff: false}
		})
		conflictCaps := h264OnlyCaps
		conflictCaps.AlwaysEncodeVideo = true
		ps, err := h.m.Prepare(context.Background(), owner, movieKey, conflictCaps)
		if err != nil {
			t.Fatalf("Prepare(NoVideoTranscode+AlwaysEncodeVideo) err = %v, want LIGHT accepted", err)
		}
		if ps.Resolution == nil || ps.Resolution.Release.ID != "LIGHT" {
			t.Fatalf("selected release = %+v, want LIGHT", ps.Resolution)
		}
	})
}
