package resolver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestVideoTranscodingOffPrefersDirectlyPlayableRelease(t *testing.T) {
	cfg := testCfg()
	cfg.Quality.Default = "4K"
	cfg.Transcode.VideoTranscoding = "off"

	// Client supports H.264 1080p SDR + AAC only (no HEVC, no HDR, no TrueHD).
	caps := core.ClientCaps{
		Product:     "Plex Web",
		Platform:    "Chrome",
		MaxHeight:   1080,
		VideoCodecs: []string{"h264"},
		AudioCodecs: []string{"aac"},
		Containers:  []string{"mp4"},
	}

	hevc4k := rel("uhd", "The.Signal.1999.2160p.UHD.BluRay.HDR10.x265-GRP", 20000)
	hevc1080 := rel("hevc1080", "The.Signal.1999.1080p.WEB-DL.H.265-GRP", 8000)
	// H.264 1080p SDR with TrueHD audio: video copies (remux + audio conversion allowed).
	h264TrueHD := rel("h264-truehd", "The.Signal.1999.1080p.BluRay.x264.TrueHD-GRP", 10000)

	ix := &fakeIndexer{releases: []core.Release{hevc4k, hevc1080, h264TrueHD}}
	s := New(ix, nil, nil, cfg, Options{})
	defer s.Close()

	res, err := s.Resolve(context.Background(), core.ResolveRequest{
		Title:  matrixTitle(),
		Client: caps,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v, want directly playable H.264+TrueHD release", err)
	}
	if res.Release.ID != "h264-truehd" {
		t.Fatalf("winner = %q, want h264-truehd", res.Release.ID)
	}

	traces := s.Traces()
	if len(traces) == 0 {
		t.Fatal("expected decision trace")
	}
	reasons := map[string]string{}
	for _, c := range traces[len(traces)-1].Candidates {
		if c.Rejected {
			reasons[c.ReleaseID] = c.Reason
		}
	}
	for _, id := range []string{"uhd", "hevc1080"} {
		if reasons[id] != ReasonNeedsVideoTranscode {
			t.Fatalf("candidate %s rejection reason = %q, want %q", id, reasons[id], ReasonNeedsVideoTranscode)
		}
	}
}

func TestVideoTranscodingOffFailsClearlyWhenNoReleaseCanPlay(t *testing.T) {
	cfg := testCfg()
	cfg.Transcode.VideoTranscoding = "off"

	caps := core.ClientCaps{
		Product:     "Plex Web",
		Platform:    "Chrome",
		MaxHeight:   1080,
		VideoCodecs: []string{"h264"},
		AudioCodecs: []string{"aac"},
		Containers:  []string{"mp4"},
	}

	hevcOnly := rel("hevc", "The.Signal.1999.1080p.WEB-DL.x265-GRP", 8000)
	hdrOnly := rel("hdr", "The.Signal.1999.1080p.WEB-DL.HDR10.x264-GRP", 8000)

	ix := &fakeIndexer{releases: []core.Release{hevcOnly, hdrOnly}}
	s := New(ix, nil, nil, cfg, Options{})
	defer s.Close()

	_, err := s.Resolve(context.Background(), core.ResolveRequest{
		Title:  matrixTitle(),
		Client: caps,
	})
	if !errors.Is(err, core.ErrVideoTranscodingDisabled) {
		t.Fatalf("Resolve() err = %v, want ErrVideoTranscodingDisabled", err)
	}

	traces := s.Traces()
	if len(traces) == 0 {
		t.Fatal("expected decision trace")
	}
	last := traces[len(traces)-1]
	if last.Err != "no release can play without a video transcode, and transcoding is off" {
		t.Fatalf("trace.Err = %q", last.Err)
	}

	// Must not poison global availability for a capable client.
	capable := core.ClientCaps{
		Product:     "Apple TV",
		Platform:    "tvOS",
		MaxHeight:   2160,
		HDR10:       true,
		VideoCodecs: []string{"h264", "hevc"},
		AudioCodecs: []string{"aac", "eac3"},
	}
	res, err := s.Resolve(context.Background(), core.ResolveRequest{
		Title:  matrixTitle(),
		Client: capable,
	})
	if err != nil || res == nil {
		t.Fatalf("capable client should still resolve after incompatible client failed: res=%v err=%v", res, err)
	}
}

func TestVideoTranscodingOffRejectsBitrateAndPinnedIncompatible(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	cfg.Transcode.VideoTranscoding = "off"
	cfg.Transcode.RemoteMaxBitrateKbps = 6000

	caps := core.ClientCaps{
		Product:     "Plex for iOS",
		Platform:    "iOS",
		Remote:      true,
		MaxHeight:   1080,
		VideoCodecs: []string{"h264"},
		AudioCodecs: []string{"aac"},
	}
	title := matrixTitle()
	title.Runtime = 100 * time.Minute

	// 12 GB over 100 min = ~16 Mbps > 6 Mbps remote cap -> needs video transcode.
	heavy := rel("heavy", "The.Signal.1999.1080p.BluRay.x264-GRP", 12000)
	// 3 GB over 100 min = ~4 Mbps <= 6 Mbps -> fits without video transcode.
	light := rel("light", "The.Signal.1999.1080p.WEB-DL.x264-GRP", 3000)

	ix := &fakeIndexer{releases: []core.Release{heavy, light}}
	s := New(ix, nil, nil, cfg, Options{})
	defer s.Close()

	res, err := s.Resolve(context.Background(), core.ResolveRequest{
		Title:  title,
		Client: caps,
	})
	if err != nil || res.Release.ID != "light" {
		t.Fatalf("Resolve() = %v, %v; want light", res, err)
	}

	// Pinned release needing a video transcode is also rejected cleanly with ErrVideoTranscodingDisabled.
	hevcPin := withParsed(rel("pin-hevc", "The.Signal.1999.1080p.WEB-DL.HEVC-GRP", 3000))
	if _, err := s.Pin(context.Background(), title.Key, "", hevcPin, ""); err != nil {
		t.Fatal(err)
	}
	_, err = s.Resolve(context.Background(), core.ResolveRequest{
		Title:  title,
		Client: caps,
	})
	if !errors.Is(err, core.ErrVideoTranscodingDisabled) {
		t.Fatalf("with incompatible pin: Resolve() err = %v, want ErrVideoTranscodingDisabled", err)
	}
}

func TestPerViewerNoVideoTranscodeMirrorsServerSwitch(t *testing.T) {
	t.Run("prefers directly playable release when server transcoding is on", func(t *testing.T) {
		cfg := testCfg()
		cfg.Quality.Default = "4K"
		cfg.Transcode.VideoTranscoding = "on"

		caps := core.ClientCaps{
			Product:          "Plex Web",
			Platform:         "Chrome",
			MaxHeight:        1080,
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"aac"},
			Containers:       []string{"mp4"},
			NoVideoTranscode: true,
		}

		hevc4k := rel("uhd", "The.Signal.1999.2160p.UHD.BluRay.HDR10.x265-GRP", 20000)
		hevc1080 := rel("hevc1080", "The.Signal.1999.1080p.WEB-DL.H.265-GRP", 8000)
		h264TrueHD := rel("h264-truehd", "The.Signal.1999.1080p.BluRay.x264.TrueHD-GRP", 10000)

		ix := &fakeIndexer{releases: []core.Release{hevc4k, hevc1080, h264TrueHD}}
		s := New(ix, nil, nil, cfg, Options{})
		defer s.Close()

		res, err := s.Resolve(context.Background(), core.ResolveRequest{
			Title:  matrixTitle(),
			Client: caps,
		})
		if err != nil {
			t.Fatalf("Resolve() error = %v, want directly playable H.264+TrueHD release", err)
		}
		if res.Release.ID != "h264-truehd" {
			t.Fatalf("winner = %q, want h264-truehd", res.Release.ID)
		}
		traces := s.Traces()
		reasons := map[string]string{}
		for _, c := range traces[len(traces)-1].Candidates {
			if c.Rejected {
				reasons[c.ReleaseID] = c.Reason
			}
		}
		for _, id := range []string{"uhd", "hevc1080"} {
			if reasons[id] != ReasonNeedsVideoTranscode {
				t.Fatalf("candidate %s rejection reason = %q, want %q", id, reasons[id], ReasonNeedsVideoTranscode)
			}
		}
	})

	t.Run("fails clearly without poisoning unrestricted viewer", func(t *testing.T) {
		cfg := testCfg()
		cfg.Transcode.VideoTranscoding = "on"

		restricted := core.ClientCaps{
			Product:          "Plex Web",
			Platform:         "Chrome",
			MaxHeight:        1080,
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"aac"},
			Containers:       []string{"mp4"},
			NoVideoTranscode: true,
		}
		hevcOnly := rel("hevc", "The.Signal.1999.1080p.WEB-DL.x265-GRP", 8000)
		hdrOnly := rel("hdr", "The.Signal.1999.1080p.WEB-DL.HDR10.x264-GRP", 8000)

		ix := &fakeIndexer{releases: []core.Release{hevcOnly, hdrOnly}}
		s := New(ix, nil, nil, cfg, Options{})
		defer s.Close()

		_, err := s.Resolve(context.Background(), core.ResolveRequest{
			Title:  matrixTitle(),
			Client: restricted,
		})
		if !errors.Is(err, core.ErrVideoTranscodingDisabled) {
			t.Fatalf("Resolve(restricted) err = %v, want ErrVideoTranscodingDisabled", err)
		}

		// Same H.264-only device for a viewer WITHOUT NoVideoTranscode must still resolve.
		unrestricted := restricted
		unrestricted.NoVideoTranscode = false
		res, err := s.Resolve(context.Background(), core.ResolveRequest{
			Title:  matrixTitle(),
			Client: unrestricted,
		})
		if err != nil || res == nil {
			t.Fatalf("unrestricted viewer should still resolve after restricted viewer failed: res=%v err=%v", res, err)
		}
	})

	t.Run("rejects remote bitrate and incompatible pin per viewer", func(t *testing.T) {
		cfg := config.Defaults(t.TempDir())
		cfg.Transcode.VideoTranscoding = "on"
		cfg.Transcode.RemoteMaxBitrateKbps = 6000

		caps := core.ClientCaps{
			Product:          "Plex for iOS",
			Platform:         "iOS",
			Remote:           true,
			MaxHeight:        1080,
			VideoCodecs:      []string{"h264"},
			AudioCodecs:      []string{"aac"},
			NoVideoTranscode: true,
		}
		title := matrixTitle()
		title.Runtime = 100 * time.Minute

		heavy := rel("heavy", "The.Signal.1999.1080p.BluRay.x264-GRP", 12000)
		light := rel("light", "The.Signal.1999.1080p.WEB-DL.x264-GRP", 3000)

		ix := &fakeIndexer{releases: []core.Release{heavy, light}}
		s := New(ix, nil, nil, cfg, Options{})
		defer s.Close()

		res, err := s.Resolve(context.Background(), core.ResolveRequest{
			Title:  title,
			Client: caps,
		})
		if err != nil || res.Release.ID != "light" {
			t.Fatalf("Resolve() = %v, %v; want light", res, err)
		}

		hevcPin := withParsed(rel("pin-hevc", "The.Signal.1999.1080p.WEB-DL.HEVC-GRP", 3000))
		if _, err := s.Pin(context.Background(), title.Key, "", hevcPin, ""); err != nil {
			t.Fatal(err)
		}
		_, err = s.Resolve(context.Background(), core.ResolveRequest{
			Title:  title,
			Client: caps,
		})
		if !errors.Is(err, core.ErrVideoTranscodingDisabled) {
			t.Fatalf("with incompatible pin: Resolve() err = %v, want ErrVideoTranscodingDisabled", err)
		}
	})
}
