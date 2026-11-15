package resolver

import (
	"context"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

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
