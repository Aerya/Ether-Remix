package plex

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// deadlineProbePlayback records the deadline ProbeMetadata is called with.
type deadlineProbePlayback struct {
	core.Playback
	deadline    time.Time
	hasDeadline bool
	calls       int
}

func (p *deadlineProbePlayback) ProbeMetadata(ctx context.Context, _ *core.User, _ core.RatingKey, _ core.ClientCaps) error {
	p.calls++
	p.deadline, p.hasDeadline = ctx.Deadline()
	return nil
}
func (p *deadlineProbePlayback) WarmProbeInfo(*core.User, core.ClientCaps, core.RatingKey) (core.MediaInfo, bool) {
	return core.MediaInfo{}, false
}
func (p *deadlineProbePlayback) Estimate(context.Context, *core.Title, core.ClientCaps) core.MediaInfo {
	return core.MediaInfo{Estimated: true}
}

func TestProbeDetailTracksKeepsVersionReserve(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	pb := &deadlineProbePlayback{Playback: f.playback}
	f.srv.setPlayback(pb)
	user := ownerUser
	title := &fightClub

	t.Run("allowance", func(t *testing.T) {
		pr := &Request{User: &user, ClientID: "android-reserve"}
		start := time.Now()
		pr.detailWaitUntil = start.Add(4 * time.Second)
		if err := f.srv.probeDetailTracks(context.Background(), pr, title); err != nil {
			t.Fatal(err)
		}
		if !pb.hasDeadline {
			t.Fatal("probe ran without the detail allowance deadline")
		}
		// The probe may use the allowance except the reserve.
		if want := pr.detailWaitUntil.Add(-detailVariantReserve); !pb.deadline.Equal(want) {
			t.Fatalf("probe deadline %s, want allowance minus reserve %s", pb.deadline, want)
		}
		// Version discovery runs after the probe, inside the same 4 s
		// bound (detailVariantContext ends no later than detailWaitUntil):
		// the probe deadline leaves it at least the reserve.
		const minReserve = 500 * time.Millisecond
		if left := pr.detailWaitUntil.Sub(pb.deadline); left < minReserve || left < detailVariantReserve {
			t.Fatalf("version discovery keeps %s after the probe deadline, want >= %s", left, minReserve)
		}
		vctx, cancel := detailVariantContext(context.Background(), pr)
		defer cancel()
		if vdl, ok := vctx.Deadline(); !ok || vdl.After(pr.detailWaitUntil) {
			t.Fatalf("version discovery %s exceeds the 4 s allowance %s", vdl, pr.detailWaitUntil)
		}
		if total := pb.deadline.Sub(start); total < 3*time.Second {
			t.Fatalf("probe gets only %s of the 4 s allowance", total)
		}
	})

	t.Run("no-allowance-passes-caller-ctx", func(t *testing.T) {
		pr := &Request{User: &user, ClientID: "desktop-reserve"}
		if err := f.srv.probeDetailTracks(context.Background(), pr, title); err != nil {
			t.Fatal(err)
		}
		if pb.hasDeadline {
			t.Fatalf("probe got a deadline %s without a detail allowance", pb.deadline)
		}
	})
}
