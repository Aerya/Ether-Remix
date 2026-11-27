package adapters_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// A list syncs exactly the size its plugin declares, the size the install
// review shows and the owner grants; lowering the grant in the plugin's
// limits caps the sync (W367: catalogue sources silently stopped at 250).
func TestListSyncGathersTheGrantedSize(t *testing.T) {
	h := newListTestHarness(t)
	ctx := context.Background()
	man, _ := json.Marshal(map[string]any{
		"id": "org.example.dubs", "name": "Dubs", "version": "1.0.0",
		"types": []string{"movie"}, "resources": []string{"catalog"},
		"catalogs": []map[string]any{{"type": "movie", "id": "dubbed", "name": "Dubbed", "extra": []map[string]any{{"name": "skip"}}}},
		"ether": map[string]any{
			"protocol": 1, "kind": "extension", "summary": "Dubbed lists",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"list_source"},
			"contributes":  map[string]any{"listSources": []map[string]any{{"key": "dubbed", "label": "Dubbed", "catalog": "movie/dubbed", "maxItems": 300}}},
		},
	})
	preview, err := h.mgr.PreviewInstall(ctx, plugins.Source{Kind: "studio", Manifest: man, Files: map[string][]byte{"dist/plugin.js": []byte("//")}})
	if err != nil {
		t.Fatal(err)
	}
	if preview.ListItems == nil || preview.ListItems.Requested != 300 || preview.ListItems.Granted != 300 {
		t.Fatalf("install review list size = %+v, want 300 asked and granted", preview.ListItems)
	}
	if !slices.Contains(preview.PermissionLines, "Syncs up to 300 titles per list") {
		t.Fatalf("install review lines %q do not show the list size", preview.PermissionLines)
	}
	rec, err := h.mgr.Install(ctx, preview, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	// An endless list: 100 new titles on every page.
	h.rt.routes["/catalog/movie/dubbed"] = func(req plugins.Request) (plugins.Response, error) {
		skip := 0
		if _, rest, ok := strings.Cut(req.Path, "skip="); ok {
			skip, _ = strconv.Atoi(strings.TrimSuffix(rest, ".json"))
		}
		metas := make([]map[string]string, 100)
		for i := range metas {
			metas[i] = map[string]string{"id": fmt.Sprintf("tt%07d", 9000000+skip+i), "type": "movie", "name": "Title"}
		}
		body, _ := json.Marshal(map[string]any{"metas": metas})
		return plugins.Response{Status: 200, Body: body}, nil
	}
	kind := extend.PluginID(rec.ID, "dubbed")
	res, err := h.cols.GatherCatalogueSource(ctx, kind, "Dubbed (Dubs)", string(core.TypeMovie))
	if err != nil {
		t.Fatal(err)
	}
	if res.Listed != 300 || res.Limit != 300 || !res.Capped() {
		t.Fatalf("declared size: listed %d of limit %d, want 300 of 300", res.Listed, res.Limit)
	}

	h.runtime.RuntimeOverrides = map[string]config.PluginRuntimeLimits{rec.ID: {ListItems: 120}}
	if res, err = h.cols.GatherCatalogueSource(ctx, kind, "Dubbed (Dubs)", string(core.TypeMovie)); err != nil {
		t.Fatal(err)
	}
	if res.Listed != 120 || res.Limit != 120 {
		t.Fatalf("lowered grant: listed %d of limit %d, want 120 of 120", res.Listed, res.Limit)
	}
}
