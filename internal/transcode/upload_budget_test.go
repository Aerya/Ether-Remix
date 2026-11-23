package transcode

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestSessionBudgetCapStepDownAndUpMPEGTS(t *testing.T) {
	baseDec := core.Decision{
		Kind:        core.DecisionTranscode,
		Container:   "mpegts",
		VideoCodec:  "h264",
		AudioCodec:  "aac",
		MaxHeight:   1080,
		BitrateKbps: 10_000,
	}
	midDec := baseDec
	midDec.MaxHeight = 720
	midDec.BitrateKbps = 4_000
	lowDec := baseDec
	lowDec.MaxHeight = 480
	lowDec.BitrateKbps = 2_000

	s := &Session{
		id:        "budget-ts",
		proto:     core.ProtocolHLS,
		container: ContainerMPEGTS,
		decision:  baseDec,
		rungs: []rung{
			{id: "1080p", avgKbps: 10_000, decision: baseDec},
			{id: "720p", avgKbps: 4_000, decision: midDec},
			{id: "480p", avgKbps: 2_000, decision: lowDec},
		},
		m: &Manager{cfg: config.Transcode{}},
	}

	rungs, cur, ok := s.AdaptiveBudgetInfo()
	if !ok || len(rungs) != 3 || cur != 0 {
		t.Fatalf("expected adaptive rungs for MPEG-TS transcode, got (%v, %d, %v)", rungs, cur, ok)
	}

	// Step down to 4000 kbps: should select the 720p rung and mark active.
	applied, rungID := s.SetBudgetCapKbps(4_000)
	if !applied || rungID != "720p" {
		t.Fatalf("expected step-down to 720p, got (%v, %q)", applied, rungID)
	}
	capVal, active, activeRung := s.BudgetStatus()
	if !active || capVal != 4_000 || activeRung != "720p" {
		t.Fatalf("budget status = (%d, %v, %q), want (4000, true, 720p)", capVal, active, activeRung)
	}
	// Effective decision bitrate must stay within the budget cap.
	s.mu.Lock()
	eff := s.applyBudgetDecisionLocked(0, baseDec)
	s.mu.Unlock()
	if eff.BitrateKbps <= 0 || eff.BitrateKbps > 4_000 || eff.MaxHeight != 720 {
		t.Fatalf("effective decision = %+v, want 720p <= 4000 kbps", eff)
	}

	// Lifting the cap (0) restores original decision cleanly.
	applied, _ = s.SetBudgetCapKbps(0)
	if !applied {
		t.Fatalf("expected lifting budget cap to apply")
	}
	capVal, active, _ = s.BudgetStatus()
	if active || capVal != 0 {
		t.Fatalf("after lift, budget status = (%d, %v), want (0, false)", capVal, active)
	}
}

func TestSessionBudgetCapLeavesFMP4CopyAndVideoTranscodingOffAlone(t *testing.T) {
	fmp4CopyDec := core.Decision{
		Kind:        core.DecisionRemux,
		Container:   "fmp4",
		VideoCopy:   true,
		BitrateKbps: 14_000,
	}
	fmp4Copy := &Session{
		id:        "budget-fmp4-copy",
		proto:     core.ProtocolHLS,
		container: ContainerFMP4,
		decision:  fmp4CopyDec,
		rungs:     []rung{{id: "orig", avgKbps: 14_000, decision: fmp4CopyDec}},
		m:         &Manager{cfg: config.Transcode{}},
	}
	if _, _, ok := fmp4Copy.AdaptiveBudgetInfo(); ok {
		t.Fatalf("fMP4 video-copy session must not advertise dynamic video transcode stepping")
	}
	if applied, _ := fmp4Copy.SetBudgetCapKbps(4_000); applied {
		t.Fatalf("fMP4 video-copy session must not force transcode mid-stream")
	}

	tsDec := core.Decision{
		Kind:        core.DecisionTranscode,
		Container:   "mpegts",
		VideoCodec:  "h264",
		BitrateKbps: 8_000,
	}
	noTC := &Session{
		id:        "budget-no-tc",
		proto:     core.ProtocolHLS,
		container: ContainerMPEGTS,
		decision:  tsDec,
		rungs:     []rung{{id: "1080p", avgKbps: 8_000, decision: tsDec}},
		m:         &Manager{cfg: config.Transcode{VideoTranscoding: "off"}},
	}
	if _, _, ok := noTC.AdaptiveBudgetInfo(); ok {
		t.Fatalf("session with video transcoding disabled must not step dynamically")
	}
}
