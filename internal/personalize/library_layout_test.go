package personalize

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestMigrateLibraryLayoutsUpgradesMoviesAndShowsOnce(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Seed pre-0.3.16 libraries and a legacy HomeLayout containing "movies" and
	// "shows" pages plus an already-customised "library:3" page.
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS libraries (
			id INTEGER PRIMARY KEY,
			source TEXT NOT NULL DEFAULT 'builtin',
			media_type TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO libraries (id, source, media_type) VALUES
			(1, 'builtin', 'movie'),
			(2, 'builtin', 'show'),
			(3, 'catalog', 'movie'),
			(4, 'catalog', 'movie'),
			(9, 'plugin',  'movie');
		CREATE TABLE IF NOT EXISTS personalize_home_layout (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			document TEXT NOT NULL
		);
	`); err != nil {
		t.Fatal(err)
	}

	dedupeOff := false
	legacyDoc := core.HomeLayout{
		Global: []core.LayoutPage{
			{
				Section: "home",
				MaxRows: 6,
				Rows:    []core.RowPolicy{{Row: "home-anchor", Status: "always", Slot: 1}},
			},
			{
				Section:    "movies",
				MaxRows:    4,
				DedupeHome: &dedupeOff,
				Rows:       []core.RowPolicy{{Row: "movies-anchor", Status: "always", Slot: 1}},
			},
			{
				Section: "shows",
				MaxRows: 5,
				Rows:    []core.RowPolicy{{Row: "shows-anchor", Status: "always", Slot: 1}},
			},
			{
				Section: "library:3",
				MaxRows: 2,
				Rows:    []core.RowPolicy{{Row: "custom-lib3", Status: "always", Slot: 1}},
			},
		},
		Viewers: []core.ViewerLayout{
			{
				UserID: 7,
				Pages: []core.LayoutPage{
					{Section: "movies", Rows: []core.RowPolicy{{Row: "viewer-movies", Status: "always", Slot: 2}}},
					{Section: "shows", Rows: []core.RowPolicy{{Row: "viewer-shows", Status: "always", Slot: 2}}},
				},
			},
		},
	}
	raw, err := json.Marshal(legacyDoc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO personalize_home_layout(id, document) VALUES(1, ?)`, string(raw)); err != nil {
		t.Fatal(err)
	}

	clk := newClock(testNow)
	cat := newFakeCatalog(testNow)
	users := &fakeUsers{}
	st := state.New(db, state.Options{Now: clk.Now, Logger: quietLogger()})
	makeEngine := func() *Engine {
		e, nerr := New(Options{
			DB: db, Catalog: cat, State: st, Events: st, Users: users,
			Logger: quietLogger(), Now: clk.Now, Location: time.UTC,
			RefreshEvery: -1, RebuildDebounce: time.Hour, ColdWait: 5 * time.Second,
			OwnerRowsWait: 2 * time.Second, FlushEvery: time.Hour,
			ServerName:  func() string { return "Test Server" },
			MinRowItems: -1,
			LibrarySections: func(context.Context) map[int]core.MediaType {
				return map[int]core.MediaType{
					1: core.TypeMovie,
					2: core.TypeShow,
					3: core.TypeMovie,
					4: core.TypeMovie,
				}
			},
		})
		if nerr != nil {
			t.Fatal(nerr)
		}
		return e
	}

	e1 := makeEngine()
	migrated, err := e1.HomeLayout(ctx)
	e1.Close()
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range migrated.Global {
		if p.Section == "movies" || p.Section == "shows" || p.Section == "library:9" {
			t.Fatalf("unexpected section %q in migrated Global: %+v", p.Section, migrated.Global)
		}
	}
	lib1 := pageFor(migrated.Global, "library:1")
	lib2 := pageFor(migrated.Global, "library:2")
	lib3 := pageFor(migrated.Global, "library:3")
	lib4 := pageFor(migrated.Global, "library:4")
	if lib1 == nil || lib1.MaxRows != 4 || lib1.DedupeHome == nil || *lib1.DedupeHome || len(lib1.Rows) != 1 || lib1.Rows[0].Row != "movies-anchor" {
		t.Fatalf("library:1 not migrated from movies: %+v", lib1)
	}
	if lib4 == nil || lib4.MaxRows != 4 || len(lib4.Rows) != 1 || lib4.Rows[0].Row != "movies-anchor" {
		t.Fatalf("library:4 not migrated from movies: %+v", lib4)
	}
	if lib2 == nil || lib2.MaxRows != 5 || len(lib2.Rows) != 1 || lib2.Rows[0].Row != "shows-anchor" {
		t.Fatalf("library:2 not migrated from shows: %+v", lib2)
	}
	if lib3 == nil || lib3.MaxRows != 2 || len(lib3.Rows) != 1 || lib3.Rows[0].Row != "custom-lib3" {
		t.Fatalf("existing library:3 customisation overwritten: %+v", lib3)
	}
	if len(migrated.Viewers) != 1 {
		t.Fatalf("expected 1 viewer layout, got %+v", migrated.Viewers)
	}
	for _, vp := range migrated.Viewers[0].Pages {
		if vp.Section == "movies" || vp.Section == "shows" {
			t.Fatalf("legacy viewer section %q survived migration", vp.Section)
		}
	}
	if pageFor(migrated.Viewers[0].Pages, "library:1") == nil || pageFor(migrated.Viewers[0].Pages, "library:2") == nil {
		t.Fatalf("viewer overrides not migrated to library:<id>: %+v", migrated.Viewers[0].Pages)
	}

	// Clearing library:4 stays cleared across engine restarts (migration runs once).
	e2 := makeEngine()
	var kept []core.LayoutPage
	for _, p := range migrated.Global {
		if p.Section != "library:4" {
			kept = append(kept, p)
		}
	}
	migrated.Global = kept
	if _, err := e2.SetHomeLayout(ctx, migrated); err != nil {
		e2.Close()
		t.Fatal(err)
	}
	e2.Close()

	e3 := makeEngine()
	afterRestart, err := e3.HomeLayout(ctx)
	e3.Close()
	if err != nil {
		t.Fatal(err)
	}
	if pageFor(afterRestart.Global, "library:4") != nil {
		t.Fatalf("library:4 reappeared on restart: %+v", afterRestart.Global)
	}
}

func TestLibraryLayoutsHaveZeroInheritanceAndRejectSectionSurfaces(t *testing.T) {
	hc := hubContext(testNow, core.DeviceTV)
	dedupeOff := false

	doc := core.HomeLayout{
		Global: []core.LayoutPage{
			{
				Section: "home",
				MaxRows: 1,
				Rows: []core.RowPolicy{
					{Row: "home-only", Status: "always", Slot: 1},
					{Row: "plain", Status: "off"},
				},
			},
			{
				Section:    "library:1",
				MaxRows:    2,
				DedupeHome: &dedupeOff,
				Rows: []core.RowPolicy{
					{Row: "lib1-anchor", Status: "always", Slot: 1},
					{Row: "plain", Status: "off"},
				},
			},
		},
		Viewers: []core.ViewerLayout{
			{
				UserID: 7,
				Pages: []core.LayoutPage{
					{
						Section: "library:1",
						Rows:    []core.RowPolicy{{Row: "viewer-lib1", Status: "always", Slot: 2}},
					},
				},
			},
		},
	}

	normalized, err := normalizeHomeLayout(doc)
	if err != nil {
		t.Fatalf("normalizeHomeLayout: %v", err)
	}

	// library:1 gets its own Global + Viewer policies.
	lib1 := arrangeLayoutFor(
		sampleRows("plain", "viewer-lib1", "lib1-anchor", "home-only"),
		normalized, 7, hc, "library:1", 10,
	)
	if got := visibleIDs(lib1); !reflect.DeepEqual(got, []string{"lib1-anchor", "viewer-lib1"}) {
		t.Fatalf("library:1 visibleIDs = %v, want [lib1-anchor viewer-lib1]", got)
	}
	if resolveDedupeHome(normalized, 7, "library:1") {
		t.Fatal("expected library:1 DedupeHome=false")
	}

	// library:3 has no page in doc: it inherits NOTHING from home or library:1.
	lib3 := arrangeLayoutFor(
		sampleRows("plain", "viewer-lib1", "lib1-anchor", "home-only"),
		normalized, 7, hc, "library:3", 10,
	)
	if got := visibleIDs(lib3); !reflect.DeepEqual(got, []string{"plain", "viewer-lib1", "lib1-anchor", "home-only"}) {
		t.Fatalf("uncustomised library:3 inherited policies from home/library:1: got %v", got)
	}
	if !resolveDedupeHome(normalized, 7, "library:3") {
		t.Fatal("expected uncustomised library:3 DedupeHome default true")
	}

	for _, bad := range []string{"movies", "shows", "library:", "library:0", "library:-2", "library:abc"} {
		if _, err := normalizeHomeLayout(core.HomeLayout{
			Global: []core.LayoutPage{{Section: bad}},
		}); err == nil {
			t.Fatalf("expected normalizeHomeLayout to reject section %q", bad)
		}
	}
}

func TestSectionHubsAndPreviewHonorPerLibraryLayout(t *testing.T) {
	h := newHarness(t, func(o *Options) {
		o.LibrarySections = func(context.Context) map[int]core.MediaType {
			return map[int]core.MediaType{
				1: core.TypeMovie,
				2: core.TypeShow,
				3: core.TypeMovie,
			}
		}
	})
	ctx := context.Background()
	sciFiFan(h, 1)
	u := core.User{ID: 1, IsOwner: true}
	h.rebuild(u)

	hc1 := hubContext(testNow, core.DeviceTV)
	hc1.SectionID = 1
	initial, err := h.e.SectionHubsFor(ctx, &u, core.TypeMovie, hc1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial) < 2 {
		t.Fatalf("need at least 2 movie hubs to test per-library layouts, got %d", len(initial))
	}
	firstID, secondID := initial[0].ID, initial[1].ID

	_, err = h.e.SetHomeLayout(ctx, core.HomeLayout{
		Global: []core.LayoutPage{
			{
				Section: "home",
				MaxRows: 1,
				Rows:    []core.RowPolicy{{Row: firstID, Status: "off"}},
			},
			{
				Section: "library:1",
				Rows:    []core.RowPolicy{{Row: firstID, Status: "always", Slot: 1}},
			},
			{
				Section: "library:3",
				MaxRows: 1,
				Rows: []core.RowPolicy{
					{Row: firstID, Status: "off"},
					{Row: secondID, Status: "always", Slot: 1},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("SetHomeLayout: %v", err)
	}

	hubs1, err := h.e.SectionHubsFor(ctx, &u, core.TypeMovie, hc1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hubs1) == 0 || hubs1[0].ID != firstID {
		t.Fatalf("library 1 first hub = %+v, want %q", hubs1, firstID)
	}

	hc3 := hubContext(testNow, core.DeviceTV)
	hc3.SectionID = 3
	hubs3, err := h.e.SectionHubsFor(ctx, &u, core.TypeMovie, hc3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hubs3) != 1 || hubs3[0].ID != secondID {
		t.Fatalf("library 3 hubs = %+v, want single hub %q", hubs3, secondID)
	}

	prev3, err := h.e.PreviewHome(ctx, &u, hubContext(testNow, core.DeviceTV), "library:3", 10)
	if err != nil {
		t.Fatalf("PreviewHome(library:3): %v", err)
	}
	if vis3 := visibleIDs(prev3.Rows); !reflect.DeepEqual(vis3, []string{secondID}) {
		t.Fatalf("PreviewHome(library:3) visible = %v, want [%s]", vis3, secondID)
	}

	for _, legacy := range []string{"movies", "shows"} {
		if _, err := h.e.PreviewHome(ctx, &u, hubContext(testNow, core.DeviceTV), legacy, 10); err == nil {
			t.Fatalf("expected PreviewHome(%q) to fail validation", legacy)
		}
	}
}

func TestPreviewCandidateRowsArrangesPluginShelves(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	u := &core.User{ID: 1, IsOwner: true}
	candidates := []core.HomeRow{
		{HubID: "plugin:9:added", Title: "Recently Added", Kind: "recentlyAdded", Items: []core.Title{{Key: 901, Title: "Clip A"}}},
		{HubID: "plugin:9:released", Title: "Newest", Kind: "recentlyReleased", Items: []core.Title{{Key: 902, Title: "Clip B"}}},
		{HubID: "plugin:9:rating", Title: "Top Rated", Kind: "topRated", Items: []core.Title{{Key: 903, Title: "Clip C"}}},
	}
	draft := core.HomeLayout{
		Global: []core.LayoutPage{
			{
				Section: "library:9",
				MaxRows: 2,
				Rows: []core.RowPolicy{
					{Row: "plugin:9:rating", Status: "always", Slot: 1},
					{Row: "plugin:9:added", Status: "off"},
				},
			},
		},
	}
	prev, err := h.e.PreviewCandidateRows(ctx, u, hubContext(testNow, core.DeviceTV), "library:9", 8, draft, candidates)
	if err != nil {
		t.Fatalf("PreviewCandidateRows: %v", err)
	}
	if got := visibleIDs(prev.Rows); !reflect.DeepEqual(got, []string{"plugin:9:rating", "plugin:9:released"}) {
		t.Fatalf("PreviewCandidateRows visible = %v, want [plugin:9:rating plugin:9:released]", got)
	}
}

func TestPreviewLayoutPinnedProbePopulatesLibraryRows(t *testing.T) {
	lib3Allowed := map[core.RatingKey]bool{movieKey(0, 9): true}
	h := newHarness(t, func(o *Options) {
		o.MinRowItems = 12
		o.LibrarySections = func(context.Context) map[int]core.MediaType {
			return map[int]core.MediaType{1: core.TypeMovie, 3: core.TypeMovie}
		}
		o.LibraryMembers = func(_ context.Context, sectionID core.LibraryID, keys []core.RatingKey) (map[core.RatingKey]bool, int64, bool, error) {
			if sectionID != 3 {
				return nil, 0, false, nil
			}
			out := map[core.RatingKey]bool{}
			for _, k := range keys {
				if lib3Allowed[k] {
					out[k] = true
				}
			}
			return out, 1, true, nil
		}
	})
	ctx := context.Background()
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := waitGenericReady(h.e, waitCtx); err != nil {
		t.Fatal(err)
	}

	u := core.User{ID: 1, Username: "alice", IsOwner: true}
	sciFiFan(h, u.ID)
	h.rebuild(u)
	hc := hubContext(testNow, core.DeviceTV)

	probe := func(user *core.User, section, row string) *core.HomeRow {
		t.Helper()
		draft := core.HomeLayout{
			Global: []core.LayoutPage{{
				Section: section,
				Rows:    []core.RowPolicy{{Row: row, Status: "always", Slot: 1}},
			}},
		}
		prev, err := h.e.PreviewLayout(ctx, user, hc, section, 12, draft)
		if err != nil {
			t.Fatalf("PreviewLayout(%s, %s): %v", section, row, err)
		}
		for i := range prev.Rows {
			if prev.Rows[i].Kind == row {
				return &prev.Rows[i]
			}
		}
		return nil
	}

	// Rows not on the default movie tab (Comfort Rewatches, New This Week, Popular)
	// resolve their real library-scoped posters when probed as pinned.
	for _, kind := range []string{KindComfort, KindNewThisWeek, KindPopular} {
		row := probe(&u, "library:1", kind)
		if row == nil || len(row.Items) == 0 {
			t.Fatalf("probed %s on library:1 = %+v, want populated row", kind, row)
		}
		for _, it := range row.Items {
			if it.Type != core.TypeMovie {
				t.Fatalf("probed %s leaked %v onto movie library", kind, it.Type)
			}
		}
	}

	// Custom scoped library:3 restricts the probed row to that library's members.
	lib3 := probe(&u, "library:3", KindComfort)
	if lib3 == nil || len(lib3.Items) != 1 || lib3.Items[0].Key != movieKey(0, 9) {
		t.Fatalf("probed comfort on library:3 = %+v, want only movieKey(0,9)", lib3)
	}

	// Typical viewer (ID 0) gets typical_viewer_personal on personal rows and real posters on Popular.
	typical := &core.User{IsOwner: true}
	if row := probe(typical, "library:1", KindComfort); row == nil || row.HiddenCode != "typical_viewer_personal" {
		t.Fatalf("typical viewer comfort = %+v, want typical_viewer_personal", row)
	}
	if row := probe(typical, "library:1", KindPopular); row == nil || len(row.Items) == 0 {
		t.Fatalf("typical viewer popular = %+v, want populated row", row)
	}
}

func TestDeclaredPluginShelvesUncappedByDefaultAndHonorOwnerOverrides(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	u := &core.User{ID: 1, IsOwner: true}

	candidates := make([]core.NeutralHomeRow, 0, 26)
	candidates = append(candidates, core.NeutralHomeRow{
		ID: "plugin:9:continue", Title: "Continue Watching", Kind: "continue",
		Items: []core.Title{{Key: core.PluginKey(1), Title: "Episode 1"}},
	})
	for i := 1; i <= 25; i++ {
		candidates = append(candidates, core.NeutralHomeRow{
			ID: fmt.Sprintf("plugin:9:shelf:topic-%02d", i), Title: fmt.Sprintf("Topic %02d", i), Kind: "shelf",
			Items: []core.Title{{Key: core.PluginKey(int64(100 + i)), Title: fmt.Sprintf("Doc %02d", i)}},
		})
	}

	sit := core.HomeSituation{
		LibraryID:      9,
		Device:         core.DeviceWeb,
		Now:            testNow,
		DefaultMaxRows: len(candidates),
	}

	// Uncustomized layout preserves all 26 rows in declared order (not clamped to MaxSectionRows=16).
	all, err := h.e.FinalizeNeutralHome(ctx, u, sit, "library:9", candidates)
	if err != nil {
		t.Fatalf("FinalizeNeutralHome: %v", err)
	}
	if len(all) != len(candidates) {
		t.Fatalf("uncustomized declared shelves returned %d rows, want %d", len(all), len(candidates))
	}
	if all[0].ID != "plugin:9:continue" || all[1].ID != "plugin:9:shelf:topic-01" || all[25].ID != "plugin:9:shelf:topic-25" {
		t.Fatalf("unexpected row order: first=%s second=%s last=%s", all[0].ID, all[1].ID, all[25].ID)
	}

	// Owner overrides (MaxRows, hide, pin/reorder) still win.
	_, err = h.e.SetHomeLayout(ctx, core.HomeLayout{
		Global: []core.LayoutPage{{
			Section: "library:9",
			MaxRows: 5,
			Rows: []core.RowPolicy{
				{Row: "plugin:9:shelf:topic-20", Status: "always", Slot: 2},
				{Row: "plugin:9:shelf:topic-01", Status: "off"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("SetHomeLayout: %v", err)
	}
	overridden, err := h.e.FinalizeNeutralHome(ctx, u, sit, "library:9", candidates)
	if err != nil {
		t.Fatalf("FinalizeNeutralHome with overrides: %v", err)
	}
	if len(overridden) != 5 {
		t.Fatalf("owner MaxRows=5 returned %d rows, want 5", len(overridden))
	}
	if overridden[0].ID != "plugin:9:continue" || overridden[1].ID != "plugin:9:shelf:topic-20" {
		t.Fatalf("owner pin/order not honored: %+v", []string{overridden[0].ID, overridden[1].ID})
	}
	for _, r := range overridden {
		if r.ID == "plugin:9:shelf:topic-01" {
			t.Fatal("hidden shelf topic-01 must not appear")
		}
	}
}
