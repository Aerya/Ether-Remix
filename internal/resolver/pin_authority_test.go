package resolver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// An owner pin tolerates inconclusive sampling, but never confirmed damage,
// missing sets, an unfetched NZB or cancellation of the viewer's request.
func TestPinHealthTimeoutKeepsOwnerSource(t *testing.T) {
	missing := fmt.Errorf("incomplete RAR set: %w", core.ErrArticleMissing)
	for _, tc := range []struct {
		name   string
		health error
		fetch  error
		cancel bool
		plays  bool
	}{
		{name: "sampling deadline", health: context.DeadlineExceeded, plays: true},
		{name: "missing set", health: missing},
		{name: "missing outweighs deadline", health: errors.Join(missing, context.DeadlineExceeded)},
		{name: "rejected archive", health: fmt.Errorf("incomplete RAR set: %w", core.ErrReleaseRejected)},
		{name: "NZB fetch deadline", fetch: context.DeadlineExceeded},
		{name: "cancelled play", health: context.DeadlineExceeded, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pinned := movieReleases()[0]
			idx := fetchFailIndexer{fakeIndexer: &fakeIndexer{releases: movieReleases()}, fail: map[string]error{pinned.ID: tc.fetch}}
			svc := New(idx, timeoutHealth(map[string]error{pinned.ID: tc.health}), nil, testCfg(), Options{})
			defer svc.Close()
			req := core.ResolveRequest{Title: matrixTitle(), Client: webCaps()}
			if _, err := svc.Pin(ctx, req.Title.Key, "", pinned, ""); err != nil {
				t.Fatal(err)
			}
			if tc.cancel {
				cancel()
			}
			res, err := svc.Resolve(ctx, req)
			if tc.plays {
				if err != nil || res == nil {
					t.Fatalf("timed-out pin did not play: %+v %v", res, err)
				}
				assertUnverifiedPlay(t, svc, res, pinned.ID)
				if res.HealthVerified || len(res.Alternates) != 0 {
					t.Fatalf("unverified pin gained verification or alternates: %+v", res)
				}
				p, _ := svc.PinFor(req.Title.Key)
				if p.LastStatus != PinHealthy || p.LastReason != unverifiedReason {
					t.Fatalf("pin outcome: %+v", p)
				}
			} else {
				if res != nil || err == nil || !strings.Contains(err.Error(), "pinned release "+pinned.ID+" cannot be played") {
					t.Fatalf("failed pin lost its clear failure: %+v %v", res, err)
				}
				if tc.health != nil && !tc.cancel && !strings.Contains(err.Error(), tc.health.Error()) {
					t.Fatalf("failure lost its reason: %v", err)
				}
			}
			if idx.searches.Load() != 0 {
				t.Fatal("pin searched for substitutes")
			}
		})
	}
}

// A pin is a source constraint across cached discovery, Auto and failover;
// changing it must not reuse a previous automatic choice or check alternatives.
func TestPinAuthoritativeSource(t *testing.T) {
	ctx := context.Background()
	idx := &fakeIndexer{releases: movieReleases()}
	svc := New(idx, fakeHealth{}, nil, testCfg(), Options{})
	defer svc.Close()
	req := core.ResolveRequest{Title: matrixTitle(), Client: webCaps()}
	if _, err := svc.Resolve(ctx, req); err != nil {
		t.Fatal(err)
	}
	searches := idx.searches.Load()
	// A DV-only HEVC source on an H.264-only client must still reach delivery
	// planning. Resolution does not require a compatible direct-play path.
	pinned, _ := svc.LookupRelease("uhd")
	pinned.Info.VideoCodec = "hevc"
	pinned.Info.DolbyVision, pinned.Info.HDR10 = true, false
	if _, err := svc.Pin(ctx, req.Title.Key, "", pinned, ""); err != nil {
		t.Fatal(err)
	}
	for _, prefer := range []string{"", (versionSlot{Kind: "auto"}).id()} {
		req.Prefer = prefer
		got, err := svc.Resolve(ctx, req)
		if err != nil || got == nil || got.Release.ID != pinned.ID || len(got.Alternates) != 0 {
			t.Fatalf("pin with selector %q: %+v %v", prefer, got, err)
		}
		if prefer != "" && got.VersionSelector != prefer {
			t.Fatalf("lost Auto selector: %+v", got)
		}
	}
	req.Exclude = []string{pinned.ID}
	got, err := svc.Resolve(ctx, req)
	if got != nil || err == nil || !strings.Contains(err.Error(), "excluded (already tried)") {
		t.Fatalf("pin failed over: %+v %v", got, err)
	}
	if idx.searches.Load() != searches {
		t.Fatal("pin searched for replacement releases")
	}
	if _, err := svc.Unpin(ctx, req.Title.Key); err != nil {
		t.Fatal(err)
	}
	req.Exclude, req.Prefer = nil, ""
	if got, err = svc.Resolve(ctx, req); err != nil || got.Release.ID != "hd" {
		t.Fatalf("unpin: %+v %v", got, err)
	}
}
