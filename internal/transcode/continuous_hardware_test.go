package transcode

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// Exercise the cross-generation invariant: one software retry, no sequence
// holes, and the same frame-exact cuts even inside the opening ladder.
func TestContinuousHardwareRecovery(t *testing.T) {
	for _, mode := range []string{AccelQSV, AccelVAAPI, AccelNVENC, AccelVideoToolbox} {
		t.Run(mode, func(t *testing.T) {
			runner := &continuousHardwareRunner{fakeRunner: newFakeRunner()}
			m := newTestManager(t, runner.fakeRunner, config.Transcode{}, WithoutHWDetection(), WithRunner(runner))
			m.hw = core.HWAccel{Name: mode, Device: "/dev/dri/renderD129", Encoders: []string{"h264_" + mode}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			e := &continuousEncoder{m: m, continuousProcess: &continuousProcess{ctx: ctx, cancel: cancel}, dir: t.TempDir(), stream: newContinuousFanout(), req: EncoderRequest{Source: RawFormat{Width: 320, Height: 180, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"}, Decision: core.Decision{VideoCodec: "h264", AudioCodec: "aac", BitrateKbps: 1500}, SegmentDuration: 2 * time.Second, LiveSource: true}}
			e.index = newContinuousSegmentIndex(e)
			var err error
			e.store, err = newContinuousStore(e.dir, 4, e.index.kick)
			if err != nil {
				t.Fatal(err)
			}
			defer e.store.close()
			child, err := e.startOrdinaryChild(0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { child.forceClose() }()
			args := strings.Join(runner.args[0], " ")
			if !strings.Contains(args, "-c:v h264_"+mode) {
				t.Fatal(args)
			}
			if mode == AccelQSV && (!strings.Contains(args, "-qsv_device /dev/dri/renderD129") || !strings.Contains(args, "-forced_idr 1")) {
				t.Fatal(args)
			}
			if mode == AccelVAAPI && (!strings.Contains(args, "-vaapi_device /dev/dri/renderD129") || !strings.Contains(args, "hwupload")) {
				t.Fatal(args)
			}
			// Segment zero is published, but the hardware dies during segment one.
			e.index.next = 1
			child.forceClose()
			if err := e.recoverFromEncoderFailure(&child, 20); err != nil {
				t.Fatal(err)
			}
			args = strings.Join(runner.args[1], " ")
			for _, want := range []string{"-c:v libx264", "start_number=1", "hls_init_time=0.500000", "-output_ts_offset 0.500000", "-force_key_frames expr:if(lt(n,105),eq(n,0)+eq(n,15)+eq(n,45)+eq(n,75),gte(n,(n_forced+1-3)*60-15))"} {
				if !strings.Contains(args, want) {
					t.Fatalf("missing %s in %s", want, args)
				}
			}
			if !e.forceCPU || child.hardware || child.startSeq != 1 || child.startTick != 20 || e.wantBoundarySwap(child) {
				t.Fatal("software fallback did not latch")
			}
			child.forceClose()
			if err := e.recoverFromEncoderFailure(&child, 21); err == nil || len(runner.args) != 2 {
				t.Fatal("software failure must terminate rather than loop")
			}
		})
	}
	t.Run("launch failure", func(t *testing.T) {
		runner := &continuousHardwareRunner{fakeRunner: newFakeRunner(), failLaunch: true}
		var logs bytes.Buffer
		m := newTestManager(t, runner.fakeRunner, config.Transcode{}, WithoutHWDetection(), WithRunner(runner))
		m.log = slog.New(slog.NewJSONHandler(&logs, nil))
		m.hw = core.HWAccel{Name: AccelQSV, Encoders: []string{"h264_qsv"}}
		enc, err := m.StartEncoder(t.Context(), EncoderRequest{Source: RawFormat{Width: 320, Height: 180, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"}})
		if err != nil {
			t.Fatal(err)
		}
		enc.Close()
		if len(runner.args) != 2 || !strings.Contains(strings.Join(runner.args[1], " "), "-c:v libx264") {
			t.Fatal("launch did not fall back once")
		}
		if !strings.Contains(logs.String(), `"hwaccel":"qsv"`) || !strings.Contains(logs.String(), `"encoder":"h264_qsv"`) {
			t.Fatalf("fallback log missing hwaccel/encoder: %s", logs.String())
		}
	})
}

func TestContinuousVAAPIArgsAndLowLatencySegment(t *testing.T) {
	for _, dev := range []string{"", "/dev/dri/renderD129"} {
		wantDev := dev
		if wantDev == "" {
			wantDev = DefaultVAAPIDevice
		}
		t.Run("device="+wantDev, func(t *testing.T) {
			runner := &continuousHardwareRunner{fakeRunner: newFakeRunner()}
			m := newTestManager(t, runner.fakeRunner, config.Transcode{}, WithoutHWDetection(), WithRunner(runner))
			m.hw = core.HWAccel{Name: AccelVAAPI, Device: dev, Encoders: []string{"h264_vaapi"}}
			enc, err := m.StartEncoder(t.Context(), EncoderRequest{
				Source:          RawFormat{Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"},
				Decision:        core.Decision{VideoCodec: "h264", AudioCodec: "aac", BitrateKbps: 5000},
				SegmentDuration: time.Second,
				LiveSource:      true,
			})
			if err != nil {
				t.Fatalf("StartEncoder(1s) failed: %v", err)
			}
			enc.Close()
			if len(runner.args) == 0 {
				t.Fatal("encoder did not start")
			}
			args := runner.args[0]
			vaIdx := slices.Index(args, "-vaapi_device")
			inIdx := slices.Index(args, "-i")
			if vaIdx < 0 || inIdx < 0 || vaIdx >= inIdx || args[vaIdx+1] != wantDev {
				t.Fatalf("-vaapi_device must precede -i with device %s: %v", wantDev, args)
			}
			joined := strings.Join(args, " ")
			for _, bad := range []string{"-color_primaries", "-color_trc", "-colorspace", "-pix_fmt", "scale=1280:720", "format=yuv420p"} {
				if strings.Contains(joined, bad) {
					t.Errorf("VAAPI continuous args must not contain %q:\n%s", bad, joined)
				}
			}
			for _, want := range []string{"setparams=color_primaries=bt709:color_trc=bt709:colorspace=bt709", "format=nv12,hwupload", "-c:v h264_vaapi"} {
				if !strings.Contains(joined, want) {
					t.Errorf("VAAPI continuous args missing %q:\n%s", want, joined)
				}
			}
		})
	}

	// Regular VAAPI software-filter paths (e.g. interlaced IPTV without
	// subtitle burn) also end in hwupload and must omit output colour flags.
	spec := runSpec{
		Input:     "http://127.0.0.1/live.ts",
		LiveInput: true,
		Protocol:  core.ProtocolHLS,
		Info: core.MediaInfo{
			Container: "mpegts",
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "mpeg2video", Width: 1920, Height: 1080, Interlaced: true},
				{Index: 1, Type: "audio", Codec: "mp2", Channels: 2, SampleRate: 48000},
			},
		},
		Decision: core.Decision{Kind: core.DecisionTranscode, VideoCodec: "h264", AudioCodec: "aac", AudioStream: 1, SubtitleStream: -1, Container: ContainerMPEGTS},
		Accel:    core.HWAccel{Name: AccelVAAPI, Encoders: []string{"h264_vaapi"}},
		Features: features{filters: map[string]bool{"yadif": true, "hwupload": true, "scale": true, "format": true}},
	}
	args := strings.Join(buildArgs(spec), " ")
	for _, bad := range []string{"-color_primaries", "-color_trc", "-colorspace"} {
		if strings.Contains(args, bad) {
			t.Errorf("VAAPI interlaced software-filter args must not emit %q:\n%s", bad, args)
		}
	}

	// Direct DASH copy of MPEG-TS AAC requires aac_adtstoasc for fMP4.
	dashSpec := runSpec{
		Input:     "http://127.0.0.1/live.ts",
		LiveInput: true,
		Protocol:  core.ProtocolDASH,
		Info: core.MediaInfo{
			Container: "mpegts",
			Streams: []core.StreamInfo{
				{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720},
				{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000},
			},
		},
		Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, VideoCodec: "h264", AudioCodec: "aac", AudioStream: 1, SubtitleStream: -1, Container: ContainerFMP4},
		Dir:      t.TempDir(),
	}
	dashArgs := strings.Join(buildArgs(dashSpec), " ")
	if !strings.Contains(dashArgs, "aac_adtstoasc") {
		t.Fatalf("DASH fMP4 MPEG-TS AAC copy missing aac_adtstoasc:\n%s", dashArgs)
	}
}

type continuousHardwareRunner struct {
	*fakeRunner
	args       [][]string
	failLaunch bool
}

func (r *continuousHardwareRunner) Start(cmd Command) (Process, error) {
	r.args = append(r.args, append([]string(nil), cmd.Args...))
	if r.failLaunch && len(r.args) == 1 {
		return nil, errors.New("hardware launch failed")
	}
	p := &fakeProc{done: make(chan struct{}), kill: make(chan struct{})}
	go func() { <-p.kill; close(p.done) }()
	return p, nil
}
