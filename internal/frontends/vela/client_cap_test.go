package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type capHarness struct {
	i      *instance
	plans  []frontend.PlanRequest
	starts []frontend.StreamRequest
	plan   frontend.PlayPlan
}

func newCapHarness() *capHarness {
	h := &capHarness{}
	broker := frontendtest.Sessions{
		PlanFunc: func(_ context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
			h.plans = append(h.plans, req)
			return h.plan, nil
		},
		OpenFunc: func(context.Context, frontend.OpenRequest) (frontend.Session, error) {
			return frontend.Session{ID: "session"}, nil
		},
		StartStreamFunc: func(_ context.Context, _ frontend.SessionAccess, _ string, req frontend.StreamRequest) (string, error) {
			h.starts = append(h.starts, req)
			if req.Format == frontend.StreamHLS {
				return "/sessions/media/session/grant/master.m3u8", nil
			}
			return "/sessions/media/session/grant/file", nil
		},
	}
	declare := frontendtest.Capabilities{BuildFunc: func(ctx context.Context, req frontend.CapabilityRequest) (frontend.CapabilityResult, error) {
		c, err := caps.Builder{}.Build(ctx, req.Device, req.Profile, req.ProtocolInfo, req.UserAgent)
		return frontend.CapabilityResult{Client: c}, err
	}}
	h.i = &instance{ctx: context.Background(), host: frontend.Host{Identity: identityFake{}, Sessions: broker, Capabilities: declare}, sessions: map[string]*playSession{}}
	return h
}

func (h *capHarness) call(handler http.HandlerFunc, b object) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(b)
	r := httptest.NewRequest("POST", "/vela/v0/test", nil)
	r.SetPathValue("id", "session")
	r.Header.Set("Authorization", "Bearer access")
	r.Header.Set("X-Vela-Device-Id", "device")
	r = r.WithContext(context.WithValue(r.Context(), bodyKey{}, json.RawMessage(raw)))
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}

func capClient(maxBitrate int) object {
	c := object{"device": object{"id": "device", "platform": "ios"}, "hls": true, "maxWidth": 1920, "maxHeight": 1080, "videoCodecs": []string{"h264", "hevc"}, "audioCodecs": []string{"aac"}, "containers": []string{"mp4"}, "originalPlayback": object{"version": 1, "range": true, "demuxContainers": []string{"mkv"}, "videoDecoders": []string{"hevc"}}}
	if maxBitrate != 0 {
		c["maxBitrate"] = maxBitrate
	}
	return c
}

var capMedia = core.MediaInfo{Container: "mkv", Size: 40_000_000_000, Duration: 2 * time.Hour, BitrateKbps: 40_000, Streams: []core.StreamInfo{{Type: "video", Index: 0, Codec: "hevc", Width: 3840, Height: 2160}, {Type: "audio", Index: 1, Codec: "aac"}}}

// Contract 1 on the wire: the converted path is selected, the original is an
// on-demand explicit choice, and switching to it serves the file.
func TestClientCapOriginalIsExplicitChoice(t *testing.T) {
	h := newCapHarness()
	original := core.Decision{Kind: core.DecisionDirectPlay, Reason: "original_runtime", AudioStream: 1, SubtitleStream: -1}
	h.plan = frontend.PlayPlan{ID: "plan", ETag: "src", HLS: true, Transcoding: true, Media: capMedia, Decision: core.Decision{Kind: core.DecisionTranscode, Reason: "transcode: bitrate cap", AudioStream: 1, SubtitleStream: -1}, OriginalOverClientCap: &original}
	w := h.call(h.i.playback, object{"titleId": ids.RatingKeyGUID(core.MovieKey(1)), "positionSeconds": 0.0, "client": capClient(8_000_000)})
	if w.Code != 200 {
		t.Fatalf("playback %d %s", w.Code, w.Body.String())
	}
	req := h.plans[0]
	if req.Intent.Options.MaxBitrateKbps != 8_000 || req.Caps.MaxBitrateKbps != 8_000 {
		t.Fatalf("cap not passed to planning: %+v %+v", req.Intent.Options, req.Caps.MaxBitrateKbps)
	}
	plan := h.i.sessions["session"].plan
	paths := plan["paths"].([]object)
	if plan["selectedPathId"] != "primary" || paths[0]["transport"] != "hls" || paths[0]["original"] != nil {
		t.Fatalf("original selected over the cap: %+v", plan)
	}
	choice := paths[len(paths)-1]
	if choice["id"] != "original" || choice["original"] != true || choice["readiness"] != "on-demand" || choice["url"] != nil || choice["exceedsClientCap"] != true || choice["exceedsMaxBitrate"] != true || choice["bitrate"] != 40_000_000 || choice["byteLength"] != capMedia.Size {
		t.Fatalf("original choice: %+v", choice)
	}
	if len(h.starts) != 1 || h.starts[0].Format != frontend.StreamHLS {
		t.Fatalf("unselected original started: %+v", h.starts)
	}
	w = h.call(h.i.command, object{"kind": "switch", "pathId": "original", "atSeconds": 30.0, "sourceGeneration": 1, "seekGeneration": 1})
	if w.Code != 200 || len(h.starts) != 2 || h.starts[1].Format != frontend.StreamDirect || choice["url"] == nil || plan["selectedPathId"] != "original" {
		t.Fatalf("switch to original %d %s %+v", w.Code, w.Body.String(), h.starts)
	}
	// Switching back to the converted primary path resumes HLS.
	w = h.call(h.i.command, object{"kind": "switch", "pathId": "primary", "atSeconds": 45.0, "sourceGeneration": 2, "seekGeneration": 2})
	if w.Code != 200 || len(h.starts) != 3 || h.starts[2].Format != frontend.StreamHLS || plan["selectedPathId"] != "primary" {
		t.Fatalf("switch back to primary %d %s %+v", w.Code, w.Body.String(), h.starts)
	}
}

// Contract 1 when resolution box triggered conversion while bitrate was within
// maxBitrate: exceedsClientCap is true, exceedsMaxBitrate is omitted.
func TestClientCapResolutionOnlyOriginalChoice(t *testing.T) {
	h := newCapHarness()
	hdMP4 := core.MediaInfo{Container: "mp4", Size: 2_700_000_000, Duration: 2 * time.Hour, BitrateKbps: 3_000, Streams: []core.StreamInfo{{Type: "video", Index: 0, Codec: "h264", Width: 1920, Height: 1080}, {Type: "audio", Index: 1, Codec: "aac"}}}
	original := core.Decision{Kind: core.DecisionDirectPlay, AudioStream: 1, SubtitleStream: -1}
	h.plan = frontend.PlayPlan{ID: "plan", ETag: "src", HLS: true, Transcoding: true, Media: hdMP4, Decision: core.Decision{Kind: core.DecisionTranscode, Reason: "transcode: height cap", AudioStream: 1, SubtitleStream: -1}, OriginalOverClientCap: &original}
	c := capClient(4_000_000)
	c["maxWidth"], c["maxHeight"] = 1280, 720
	w := h.call(h.i.playback, object{"titleId": ids.RatingKeyGUID(core.MovieKey(1)), "positionSeconds": 0.0, "client": c})
	if w.Code != 200 {
		t.Fatalf("playback %d %s", w.Code, w.Body.String())
	}
	paths := h.i.sessions["session"].plan["paths"].([]object)
	choice := paths[len(paths)-1]
	if choice["id"] != "original" || choice["exceedsClientCap"] != true || choice["exceedsMaxBitrate"] != nil || choice["original"] != nil || choice["bitrate"] != 3_000_000 {
		t.Fatalf("unexpected resolution-only original choice: %+v", choice)
	}
}

// Contract 3: live starts plan with the cap, so the live service starts at
// and stays on the variant within it.
func TestClientCapLiveStart(t *testing.T) {
	h := newCapHarness()
	h.plan = frontend.PlayPlan{ID: "plan", HLS: true, Transcoding: true, Media: core.MediaInfo{Container: "mpegts", Estimated: true}, Decision: core.Decision{Kind: core.DecisionRemux, Reason: "live_remux_hls"}}
	w := h.call(h.i.playback, object{"channelId": "ch1", "liveMode": "adaptive", "client": capClient(3_000_000)})
	if w.Code != 200 {
		t.Fatalf("live %d %s", w.Code, w.Body.String())
	}
	req := h.plans[0]
	if req.Caps.MaxBitrateKbps != 3_000 || req.Intent.Options.MaxBitrateKbps != 3_000 {
		t.Fatalf("live start not capped: caps %d options %d", req.Caps.MaxBitrateKbps, req.Intent.Options.MaxBitrateKbps)
	}
	for _, p := range h.i.sessions["session"].plan["paths"].([]object) {
		if p["id"] == "original" {
			t.Fatal("live offered an original")
		}
	}
}

// Contract 4: maxWidth/maxHeight without maxBitrate carry no bitrate cap and
// no original choice.
func TestClientBoxWithoutBitrateUnchanged(t *testing.T) {
	h := newCapHarness()
	h.plan = frontend.PlayPlan{ID: "plan", ETag: "src", HLS: true, Transcoding: true, Media: capMedia, Decision: core.Decision{Kind: core.DecisionTranscode, AudioStream: 1, SubtitleStream: -1}}
	w := h.call(h.i.playback, object{"titleId": ids.RatingKeyGUID(core.MovieKey(1)), "positionSeconds": 0.0, "client": capClient(0)})
	if w.Code != 200 {
		t.Fatalf("playback %d %s", w.Code, w.Body.String())
	}
	req := h.plans[0]
	if req.Intent.Options.MaxBitrateKbps != 0 || req.Caps.MaxBitrateKbps != 0 || req.Intent.Options.MaxWidth != 1920 || req.Intent.Options.MaxHeight != 1080 {
		t.Fatalf("decoder box changed: %+v caps %d", req.Intent.Options, req.Caps.MaxBitrateKbps)
	}
	for _, p := range h.i.sessions["session"].plan["paths"].([]object) {
		if p["id"] == "original" {
			t.Fatal("original choice without a cap")
		}
	}
}

func TestClientBitrateKbpsConversion(t *testing.T) {
	if got := clientBitrateKbps(0); got != 0 {
		t.Fatalf("0 bps = %d", got)
	}
	if got := clientBitrateKbps(-5000); got != 0 {
		t.Fatalf("negative bps = %d", got)
	}
	if got := clientBitrateKbps(500); got != 1 {
		t.Fatalf("500 bps = %d, want 1", got)
	}
	if got := clientBitrateKbps(8_000_000); got != 8_000 {
		t.Fatalf("8M bps = %d, want 8000", got)
	}
}
