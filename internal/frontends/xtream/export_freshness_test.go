package xtream

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/listcache"
)

// TestExportListRequestStampTracksLibrariesAndKindSync verifies that Xtream's
// listing cache stamp incorporates PageFreshness.Libraries and KindSync so
// plugin library updates invalidate cached exports even when Catalog is unchanged.
func TestExportListRequestStampTracksLibrariesAndKindSync(t *testing.T) {
	lib := &policyLibrary{
		syntheticExport: syntheticExport{size: 1},
		cap:             1,
	}
	var librariesGen, kindSyncGen atomic.Uint64
	inst := &instance{
		host: frontend.Host{
			Experience: lib,
			PageFreshness: func() frontend.PageFreshness {
				return frontend.PageFreshness{
					Catalog:   1,
					Libraries: librariesGen.Load(),
					KindSync:  kindSyncGen.Load(),
				}
			},
		},
		exports: listcache.New(t.TempDir()),
	}
	req := httptest.NewRequest("GET", "/player_api.php?action=get_vod_streams", nil).WithContext(context.Background())
	l := login{viewer: &frontend.Viewer{ID: 1}, base: "http://example.test", hasBase: true}

	r0, ok := inst.listRequest(req, l, "movie")
	if !ok {
		t.Fatal("listRequest returned !ok")
	}
	kindSyncGen.Add(1)
	r1, ok := inst.listRequest(req, l, "movie")
	if !ok {
		t.Fatal("listRequest returned !ok after KindSync")
	}
	if r1.State.Stamp == r0.State.Stamp {
		t.Fatalf("expected stamp to change on KindSync bump, both %q", r0.State.Stamp)
	}
	librariesGen.Add(1)
	r2, ok := inst.listRequest(req, l, "movie")
	if !ok {
		t.Fatal("listRequest returned !ok after Libraries")
	}
	if r2.State.Stamp == r1.State.Stamp {
		t.Fatalf("expected stamp to change on Libraries bump, both %q", r1.State.Stamp)
	}
}
