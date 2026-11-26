package adapters_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// Every catalogue source a plugin offers must sync through a list-source
// kind the plugin registered on install, whether the plugin is a plain
// Stremio add-on (lists keyed by catalog) or a native plugin whose own
// listSources read its declared catalogs under their own keys (W366: the
// English-dubs plugin's sources all failed with "catalogue source
// unavailable").
func TestEveryCatalogueListResolvesToARegisteredListSource(t *testing.T) {
	h := newListTestHarness(t)
	ctx := context.Background()

	man, _ := json.Marshal(map[string]any{
		"id": "org.example.dubs", "name": "Dubs", "version": "1.0.0",
		"types": []string{"series", "movie"}, "resources": []string{"catalog"},
		"catalogs": []map[string]any{
			{"type": "series", "id": "dubbed", "name": "Dubbed", "extra": []map[string]any{{"name": "skip"}}},
			{"type": "movie", "id": "dubbed", "name": "Dubbed", "extra": []map[string]any{{"name": "skip"}}},
		},
		"ether": map[string]any{
			"protocol": 1, "kind": "extension", "summary": "Dubbed lists",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"list_source"},
			"contributes": map[string]any{"listSources": []map[string]any{
				{"key": "dubbed", "label": "Dubbed", "catalog": "dubbed"},
				{"key": "picked", "label": "Picked", "catalog": "movie/picked", "params": []map[string]any{{"key": "genre", "kind": "text", "label": "Genre", "required": true}}},
			}},
		},
	})
	preview, err := h.mgr.PreviewInstall(ctx, plugins.Source{Kind: "studio", Manifest: man, Files: map[string][]byte{"dist/plugin.js": []byte("//")}})
	if err != nil {
		t.Fatal(err)
	}
	native, err := h.mgr.Install(ctx, preview, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	addon := installCatalogAddon(t, h)

	for _, rec := range []plugins.Record{native, addon} {
		lists := rec.Manifest.CatalogueLists()
		if len(lists) != 2 {
			t.Fatalf("%s offers %d catalogue lists, want 2: %+v", rec.ID, len(lists), lists)
		}
		for _, cl := range lists {
			if _, ok := h.cols.KindRegistry().Lookup(extend.PluginID(rec.ID, cl.List)); !ok {
				t.Fatalf("%s catalogue list %s (%s/%s) reads list %q, which is not registered", rec.ID, cl.Key, cl.Catalog.Type, cl.Catalog.ID, cl.List)
			}
		}
	}

	// A typeless list gathers the catalog of the source's media type.
	h.rt.routes["/catalog/series/dubbed"] = func(plugins.Request) (plugins.Response, error) {
		return plugins.Response{Status: 200, Body: json.RawMessage(`{"metas":[{"id":"tt0903747","type":"series","name":"Breaking Bad"}]}`)}, nil
	}
	res, err := h.cols.GatherCatalogueSource(ctx, extend.PluginID(native.ID, "dubbed"), "Dubbed (Dubs)", string(core.TypeShow))
	titles := res.Titles
	if err != nil || len(titles) != 1 || titles[0].Type != core.TypeShow {
		t.Fatalf("show gather = %+v, %v", titles, err)
	}
	if _, err := h.cols.GatherCatalogueSource(ctx, extend.PluginID(native.ID, "gone"), "Gone (Dubs)", "movie"); err == nil || !strings.Contains(err.Error(), "Gone (Dubs)") {
		t.Fatalf("missing source error = %v, want it to name the source", err)
	}
}
