package transcode

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestQualityLadderInvariants(t *testing.T) {
	fr := newFakeRunner()
	m := newTestManager(t, fr, config.Transcode{}, WithSegmentTimeout(time.Second), withRungDwell(0))
	req := remuxRequest(t, "lazy-quality", time.Minute)
	req.Info = ladderInfo(1080, 15000)
	req.Decision = core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, AudioStream: 1, SubtitleStream: -1, Container: ContainerFMP4}
	variants, _ := VelaQualityVariants(req.Info, req.Decision, core.ClientCaps{VideoCodecs: []string{"h264"}}, 1280, 720, 2000, QualitySupport{})
	rungs := make([]core.HLSRung, 0, len(variants))
	for _, v := range variants {
		rungs = append(rungs, v.Rung)
	}
	if len(rungs) == 0 {
		t.Fatal("no lower quality")
	}
	for _, r := range rungs {
		if r.Width > 1280 || r.Height > 720 || r.Bandwidth > 2000000 {
			t.Fatal("cap exceeded", r)
		}
	}
	if got, _ := VelaQualityVariants(req.Info, req.Decision, core.ClientCaps{}, 0, 0, 0, QualitySupport{}); len(got) != 0 {
		t.Fatal("undeclared decoder", got)
	}
	req.QualityRungs, req.LazyStart, req.Continuous = true, true, true
	req.LadderVideoCodecs = []string{"h264"}
	req.QualityVariants = variants
	for _, r := range rungs {
		req.QualityRungIDs = append(req.QualityRungIDs, r.ID)
	}
	s := startSession(t, m, req)
	if fr.startCount() != 0 || len(s.Rungs()) != len(rungs)+1 {
		t.Fatal("planning started work or lost ladder", fr.startCount(), s.Rungs())
	}
	if err := s.PrepareRung(context.Background(), rungs[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	if fr.startCount() != 1 {
		t.Fatal("preparation did not select exactly one encoder", fr.startCount())
	}
	if err := s.PrepareRung(context.Background(), rungs[0].ID, 0); err != nil {
		t.Fatal(err)
	}
	if fr.startCount() != 1 {
		t.Fatal("prepared media restarted encoder")
	}
}

func TestQualityExplicitInvariants(t *testing.T) {
	info := ladderInfo(2160, 80000)
	info.Streams[0].Codec, info.Streams[0].ColorTransfer, info.Streams[0].BitDepth = "hevc", "smpte2084", 10
	base := core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, AudioStream: 1, Container: ContainerFMP4, SubtitleStream: -1}
	for _, tc := range []struct {
		name    string
		caps    core.ClientCaps
		support QualitySupport
		count   int
		hdr     bool
	}{
		{"hevc-hdr", core.ClientCaps{VideoCodecs: []string{"hevc", "h264"}, HDR10: true, HDRFormats: []string{"hdr10"}}, QualitySupport{HEVC: true, ToneMap: true}, 9, true},
		{"h264-only", core.ClientCaps{VideoCodecs: []string{"h264"}}, QualitySupport{HEVC: true, ToneMap: true}, 8, false},
		{"display-undeclared", core.ClientCaps{VideoCodecs: []string{"hevc", "h264"}}, QualitySupport{HEVC: true, ToneMap: true}, 8, false},
		{"no-hevc-encoder", core.ClientCaps{VideoCodecs: []string{"hevc", "h264"}, HDR10: true, HDRFormats: []string{"hdr10"}}, QualitySupport{ToneMap: true}, 8, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			variants, limits := VelaQualityVariants(info, base, tc.caps, 0, 0, 0, tc.support)
			if len(variants) != tc.count {
				t.Fatalf("variants=%+v limits=%+v", variants, limits)
			}
			hasHDR, hasSDR4k := false, false
			for _, v := range variants {
				r := v.Rung
				if r.Height > 2160 || r.Width > 3840 || r.VideoBitrate != videoBitrate(info, v.Decision)*1000 || r.AverageBandwidth != v.Decision.BitrateKbps*1000 || r.Bandwidth < r.AverageBandwidth {
					t.Fatal("invalid geometry/rate", v)
				}
				if r.DynamicRange != "sdr" {
					hasHDR = true
					if v.Decision.ToneMap || r.Codec != "hevc" {
						t.Fatal("false HDR", v)
					}
				}
				if r.Height == 2160 && r.DynamicRange == "sdr" {
					hasSDR4k = true
					if !v.Decision.ToneMap {
						t.Fatal("missing tone map", v)
					}
				}
				t.Logf("%dx%d %s %s video=%d average=%d peak=%d", r.Width, r.Height, r.Codec, r.DynamicRange, r.VideoBitrate, r.AverageBandwidth, r.Bandwidth)
			}
			if hasHDR != tc.hdr || !hasSDR4k {
				t.Fatal("range choices", variants)
			}
			capped, _ := VelaQualityVariants(info, base, tc.caps, 1280, 720, 5000, tc.support)
			for _, v := range capped {
				if v.Rung.Width > 1280 || v.Rung.Height > 720 || v.Rung.Bandwidth > 5000000 {
					t.Fatal("rules escaped", v)
				}
			}
		})
	}
	for _, h := range []int{360, 480, 720, 1080, 2160} {
		source := ladderInfo(h, 80000)
		variants, _ := VelaQualityVariants(source, base, core.ClientCaps{VideoCodecs: []string{"h264"}}, 0, 0, 0, QualitySupport{})
		for _, v := range variants {
			if v.Rung.Width > source.Video().Width || v.Rung.Height > source.Video().Height {
				t.Fatal("upscaled", v)
			}
		}
	}
}
