package experience

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type lastViewedShelfPluginLibs struct {
	fakePluginLibraries
	pres       PluginPresentation
	lastQuery  frontend.ItemQuery
	emptyWhen  func(q frontend.ItemQuery) bool
	itemsByLvl map[string][]core.Title
}

func (f *lastViewedShelfPluginLibs) Presentation(_ context.Context, _ core.Library) (PluginPresentation, bool) {
	return f.pres, true
}

func (f *lastViewedShelfPluginLibs) FacetBuckets(context.Context, core.Library, string) ([]frontend.FacetBucket, error) {
	return nil, nil
}

func (f *lastViewedShelfPluginLibs) FacetCollections(context.Context, core.Library, string, string, string, int, int) (frontend.FacetCollectionPage, error) {
	return frontend.FacetCollectionPage{}, nil
}

func (f *lastViewedShelfPluginLibs) FacetPeople(context.Context, core.Library, string, string, string, int, int) (PluginFacetPeoplePage, error) {
	return PluginFacetPeoplePage{}, nil
}

func (f *lastViewedShelfPluginLibs) Browse(_ context.Context, _ core.Library, q frontend.ItemQuery, _ string, _ int) (PluginBrowsePage, error) {
	f.lastQuery = q
	if f.emptyWhen != nil && f.emptyWhen(q) {
		return PluginBrowsePage{Items: []core.Title{}}, nil
	}
	items := f.itemsByLvl[q.Level]
	return PluginBrowsePage{Items: items, Total: len(items), TotalExact: true}, nil
}

func TestDeclaredLastViewedShelfRequiresViewerHistoryAndHidesWhenEmpty(t *testing.T) {
	ctx := context.Background()
	lib := core.Library{
		ID:        88,
		Key:       "plugin:community.music:music",
		Name:      "Music",
		Source:    core.LibraryPlugin,
		PluginID:  "community.music",
		KindKey:   "music",
		MediaType: core.TypeShow,
	}
	album := core.Title{
		Key:        core.PluginKey(501),
		LibraryID:  88,
		Type:       core.TypeSeason,
		Title:      "Kind of Blue",
		MediaClass: core.MediaClassMusic,
		MediaRole:  core.MediaRoleAlbum,
	}
	var seenHistoryRule bool
	plug := &lastViewedShelfPluginLibs{
		fakePluginLibraries: fakePluginLibraries{
			libs: []core.Library{lib},
		},
		pres: PluginPresentation{
			Media: core.MediaClassMusic,
			Card:  "square",
			Shelves: []PluginShelf{
				{Key: "recent", Label: "Jump back in", Level: "album", Sort: core.SortLastViewed, Direction: "desc"},
			},
		},
		itemsByLvl: map[string][]core.Title{"album": {album}},
		emptyWhen: func(q frontend.ItemQuery) bool {
			for _, g := range q.Filter.TabRules {
				for _, r := range g.Rules {
					if r.Field == "viewer" && r.Op == "is" {
						var v map[string]any
						if json.Unmarshal(r.Value, &v) == nil && v["history"] == true {
							seenHistoryRule = true
						}
					}
				}
			}
			// Simulate viewer having no watch history yet.
			return true
		},
	}

	svc, _ := peopleFixture()
	svc.d.PluginLibraries = plug
	svc.d.LibraryPresentation = func(_ core.Presentations, mt core.MediaType) (core.MediaType, string, bool) {
		return mt, "shows", true
	}
	owner := &core.User{ID: 1, IsOwner: true}

	// Views propagates Media class onto frontend.View.
	views, err := svc.Views(ctx, owner)
	if err != nil {
		t.Fatalf("Views: %v", err)
	}
	var gotMedia core.MediaClass
	for _, vw := range views {
		if vw.ID == "88" {
			gotMedia = vw.Media
		}
	}
	if gotMedia != core.MediaClassMusic {
		t.Fatalf("View.Media = %q, want %q", gotMedia, core.MediaClassMusic)
	}

	// Empty history: shelf must inject viewer history filter AND hide cleanly (0 rows).
	rows, err := svc.PluginLibraryRows(ctx, owner, core.Device{}, "88")
	if err != nil {
		t.Fatalf("PluginLibraryRows err=%v", err)
	}
	if !seenHistoryRule {
		t.Fatalf("expected sort:lastViewed shelf to inject viewer history TabRules, got %+v (fallback=%q)", plug.lastQuery.Filter.TabRules, plug.lastQuery.Filter.TabFallback)
	}
	if plug.lastQuery.Filter.TabFallback != "hide" {
		t.Fatalf("TabFallback = %q, want hide", plug.lastQuery.Filter.TabFallback)
	}
	if len(rows) != 0 {
		t.Fatalf("empty lastViewed shelf should hide, got %d rows: %+v", len(rows), rows)
	}

	// Once viewer has history, the shelf appears with the rolled-up album.
	plug.emptyWhen = nil
	rows, err = svc.PluginLibraryRows(ctx, owner, core.Device{}, "88")
	if err != nil {
		t.Fatalf("PluginLibraryRows with history err=%v", err)
	}
	if len(rows) != 1 || rows[0].Title != "Jump back in" || len(rows[0].Items) != 1 || rows[0].Items[0].Title != "Kind of Blue" {
		t.Fatalf("unexpected rows with history: %+v", rows)
	}
}
