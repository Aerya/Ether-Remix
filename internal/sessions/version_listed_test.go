package sessions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

// A list row names a configured slot without a write; playing that id must
// resolve to the same release detail would have issued, and only an offered
// release may resolve.
func TestListedVersionHandleResolvesAtPlayLikeIssued(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	bus := NewEventBus(nil, active)
	defer bus.Close()
	b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy, Events: bus, Playback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 1}, Progress: &fakeProgress{}, ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k}, nil
	}, Variants: func(context.Context, *core.Viewer, *core.Title, core.ClientCaps) ([]core.ReleaseVariant, error) {
		return []core.ReleaseVariant{{ID: "vs:auto", Auto: true}, {ID: "vs:alt"}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)
	service, err := NewVersionHandles(db, b, "jellyfin", testVersionKey)
	if err != nil {
		t.Fatal(err)
	}
	listed := service.(frontend.ListedVersionHandles)
	scope := frontend.VersionScope{Viewer: &core.Viewer{ID: 1}, Device: core.Device{ID: "tv"}, Protocol: "jellyfin"}
	item := core.MovieKey(7)
	id := listed.ListedHandle(scope, item, "vs:alt")
	var count int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions_version_ids`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("listing wrote %d rows: %v", count, err)
	}
	if _, err = service.Resolve(ctx, scope, id); err == nil {
		t.Fatal("an unrecorded listed id resolved without its parent")
	}
	target, err := listed.ResolveListed(ctx, scope, item, id)
	if err != nil || target.Item != item || target.Release != "vs:alt" {
		t.Fatalf("listed resolve: %+v %v", target, err)
	}
	if issued, err := service.Issue(ctx, scope, item, "vs:alt"); err != nil || issued != id {
		t.Fatalf("issued %q, listed %q: %v", issued, id, err)
	}
	if target, err = service.Resolve(ctx, scope, id); err != nil || target.Release != "vs:alt" {
		t.Fatalf("recorded listed id: %+v %v", target, err)
	}
	if _, err = listed.ResolveListed(ctx, scope, core.MovieKey(8), id); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("listed id resolved for another item: %v", err)
	}
	if _, err = listed.ResolveListed(ctx, scope, item, listed.ListedHandle(scope, item, "vs:gone")); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unoffered listed id: %v", err)
	}
}
