package plugins

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestManifestRefreshPersistedAndPreserved(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	p, err := e.m.PreviewInstall(ctx, Source{Kind: "url", URL: "https://addon.example/private/manifest.json", Manifest: json.RawMessage(`{"id":"refresh.test","name":"Refresh","version":"1.0.0","resources":["catalog"],"types":["movie"],"catalogs":[{"type":"movie","id":"top"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	r, err := e.m.Install(ctx, p, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if manifestRefreshInterval(r) != time.Hour {
		t.Fatal("default changed")
	}
	for _, hours := range []int{0, -1, 169} {
		if e.m.SetManifestRefresh(ctx, r.ID, hours) == nil {
			t.Fatal("invalid interval accepted", hours)
		}
	}
	if err := e.m.SetManifestRefresh(ctx, r.ID, 6); err != nil {
		t.Fatal(err)
	}
	r, err = e.m.Get(ctx, r.ID)
	if err != nil || manifestRefreshInterval(r) != 6*time.Hour {
		t.Fatalf("interval not persisted: %+v %v", r.ManifestRefreshHours, err)
	}
	// A manifest update must not reset the owner's schedule.
	if err := e.m.store.upsert(ctx, r); err != nil {
		t.Fatal(err)
	}
	r, _ = e.m.Get(ctx, r.ID)
	if manifestRefreshInterval(r) != 6*time.Hour {
		t.Fatal("schedule reset by manifest update")
	}
	last := map[string]time.Time{"stremio-manifest|" + r.ID: e.m.now().Add(-2 * time.Hour)}
	before := last["stremio-manifest|"+r.ID]
	e.m.runOnce(ctx, last)
	if !last["stremio-manifest|"+r.ID].Equal(before) {
		t.Fatal("manifest refreshed before selected interval")
	}
}
