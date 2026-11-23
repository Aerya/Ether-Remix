package uploadbudget

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/sockerr"
)

func remoteTranscodeSession(id string, bitrateKbps int) *core.PlaySession {
	return &core.PlaySession{
		ID:       id,
		Client:   core.ClientCaps{Remote: true},
		Decision: core.DecisionTranscode,
		DeliveryDecision: &core.Decision{
			Kind:        core.DecisionTranscode,
			BitrateKbps: bitrateKbps,
		},
	}
}

func remoteDirectSession(id string, bitrateKbps int) *core.PlaySession {
	return &core.PlaySession{
		ID:       id,
		Client:   core.ClientCaps{Remote: true},
		Decision: core.DecisionDirectPlay,
		Info:     core.MediaInfo{BitrateKbps: bitrateKbps},
	}
}

func lanSession(id string, bitrateKbps int) *core.PlaySession {
	return &core.PlaySession{
		ID:       id,
		Client:   core.ClientCaps{Remote: false},
		Decision: core.DecisionDirectPlay,
		Info:     core.MediaInfo{BitrateKbps: bitrateKbps},
	}
}

func remoteLiveSession(id string, bitrateKbps int) *core.PlaySession {
	return &core.PlaySession{
		ID:          id,
		Client:      core.ClientCaps{Remote: true},
		Decision:    core.DecisionTranscode,
		LiveChannel: &core.LiveChannel{ID: "ch-1", Name: "News"},
		DeliveryDecision: &core.Decision{
			Kind:        core.DecisionTranscode,
			BitrateKbps: bitrateKbps,
		},
	}
}

func TestBudgetAllocationFairnessAndHeadroom(t *testing.T) {
	// 40 Mbps upload -> 85% usable = 34,000 kbps.
	usable := UsableBudgetKbps(40_000)
	if usable != 34_000 {
		t.Fatalf("usable budget = %d, want 34000", usable)
	}

	// Active streams:
	// - Remote direct-play (14 Mbps, fixed)
	// - Remote adaptive elastic stream (12 Mbps base)
	// - LAN 4K direct-play (60 Mbps, MUST be ignored)
	// Plus external sibling PMS WAN session (4 Mbps).
	// Remaining pool for elastic streams = 34,000 - 4,000 - 14,000 = 16,000 kbps.
	// Split fairly across 1 existing elastic + 1 new elastic = 8,000 kbps each.
	active := []ActiveStream{
		{ID: "direct", Remote: true, DirectPlay: true, BitrateKbps: 14_000},
		{ID: "elastic-1", Remote: true, Adaptive: true, BitrateKbps: 12_000, BaseBitrateKbps: 12_000},
		{ID: "lan-4k", Remote: false, DirectPlay: true, BitrateKbps: 60_000},
	}
	capKbps := AllocateForNewStream(usable, 4_000, active, "", 20_000)
	if capKbps != 8_000 {
		t.Fatalf("cap = %d kbps, want 8000 kbps (fair share of 16000 across 2 elastic streams)", capKbps)
	}

	// Surplus water-filling: if existing elastic stream only needs 4 Mbps,
	// the new stream gets the remaining 12 Mbps instead of being stuck at 8 Mbps.
	activeLow := []ActiveStream{
		{ID: "direct", Remote: true, DirectPlay: true, BitrateKbps: 14_000},
		{ID: "elastic-low", Remote: true, Adaptive: true, BitrateKbps: 4_000, BaseBitrateKbps: 4_000},
		{ID: "lan-4k", Remote: false, DirectPlay: true, BitrateKbps: 60_000},
	}
	capLow := AllocateForNewStream(usable, 4_000, activeLow, "", 20_000)
	if capLow != 12_000 {
		t.Fatalf("water-fill cap = %d kbps, want 12000 kbps", capLow)
	}
}

func TestLANExemptionAndDirectPlayNotForcedToTranscode(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	sessions := []*core.PlaySession{
		lanSession("lan-1", 80_000),
		lanSession("lan-2", 50_000),
	}
	var applied []string
	mgr := NewManager(Options{
		Now:            func() time.Time { return now },
		OverrideKbps:   func() int { return 20_000 }, // 17,000 kbps usable
		ActiveSessions: func() []*core.PlaySession { return sessions },
		AdaptiveInfo: func(id string) ([]int, int, bool) {
			return []int{20_000, 8_000, 4_000, 2_000}, 0, true
		},
		ApplySessionCap: func(id string, capKbps int) (bool, string) {
			applied = append(applied, id)
			return true, ""
		},
	})

	// LAN request is never capped even when override is tight.
	capKbps, fromBudget := mgr.RemoteCapForNewStream(context.Background(), "lan-new", false, true, 0, 50_000)
	if fromBudget || capKbps != 0 {
		t.Fatalf("LAN stream must be exempt: cap=%d fromBudget=%v", capKbps, fromBudget)
	}

	// Direct-play-only remote stream (allowTranscoding == false) must never be forced to transcode.
	capKbps, fromBudget = mgr.RemoteCapForNewStream(context.Background(), "wan-dp", true, false, 0, 25_000)
	if fromBudget || capKbps != 0 {
		t.Fatalf("direct-play-only remote stream must not be forced to transcode: cap=%d fromBudget=%v", capKbps, fromBudget)
	}

	// Remote stream starting alongside 130 Mbps of LAN traffic sees full 17,000 kbps budget.
	capKbps, fromBudget = mgr.RemoteCapForNewStream(context.Background(), "wan-1", true, true, 0, 25_000)
	if !fromBudget || capKbps != 17_000 {
		t.Fatalf("remote stream cap = (%d, %v), want (17000, true) ignoring LAN load", capKbps, fromBudget)
	}

	// Reconcile never touches LAN sessions.
	mgr.ReconcileOnce(context.Background())
	if len(applied) != 0 {
		t.Fatalf("LAN streams should never be adjusted, got %v", applied)
	}
}

func TestLiveTVAndDirectPlayCountingAndNoLimitUntilEvidence(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	override := 0
	sessions := []*core.PlaySession{
		remoteDirectSession("dp-1", 10_000),
		remoteLiveSession("live-1", 0), // falls back to DefaultLiveStreamKbps (4000)
	}
	mgr := NewManager(Options{
		Now:            func() time.Time { return now },
		OverrideKbps:   func() int { return override },
		ActiveSessions: func() []*core.PlaySession { return sessions },
	})

	// Before passive evidence exists and without override: assume no limit.
	capKbps, fromBudget := mgr.RemoteCapForNewStream(context.Background(), "wan-new", true, true, 0, 12_000)
	if fromBudget || capKbps != 0 {
		t.Fatalf("without override or measured evidence, expected no limit; got (%d, %v)", capKbps, fromBudget)
	}
	if st := mgr.Status(); st.Source != "learning" || st.EffectiveKbps != 0 {
		t.Fatalf("status before evidence = %+v, want learning/0", st)
	}

	// Feed real passive backpressured/starved delivery samples (~30 Mbps throughput).
	for i := 0; i < 6; i++ {
		now = now.Add(3 * time.Second)
		// 30 Mbps = 3,750,000 bytes/s -> 7.5 MB in 2s.
		mgr.ObserveDelivery("dp-1", Observation{
			Bytes:            7_500_000,
			Duration:         2 * time.Second,
			MediaBitrateKbps: 10_000,
			TCPBacklogBytes:  256 << 10,
			Starved:          i >= 2,
		})
	}

	measured, source, _, _, samples := mgr.EffectiveCapacity()
	if source != "measured" || measured <= 0 || samples < 4 {
		t.Fatalf("expected passive measured capacity, got (%d, %q, samples=%d)", measured, source, samples)
	}
	// Concurrent aggregate throughput on the uplink is 30,000 kbps (dp-1) +
	// 4,000 kbps (concurrent live-1) = 34,000 kbps; conservative 95% estimate
	// should stay strictly below raw 34,000 kbps (~32,300 kbps).
	if measured >= 34_000 || measured < 25_000 {
		t.Fatalf("measured capacity = %d kbps, expected conservative estimate in [25000, 34000)", measured)
	}

	// Status accounts for direct play (10,000) + Live TV fallback (4,000) = 14,000 kbps.
	st := mgr.Status()
	if st.EtherRemoteKbps != 10_000+DefaultLiveStreamKbps {
		t.Fatalf("ether remote usage = %d kbps, want %d (direct + live tv)", st.EtherRemoteKbps, 10_000+DefaultLiveStreamKbps)
	}

	// Owner override wins immediately over the measured estimate.
	override = 50_000
	eff, src, _, _, _ := mgr.EffectiveCapacity()
	if src != "override" || eff != 50_000 {
		t.Fatalf("override capacity = (%d, %q), want (50000, override)", eff, src)
	}
}

func TestHysteresisStepDownNoFlapAndStepUp(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	h := NewHysteresisController()
	rungs := []int{12_000, 8_000, 4_000, 2_000} // descending (base -> lowest)
	usable := UsableBudgetKbps(20_000)          // 17,000 kbps

	// Single viewer at 12 Mbps fits comfortably within 17 Mbps: no adjustment.
	s1 := ActiveStream{ID: "s1", Remote: true, Adaptive: true, BitrateKbps: 12_000, BaseBitrateKbps: 12_000, RungKbps: rungs}
	if adj := h.Reconcile(now, usable, 0, []ActiveStream{s1}); len(adj) != 0 {
		t.Fatalf("single stream fits, expected no adjustment, got %+v", adj)
	}

	// Second remote viewer joins (also at 12 Mbps -> 24 Mbps > 17 Mbps usable).
	// LAN stream alongside must NOT be adjusted.
	s2 := ActiveStream{ID: "s2", Remote: true, Adaptive: true, BitrateKbps: 12_000, BaseBitrateKbps: 12_000, RungKbps: rungs}
	lan := ActiveStream{ID: "lan-1", Remote: false, Adaptive: true, BitrateKbps: 40_000, BaseBitrateKbps: 40_000, RungKbps: rungs}
	now = now.Add(2 * time.Second)
	adj := h.Reconcile(now, usable, 0, []ActiveStream{s1, s2, lan})
	if len(adj) != 2 {
		t.Fatalf("expected both remote streams to step down when 2nd viewer joins (and LAN exempt), got %+v", adj)
	}
	for _, a := range adj {
		if a.SessionID == "lan-1" {
			t.Fatalf("LAN stream must never be adjusted: %+v", a)
		}
		if !a.StepDown || a.CapKbps != 8_500 {
			t.Fatalf("unexpected step-down decision: %+v (want StepDown=true CapKbps=8500)", a)
		}
	}
	// Both sessions step onto the 8,000 kbps rung under the 8,500 kbps cap.
	s1.BitrateKbps = 8_000
	s1.CurrentCapKbps = 8_500
	s2.BitrateKbps = 8_000
	s2.CurrentCapKbps = 8_500

	// Brief departure of s2 for 10s (less than StepUpCooldown 20s) MUST NOT step s1 up (anti-flap).
	now = now.Add(10 * time.Second)
	if adj := h.Reconcile(now, usable, 0, []ActiveStream{s1}); len(adj) != 0 {
		t.Fatalf("should not step up before cooldown, got %+v", adj)
	}

	// Brief blip where s2 returns resets the step-up headroom timer without flapping.
	now = now.Add(5 * time.Second)
	if adj := h.Reconcile(now, usable, 0, []ActiveStream{s1, s2}); len(adj) != 0 {
		t.Fatalf("re-balanced two streams should not flap, got %+v", adj)
	}

	// Now s2 leaves for good. First check starts the headroom clock; still no immediate jump.
	now = now.Add(2 * time.Second)
	if adj := h.Reconcile(now, usable, 0, []ActiveStream{s1}); len(adj) != 0 {
		t.Fatalf("first check after leave should start headroom timer without jumping, got %+v", adj)
	}

	// After sustained headroom >= StepUpCooldown (20s), s1 steps cleanly back up.
	now = now.Add(StepUpCooldown + time.Second)
	adj = h.Reconcile(now, usable, 0, []ActiveStream{s1})
	if len(adj) != 1 || adj[0].StepDown || adj[0].CapKbps != 0 {
		t.Fatalf("expected clean step-up releasing cap after sustained headroom, got %+v", adj)
	}
}

func TestPMSPollerOnlyPollsDuringRemoteStreamsAndCountsWANOnly(t *testing.T) {
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status/sessions" {
			http.NotFound(w, r)
			return
		}
		polls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"MediaContainer": map[string]any{
				"Metadata": []map[string]any{
					// Remote playing stream -> MUST count (8500 kbps)
					{
						"Session": map[string]any{"bandwidth": 8500, "location": "wan"},
						"Player":  map[string]any{"local": false, "state": "playing"},
					},
					// Remote stopped stream -> MUST ignore
					{
						"Session": map[string]any{"bandwidth": 6000, "location": "wan"},
						"Player":  map[string]any{"local": false, "state": "stopped"},
					},
					// LAN stream -> MUST ignore
					{
						"Session": map[string]any{"bandwidth": 40000, "location": "lan"},
						"Player":  map[string]any{"local": true, "state": "playing"},
					},
				},
			},
		})
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	poller := NewPMSPoller(PMSPollerOptions{
		HTTP: srv.Client(),
		Now:  func() time.Time { return now },
		LinkedPMS: func(context.Context) ([]ServerTarget, error) {
			return []ServerTarget{{
				MachineID: "pms-real",
				Name:      "Living Room PMS",
				BaseURLs:  []string{srv.URL},
				Token:     "tok",
				Source:    "linked_pms",
			}}, nil
		},
	})

	active := []*core.PlaySession{lanSession("lan-only", 30_000)}
	mgr := NewManager(Options{
		Now:            func() time.Time { return now },
		OverrideKbps:   func() int { return 40_000 },
		ActiveSessions: func() []*core.PlaySession { return active },
		Poller:         poller,
	})

	// With only LAN sessions on Ether, PMS MUST NOT be polled.
	mgr.ReconcileOnce(context.Background())
	if polls.Load() != 0 {
		t.Fatalf("expected 0 PMS polls when Ether has no remote streams, got %d", polls.Load())
	}

	// Once Ether has a remote stream, PMS is polled and counts only its WAN playing stream.
	active = append(active, remoteTranscodeSession("wan-1", 8_000))
	mgr.ReconcileOnce(context.Background())
	if polls.Load() != 1 {
		t.Fatalf("expected 1 PMS poll when Ether has a remote stream, got %d", polls.Load())
	}
	st := mgr.Status()
	if st.OtherPMSKbps != 8500 {
		t.Fatalf("external PMS WAN kbps = %d, want 8500", st.OtherPMSKbps)
	}
	if len(st.OtherServers) != 1 || st.OtherServers[0].RemoteStreams != 1 {
		t.Fatalf("external servers = %+v, want 1 server with 1 remote stream", st.OtherServers)
	}

	// When Ether's remote stream leaves, PMS stops polling and clears idle state.
	active = []*core.PlaySession{lanSession("lan-only", 30_000)}
	now = now.Add(30 * time.Second)
	mgr.ReconcileOnce(context.Background())
	if polls.Load() != 1 {
		t.Fatalf("expected PMS polling to stop when Ether has 0 remote streams, got %d polls", polls.Load())
	}
	if st := mgr.Status(); st.OtherPMSKbps != 0 {
		t.Fatalf("expected external kbps cleared when idle, got %d", st.OtherPMSKbps)
	}
}

// Regression test for #721 (reporter's exact test sketch): a client pacing a
// direct play near the media bitrate makes Write block with a full kernel send
// queue because the client stopped reading (TCP rwnd closed). That is client
// pacing, never server upload capacity, so the estimator must ignore it and the
// next play of the same file must still be allowed to direct play.
func TestClientPacedDirectPlayIsNotCapacity(t *testing.T) {
	now := time.Date(2026, 11, 22, 16, 42, 0, 0, time.UTC)
	e := NewEstimator(func() time.Time { return now })
	for i := 0; i < 360; i++ { // 12 min of 2 s meter windows
		e.Observe(Observation{
			At: now, Bytes: 5_400_000, Duration: 2 * time.Second, // ~21.6 Mbps, client-paced
			MediaBitrateKbps: 19168, TCPBacklogBytes: 4 << 20, // send queue full: client not reading
		})
		now = now.Add(2 * time.Second)
	}
	kbps, n := e.Estimate()
	if kbps != 0 || n != 0 {
		t.Fatalf("client-paced direct play must not be learned as capacity: got kbps=%d samples=%d (usable=%d)", kbps, n, UsableBudgetKbps(kbps))
	}

	// Receiver-window-limited (TCPStallRwnd) windows — even during a quiet VBR
	// scene below average media bitrate or while BufferFilling is still armed —
	// and application-limited (source/encoder) stalls must never count either.
	for i := 0; i < 10; i++ {
		now = now.Add(2 * time.Second)
		if e.Observe(Observation{
			At: now, Bytes: 2_500_000, Duration: 2 * time.Second, // 10 Mbps VBR quiet scene
			MediaBitrateKbps: 19168, TCPBacklogBytes: 4 << 20,
			TCPStall: sockerr.TCPStallRwnd, BufferFilling: true,
		}) {
			t.Fatalf("TCPStallRwnd window must never be recorded as capacity evidence")
		}
		if e.Observe(Observation{
			At: now, Bytes: 2_500_000, Duration: 2 * time.Second,
			MediaBitrateKbps: 19168, TCPBacklogBytes: 256 << 10,
			TCPStall: sockerr.TCPStallAppLimited, BufferFilling: true,
		}) {
			t.Fatalf("TCPStallAppLimited window must never be recorded as capacity evidence")
		}
	}
	if kbps, n := e.Estimate(); kbps != 0 || n != 0 {
		t.Fatalf("rwnd/app-limited windows must leave estimator in learning state: got (%d, %d)", kbps, n)
	}

	// End-to-end through Manager: after 12 minutes of client-paced direct play,
	// the next play of the same 19,168 kbps file must NOT be refused direct play.
	mgrNow := time.Date(2026, 11, 22, 16, 42, 0, 0, time.UTC)
	mgr := NewManager(Options{
		Now: func() time.Time { return mgrNow },
	})
	for i := 0; i < 360; i++ {
		mgr.ObserveDelivery("dp-4k", Observation{
			At: mgrNow, Bytes: 5_400_000, Duration: 2 * time.Second,
			MediaBitrateKbps: 19168, TCPBacklogBytes: 4 << 20,
		})
		mgrNow = mgrNow.Add(2 * time.Second)
	}
	if capKbps, fromBudget := mgr.RemoteCapForNewStream(context.Background(), "dp-4k-next", true, true, 0, 19168); fromBudget || capKbps != 0 {
		t.Fatalf("next play of 19168 kbps file after client-paced play must not be capped: got (%d, %v)", capKbps, fromBudget)
	}
}

func TestRealBottleneckLearnsCapacity(t *testing.T) {
	t.Run("buffer fill with TCP congestion learns real uplink and ignores later pacing", func(t *testing.T) {
		now := time.Date(2026, 11, 22, 17, 0, 0, 0, time.UTC)
		e := NewEstimator(func() time.Time { return now })

		// Real 35 Mbps uplink during initial buffer fill (6 x 2s = 12s, 8.75 MB/2s).
		for i := 0; i < 6; i++ {
			if !e.Observe(Observation{
				At: now, SessionID: "s1",
				Bytes: 8_750_000, Duration: 2 * time.Second, // 35 Mbps
				MediaBitrateKbps: 19168, TCPBacklogBytes: 512 << 10,
				TCPStall: sockerr.TCPStallCongestion, BufferFilling: true,
			}) {
				t.Fatalf("buffer-fill congestion window %d should be recorded", i)
			}
			now = now.Add(2 * time.Second)
		}

		// Subsequent 10 minutes of client-paced steady state at ~21.6 Mbps must
		// NOT overwrite or drag down the 35 Mbps estimate.
		for i := 0; i < 300; i++ {
			if e.Observe(Observation{
				At: now, SessionID: "s1",
				Bytes: 5_400_000, Duration: 2 * time.Second, // 21.6 Mbps client-paced
				MediaBitrateKbps: 19168, TCPBacklogBytes: 4 << 20,
				TCPStall: sockerr.TCPStallRwnd,
			}) {
				t.Fatalf("steady-state rwnd-limited window %d must be rejected", i)
			}
			now = now.Add(2 * time.Second)
		}

		kbps, n := e.Estimate()
		// 35,000 * 95% = 33,250 -> rounded 33,300 kbps; usable = 28,305 kbps (> 19,168).
		if n != 6 || kbps < 32_000 || kbps > 35_000 {
			t.Fatalf("estimate = (%d kbps, %d samples), want ~33300 kbps from 6 fill samples", kbps, n)
		}
		if usable := UsableBudgetKbps(kbps); usable < 19168 {
			t.Fatalf("usable budget %d must allow 19168 kbps direct play on 35 Mbps uplink", usable)
		}
	})

	t.Run("delivery clearly below consumption with send-queue saturation learns bottleneck", func(t *testing.T) {
		now := time.Date(2026, 11, 22, 17, 15, 0, 0, time.UTC)
		e := NewEstimator(func() time.Time { return now })

		// 14 Mbps uplink trying to direct-play a 19,168 kbps file (73% <= 85%).
		for i := 0; i < 6; i++ {
			if !e.Observe(Observation{
				At: now, SessionID: "s-slow",
				Bytes: 3_500_000, Duration: 2 * time.Second, // 14 Mbps
				MediaBitrateKbps: 19168, TCPBacklogBytes: 256 << 10,
			}) {
				t.Fatalf("below-consumption saturated window %d should be recorded", i)
			}
			now = now.Add(2 * time.Second)
		}
		kbps, n := e.Estimate()
		// 14,000 * 95% = 13,300 kbps.
		if n != 6 || kbps < 12_500 || kbps > 14_000 {
			t.Fatalf("estimate = (%d kbps, %d samples), want ~13300 kbps", kbps, n)
		}
	})

	t.Run("HLS segment slower than real time learns bottleneck while healthy segments do not", func(t *testing.T) {
		now := time.Date(2026, 11, 22, 17, 30, 0, 0, time.UTC)
		e := NewEstimator(func() time.Time { return now })

		// Healthy 4s segments transferring in 3s (1.33x real time) must NOT be
		// classified as starved (previously >= 0.7 * SegDuration falsely was).
		for i := 0; i < 6; i++ {
			if e.Observe(Observation{
				At: now, SessionID: "hls-ok",
				Bytes: 4_000_000, Duration: 3 * time.Second, SegDuration: 4 * time.Second,
				MediaBitrateKbps: 8000, TCPBacklogBytes: 64 << 10,
			}) {
				t.Fatalf("healthy 3s transfer of 4s segment must not count as bottleneck")
			}
			now = now.Add(4 * time.Second)
		}
		if kbps, n := e.Estimate(); kbps != 0 || n != 0 {
			t.Fatalf("healthy HLS segments should stay learning, got (%d, %d)", kbps, n)
		}

		// After hls-ok ends, a later stream hitting a real bottleneck (4s
		// segments taking 5s = 1.25x media duration -> 6.4 Mbps) learns it.
		now = now.Add(2 * time.Minute)
		for i := 0; i < 4; i++ {
			if !e.Observe(Observation{
				At: now, SessionID: "hls-slow",
				Bytes: 4_000_000, Duration: 5 * time.Second, SegDuration: 4 * time.Second,
				MediaBitrateKbps: 8000,
			}) {
				t.Fatalf("slower-than-realtime segment %d should be recorded", i)
			}
			now = now.Add(5 * time.Second)
		}
		kbps, n := e.Estimate()
		if n != 4 || kbps < 5_800 || kbps > 6_500 {
			t.Fatalf("slow HLS estimate = (%d kbps, %d samples), want ~6100 kbps", kbps, n)
		}
	})
}

func TestSmoothPlaybackFloorNeverLowersBudgetBelowSmoothStream(t *testing.T) {
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	e := NewEstimator(func() time.Time { return now })

	// 1. An earlier viewer on a slow mobile connection teaches a 14,300 kbps
	//    estimate (usable = 12,155 kbps).
	for i := 0; i < 5; i++ {
		e.Observe(Observation{
			At: now, SessionID: "mobile",
			Bytes: 3_750_000, Duration: 2 * time.Second, // 15 Mbps
			MediaBitrateKbps: 19168, TCPBacklogBytes: 256 << 10,
			Starved: true,
		})
		now = now.Add(2 * time.Second)
	}
	lowEst, _ := e.Estimate()
	if UsableBudgetKbps(lowEst) >= 19168 {
		t.Fatalf("setup expected low estimate below 19168 usable, got %d (usable=%d)", lowEst, UsableBudgetKbps(lowEst))
	}

	// 2. Now a remote viewer plays a 19,168 kbps direct play smoothly for 20s
	//    (client-paced, no buffering). The learned budget must not stay below
	//    19,168 kbps usable.
	for i := 0; i < 10; i++ {
		e.Observe(Observation{
			At: now, SessionID: "smooth-tv",
			Bytes: 5_400_000, Duration: 2 * time.Second, // ~21.6 Mbps client-paced
			MediaBitrateKbps: 19168, TCPBacklogBytes: 4 << 20,
			TCPStall: sockerr.TCPStallRwnd,
		})
		now = now.Add(2 * time.Second)
	}
	floored, _ := e.Estimate()
	if usable := UsableBudgetKbps(floored); usable < 19168 {
		t.Fatalf("smooth playback floor failed: est=%d usable=%d < 19168", floored, usable)
	}

	// 3. Multi-stream starvation at a lower aggregate rate (genuine ISP
	//    slowdown) still retires the stale smooth floor so the budget adapts.
	now = now.Add(12 * time.Minute)
	for i := 0; i < 4; i++ {
		e.Observe(Observation{
			At: now, SessionID: "isp-slow",
			Bytes: 2_500_000, Duration: 2 * time.Second, // 10 Mbps
			AggregateKbps: 12_000, MediaBitrateKbps: 19168,
			TCPBacklogBytes: 256 << 10, Starved: true, ConcurrentStreams: 2,
		})
		now = now.Add(2 * time.Second)
	}
	adapted, _ := e.Estimate()
	if adapted > 14_000 {
		t.Fatalf("multi-stream ISP slowdown should retire stale smooth floor, got %d kbps", adapted)
	}

	// 4. Manager also protects a currently playing smooth PlaySession (via live
	//    telemetry even without prior smooth delivery samples), while an
	//    explicit manual override still always wins.
	override := 0
	live := []*core.PlaySession{{
		ID:       "live-smooth",
		Liveness: core.SessionLivenessPlaying,
		Client:   core.ClientCaps{Remote: true},
		Decision: core.DecisionDirectPlay,
		Info:     core.MediaInfo{BitrateKbps: 19168},
		Experience: &core.PlayExperience{
			Requested:  true,
			Pictured:   true,
			PicturedAt: now.Add(-30 * time.Second),
		},
	}}
	mgr := NewManager(Options{
		Now:            func() time.Time { return now },
		OverrideKbps:   func() int { return override },
		ActiveSessions: func() []*core.PlaySession { return live },
	})
	for i := 0; i < 4; i++ {
		mgr.ObserveDelivery("mobile-2", Observation{
			At: now, Bytes: 3_000_000, Duration: 2 * time.Second, // 12 Mbps
			MediaBitrateKbps: 19168, TCPBacklogBytes: 256 << 10, Starved: true,
		})
		now = now.Add(2 * time.Second)
	}
	eff, src, _, _, _ := mgr.EffectiveCapacity()
	if src != "measured" || UsableBudgetKbps(eff) < 19168 {
		t.Fatalf("manager must never lower learned budget below active smooth stream: src=%q eff=%d usable=%d", src, eff, UsableBudgetKbps(eff))
	}

	override = 10_000
	eff, src, _, _, _ = mgr.EffectiveCapacity()
	if src != "override" || eff != 10_000 {
		t.Fatalf("manual override must still win over smooth floor: src=%q eff=%d", src, eff)
	}
}

func TestNoteBufferEventsOnlyFiresOnIncrease(t *testing.T) {
	mgr := NewManager(Options{})
	if mgr.NoteBufferEvents("s1", 0) {
		t.Fatalf("zero buffer events must not report starvation")
	}
	if !mgr.NoteBufferEvents("s1", 1) {
		t.Fatalf("first buffer event (0->1) must report starvation")
	}
	if mgr.NoteBufferEvents("s1", 1) {
		t.Fatalf("unchanged buffer event count (1->1) must not re-report starvation")
	}
	if !mgr.NoteBufferEvents("s1", 2) {
		t.Fatalf("new buffer event (1->2) must report starvation")
	}
}
