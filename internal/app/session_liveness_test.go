package app

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestOverviewActivityAndRestartGateShareLivenessRule(t *testing.T) {
	p := &activityPlayback{entries: map[string]*core.PlaySession{
		"playing":  {ID: "playing", Liveness: core.SessionLivenessPlaying},
		"paused":   {ID: "paused", Liveness: core.SessionLivenessPaused, ViewerState: "paused"},
		"starting": {ID: "starting", Liveness: core.SessionLivenessStarting},
		"idle":     {ID: "idle", Liveness: core.SessionLivenessIdle},
		"stopped":  {ID: "stopped", Liveness: core.SessionLivenessStopped, ViewerState: "stopped"},
		"prepared": {ID: "prepared", PreparedOnly: true, Liveness: core.SessionLivenessStarting},
	}}
	a := &App{brokerPlayback: p}
	owner := ownerPlayback{Playback: p, app: a}

	// Overview & Activity (/ether/api/status/sessions) read ownerPlayback.Sessions():
	// only genuinely playing and explicitly paused sessions are visible.
	visible := owner.Sessions()
	if len(visible) != 2 {
		t.Fatalf("visible sessions = %d (%+v), want 2 (playing + paused)", len(visible), visible)
	}
	seen := map[string]core.SessionLivenessState{}
	for _, s := range visible {
		seen[s.ID] = s.Liveness
	}
	if seen["playing"] != core.SessionLivenessPlaying || seen["paused"] != core.SessionLivenessPaused {
		t.Fatalf("unexpected visible set: %+v", seen)
	}

	// Restart/update gate counts ONLY actively playing streams (excludes paused, starting, idle, stopped, prepared).
	if got := a.activeSessionCount(); got != 1 {
		t.Fatalf("activeSessionCount = %d, want 1", got)
	}
	st := a.restartStatus()
	if st.Active != 1 {
		t.Fatalf("restartStatus.Active = %d, want 1", st.Active)
	}
	activeList, ok := st.Sessions.([]*core.PlaySession)
	if !ok || len(activeList) != 1 || activeList[0].ID != "playing" {
		t.Fatalf("restartStatus.Sessions = %+v, want only playing", st.Sessions)
	}

	// When the only playing stream pauses, restart gate immediately drops to 0 while Activity still shows it Paused.
	p.entries["playing"].Liveness = core.SessionLivenessPaused
	p.entries["playing"].ViewerState = "paused"
	if got := a.activeSessionCount(); got != 0 {
		t.Fatalf("activeSessionCount with only paused streams = %d, want 0", got)
	}
	if got := len(owner.Sessions()); got != 2 {
		t.Fatalf("paused streams should remain visible in Activity/Overview, got %d", got)
	}
}
