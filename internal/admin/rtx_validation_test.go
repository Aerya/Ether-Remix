package admin_test

import (
	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"strings"
	"testing"
)

func TestRTXVideoConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*config.RTXVideo)
		field  string
	}{
		{"mode", func(c *config.RTXVideo) { c.Mode = "fake" }, "mode"},
		{"source", func(c *config.RTXVideo) { c.SourceMaxHeight = -1 }, "sourceMaxHeight"},
		{"target", func(c *config.RTXVideo) { c.TargetMaxHeight = 4320 }, "targetMaxHeight"},
		{"quality", func(c *config.RTXVideo) { c.Quality = 22 }, "quality"},
		{"concurrency", func(c *config.RTXVideo) { c.MaxConcurrent = 9 }, "maxConcurrent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Defaults(t.TempDir())
			tc.mutate(&cfg.Transcode.RTXVideo)
			for _, e := range admin.ValidateConfig(cfg) {
				if e.Field == "transcode.rtxVideo."+tc.field {
					return
				}
			}
			t.Fatal("missing field validation")
		})
	}
	for _, quality := range []int{0, 1, 2, 3, 4, 21, 23} {
		cfg := config.Defaults(t.TempDir())
		cfg.Transcode.RTXVideo = config.RTXVideo{Quality: quality}
		for _, e := range admin.ValidateConfig(cfg) {
			if strings.HasPrefix(e.Field, "transcode.rtxVideo.") {
				t.Fatal(e)
			}
		}
	}
}

func TestVideoTranscodingConfigValidation(t *testing.T) {
	for _, valid := range []string{"", "on", "off"} {
		cfg := config.Defaults(t.TempDir())
		cfg.Transcode.VideoTranscoding = valid
		for _, e := range admin.ValidateConfig(cfg) {
			if e.Field == "transcode.videoTranscoding" {
				t.Fatalf("valid value %q rejected: %v", valid, e)
			}
		}
	}
	cfg := config.Defaults(t.TempDir())
	cfg.Transcode.VideoTranscoding = "sometimes"
	for _, e := range admin.ValidateConfig(cfg) {
		if e.Field == "transcode.videoTranscoding" {
			return
		}
	}
	t.Fatal("invalid transcode.videoTranscoding value was accepted")
}

func TestThrottleSecondsValidation(t *testing.T) {
	for _, valid := range []int{0, 16, 60, 600} {
		cfg := config.Defaults(t.TempDir())
		cfg.Transcode.ThrottleSeconds = valid
		for _, e := range admin.ValidateConfig(cfg) {
			if e.Field == "transcode.throttleSeconds" {
				t.Fatalf("valid throttleSeconds=%d rejected: %v", valid, e)
			}
		}
	}
	for _, invalid := range []int{-1, 8, 15, 601} {
		cfg := config.Defaults(t.TempDir())
		cfg.Transcode.ThrottleSeconds = invalid
		found := false
		for _, e := range admin.ValidateConfig(cfg) {
			if e.Field == "transcode.throttleSeconds" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("invalid throttleSeconds=%d was accepted", invalid)
		}
	}
}
