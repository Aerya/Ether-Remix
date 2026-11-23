package transcode

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestAheadLimit(t *testing.T) {
	m := &Manager{opts: options{maxAhead: 2 * time.Minute}}
	video := core.MediaInfo{Streams: []core.StreamInfo{{Type: "video", Codec: "h264"}}}
	encode := core.Decision{Kind: core.DecisionTranscode, VideoCopy: false}
	copyDec := core.Decision{Kind: core.DecisionRemux, VideoCopy: true}

	makeSession := func(live, speculative bool, dec core.Decision, segDur time.Duration) *Session {
		return &Session{
			m:           m,
			segDur:      segDur,
			speculative: speculative,
			sessionInput: sessionInput{
				liveInput: live,
				info:      video,
				decision:  dec,
			},
		}
	}

	// Default (0): built-in distances apply.
	if got := makeSession(false, false, encode, 4*time.Second).aheadLimit(); got != 4 {
		t.Fatalf("default encode aheadLimit = %d, want 4", got)
	}
	if got := makeSession(false, false, copyDec, 4*time.Second).aheadLimit(); got != 30 {
		t.Fatalf("default copy aheadLimit = %d, want 30", got)
	}
	if got := makeSession(true, false, encode, 4*time.Second).aheadLimit(); got != 30 {
		t.Fatalf("default live encode aheadLimit = %d, want 30", got)
	}
	if got := makeSession(true, false, copyDec, 4*time.Second).aheadLimit(); got != 30 {
		t.Fatalf("default live copy aheadLimit = %d, want 30", got)
	}
	if got := makeSession(false, true, encode, 4*time.Second).aheadLimit(); got != 2 {
		t.Fatalf("default speculative aheadLimit = %d, want 2", got)
	}

	// Owner-set throttle distance applies to non-live encodes and copies alike;
	// live-input and speculative sessions keep their own limits.
	m.UpdateConfig(config.Transcode{ThrottleSeconds: 60})
	if got := makeSession(false, false, encode, 4*time.Second).aheadLimit(); got != 15 {
		t.Fatalf("60s encode aheadLimit = %d, want 15", got)
	}
	if got := makeSession(false, false, copyDec, 4*time.Second).aheadLimit(); got != 15 {
		t.Fatalf("60s copy aheadLimit = %d, want 15", got)
	}
	if got := makeSession(true, false, encode, 4*time.Second).aheadLimit(); got != 30 {
		t.Fatalf("60s live encode aheadLimit = %d, want 30", got)
	}
	if got := makeSession(true, false, copyDec, 4*time.Second).aheadLimit(); got != 30 {
		t.Fatalf("60s live copy aheadLimit = %d, want 30", got)
	}
	if got := makeSession(false, true, copyDec, 4*time.Second).aheadLimit(); got != 2 {
		t.Fatalf("60s speculative aheadLimit = %d, want 2", got)
	}

	// Built-in floor of 4 segments holds even when seconds/segDur < 4.
	m.UpdateConfig(config.Transcode{ThrottleSeconds: 16})
	if got := makeSession(false, false, encode, 4*time.Second).aheadLimit(); got != 4 {
		t.Fatalf("16s/4s aheadLimit = %d, want 4", got)
	}
	if got := makeSession(false, false, copyDec, 5*time.Second).aheadLimit(); got != 4 {
		t.Fatalf("16s/5s floor aheadLimit = %d, want 4", got)
	}
	m.UpdateConfig(config.Transcode{ThrottleSeconds: 8})
	if got := makeSession(false, false, encode, 4*time.Second).aheadLimit(); got != 4 {
		t.Fatalf("8s/4s floor aheadLimit = %d, want 4", got)
	}

	// Resetting to 0 restores built-in distances live.
	m.UpdateConfig(config.Transcode{ThrottleSeconds: 0})
	if got := makeSession(false, false, encode, 4*time.Second).aheadLimit(); got != 4 {
		t.Fatalf("reset encode aheadLimit = %d, want 4", got)
	}
	if got := makeSession(false, false, copyDec, 4*time.Second).aheadLimit(); got != 30 {
		t.Fatalf("reset copy aheadLimit = %d, want 30", got)
	}
}
