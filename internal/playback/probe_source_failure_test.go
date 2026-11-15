package playback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestProbeReadFailureTriesAnotherSourceInsteadOfEstimatedPlayback(t *testing.T) {
	bad := mkv("unreadable", 10000, time.Minute)
	bad.setFail(0, bad.Size, errors.New("source reader has closed"))
	h := newHarness(t, []*fakeRelease{bad, mkv("usable", 10000, time.Minute)}, []string{"unreadable", "usable"})
	ps := h.prepare(owner, movieKey, "probe-read-failure")
	if ps.Resolution.Release.ID != "usable" || ps.Info.Estimated {
		t.Fatalf("unreadable source became estimated playback: %+v", ps.Resolution.Release)
	}
	if h.resolver.bad["unreadable"] != "" {
		t.Fatal("unclassified read error permanently blacklisted the release")
	}
	// A readable source with incomplete facts must also yield to the next
	// candidate on surfaces that publish verified track identities.
	verified := newHarness(t, []*fakeRelease{mkv("incomplete", 16989, 0), mkv("verified", 10000, time.Minute)}, []string{"incomplete", "verified"}, withNativeProbe(func(_ context.Context, src core.MediaSource) (*core.MediaInfo, error) {
		if src.ReleaseID() == "incomplete" {
			return &core.MediaInfo{Estimated: true, Size: src.Size()}, nil
		}
		return nativeInfo(src), nil
	}, nil))
	caps := defaultCaps("verified-tracks")
	caps.Protocol = "vela"
	real, err := verified.m.PrepareSurface(context.Background(), owner, movieKey, caps, "", false)
	if err != nil || real.Info.Estimated || real.Resolution.Release.ID != "verified" {
		t.Fatalf("verified candidate: %+v %v", real, err)
	}
	if verified.resolver.bad["incomplete"] != "" {
		t.Fatal("incomplete facts permanently blacklisted a source")
	}
}
