package adapters_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// Add-on catalogues are Recommendations row sources under "From plugins ›
// <add-on>", with the catalogue's genres as a choice; catalogues that need
// ids only a client has are not offered.
func TestAddonCatalogsAreRowSources(t *testing.T) {
	h := newListTestHarness(t)
	ctx := context.Background()
	rec := installCatalogAddon(t, h)

	movies := addonCatalogKind(rec, "movie", "top")
	var info core.RowSourceInfo
	for _, s := range h.pers.SourceInfos() {
		if s.Kind == movies {
			info = s
		}
	}
	if info.Kind == "" || info.Group != "plugins" || info.Plugin != "Example catalogs" || !info.Split {
		t.Fatalf("catalogue source = %+v", info)
	}
	if len(info.Params) != 1 || info.Params[0].Key != "genre" || info.Params[0].Type != core.ParamChoice ||
		len(info.Params[0].Choices) != 3 || info.Params[0].Choices[0].Value != "" || info.Params[0].Choices[2].Value != "Sci-Fi" {
		t.Fatalf("genre param = %+v", info.Params)
	}
	if err := info.ValidateParams(map[string]string{"genre": "Western"}); err == nil {
		t.Fatal("a genre the catalogue doesn't offer must be rejected")
	}

	raw := json.RawMessage(`{
		"id": "org.example.calendar", "name": "Calendar", "version": "1.0.0",
		"resources": ["catalog"], "types": ["series"],
		"catalogs": [{"type": "series", "id": "calendar-videos", "extra": [{"name": "calendarVideosIds", "isRequired": true}]}]
	}`)
	preview, err := h.mgr.PreviewInstall(ctx, plugins.Source{Kind: "url", URL: "https://calendar.example/manifest.json", Manifest: raw})
	if err != nil {
		t.Fatal(err)
	}
	cal, err := h.mgr.Install(ctx, preview, plugins.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.pers.RowKindRegistry().Source(addonCatalogKind(cal, "series", "calendar-videos")); ok {
		t.Fatal("a catalogue that needs client ids must not be a row source")
	}
}
