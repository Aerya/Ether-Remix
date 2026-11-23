package uploadbudget

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestUploadBudgetExcludesPausedIdleAndAbandonedSessions(t *testing.T) {
	sessions := []*core.PlaySession{
		{ID: "playing", Liveness: core.SessionLivenessPlaying, Client: core.ClientCaps{Remote: true}, Decision: core.DecisionDirectPlay, Info: core.MediaInfo{BitrateKbps: 10000}},
		{ID: "paused", Liveness: core.SessionLivenessPaused, ViewerState: "paused", Client: core.ClientCaps{Remote: true}, Decision: core.DecisionDirectPlay, Info: core.MediaInfo{BitrateKbps: 10000}},
		{ID: "starting", Liveness: core.SessionLivenessStarting, Client: core.ClientCaps{Remote: true}, Decision: core.DecisionDirectPlay, Info: core.MediaInfo{BitrateKbps: 10000}},
		{ID: "idle", Liveness: core.SessionLivenessIdle, Client: core.ClientCaps{Remote: true}, Decision: core.DecisionDirectPlay, Info: core.MediaInfo{BitrateKbps: 10000}},
		{ID: "stopped", Liveness: core.SessionLivenessStopped, ViewerState: "stopped", Client: core.ClientCaps{Remote: true}, Decision: core.DecisionDirectPlay, Info: core.MediaInfo{BitrateKbps: 10000}},
	}
	m := NewManager(Options{
		ActiveSessions: func() []*core.PlaySession { return sessions },
	})
	got := m.snapshotActiveStreams()
	if len(got) != 1 || got[0].ID != "playing" {
		t.Fatalf("upload budget active streams = %+v, want only playing", got)
	}
}
