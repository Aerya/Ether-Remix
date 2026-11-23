package catalog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/store"
)

// One invariant across SQL pagination, compiled discovery/search policy, restart,
// global edits and viewer cache revisions: a viewer override never changes peers.
func TestViewerRulesIsolationAndScopes(t *testing.T) {
	db := store.OpenTest(t)
	rule := core.LibraryExclusion{Name: "French", MediaType: "movie", Match: "all", Scope: core.VisibilityDiscovery, Rules: []core.CollectionRule{{Field: "language", Op: "is", Value: json.RawMessage(`"fr"`)}}}
	cfg := config.Catalog{Exclusions: []core.LibraryExclusion{rule}}
	c, err := New(db, nil, cfg, nil, WithLibraryPolicies(testPolicies(cfg.Exclusions, cfg.VisibilityDefault)))
	if err != nil {
		t.Fatal(err)
	}
	title := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "French movie", Language: "fr", Released: time.Now().Add(-24 * time.Hour)}
	if err := c.upsert(t.Context(), title, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	a, b := &core.User{ID: 1}, &core.User{ID: 2}
	check := func(wantA, wantB int) {
		t.Helper()
		for i, u := range []*core.User{a, b} {
			want := []int{wantA, wantB}[i]
			ctx := core.WithCatalogViewer(t.Context(), u)
			page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 1})
			if err != nil || page.Total != want || len(page.Items) != want {
				t.Fatalf("viewer %d: %+v %v want %d", u.ID, page, err, want)
			}
			if got := len(c.FilterVisibility(ctx, []core.Title{title})); got != want {
				t.Fatalf("viewer %d discovery=%d want %d", u.ID, got, want)
			}
			if !c.VisibleForViewer(u, &title) {
				t.Fatal("browsing-only rule blocked lookup")
			}
		}
	}
	check(0, 0)
	candidates, err := c.List(core.WithCatalogCandidates(t.Context()), "popular", core.TypeMovie, 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("shared candidates lost globally hidden titles: %v %v", candidates, err)
	}
	ordinary, err := c.List(t.Context(), "popular", core.TypeMovie, 10)
	if err != nil || len(ordinary) != 0 {
		t.Fatalf("materialization polluted global list cache: %v %v", ordinary, err)
	}
	if err := c.PutViewerRules(t.Context(), a.ID, ViewerRules{Mode: "exempt", Exemptions: []string{RuleIdentity(rule)}}); err != nil {
		t.Fatal(err)
	}
	check(1, 0)
	if c.ViewerRulesRevision(a) != 1 || c.ViewerRulesRevision(b) != 0 {
		t.Fatal("peer revision changed")
	}
	// Reordering unrelated global rules cannot move an exemption to another rule.
	cfg.Exclusions = append([]core.LibraryExclusion{{Name: "English", MediaType: "movie", Match: "all", Rules: []core.CollectionRule{{Field: "language", Op: "is", Value: json.RawMessage(`"en"`)}}}}, rule)
	setTestPolicies(t, c, cfg.Exclusions, cfg.VisibilityDefault)
	check(1, 0)
	if err := c.PutViewerRules(t.Context(), a.ID, ViewerRules{Mode: "custom", VisibilityDefault: "show"}); err != nil {
		t.Fatal(err)
	}
	c, err = New(db, nil, cfg, nil, WithLibraryPolicies(testPolicies(cfg.Exclusions, cfg.VisibilityDefault)))
	if err != nil {
		t.Fatal(err)
	}
	check(1, 0)
	if err := c.PutViewerRules(t.Context(), a.ID, ViewerRules{Mode: "inherit"}); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
	if c.ViewerRulesRevision(a) != 3 || c.ViewerRulesRevision(b) != 0 {
		t.Fatal("revision isolation lost after reset")
	}
}

// A bad persisted document must never take down the server or leak titles to
// its viewer, including search/direct lookup and explicit owner inclusions.
func TestStoredViewerRulesRecovery(t *testing.T) {
	db := store.OpenTest(t)
	good := `{"mode":"custom","exclusions":[{"name":"Movies","mediaType":"Movies","match":"all","scope":"discovery","rules":[{"field":"browse:type","op":"is","value":"Movies"}]}]}`
	bad := `{"mode":"custom","exclusions":[{"name":"Broken type","mediaType":"movie","match":"all","rules":[{"field":"browse:type","op":"is","value":"garbage"}]}]}`
	for i, raw := range []string{good, bad, `{broken`} {
		if _, err := db.Exec(`INSERT INTO catalog_viewer_rules(viewer_id,document,revision) VALUES(?,?,1)`, i+1, raw); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := libraries.New(t.Context(), db, libraries.Options{}); err != nil {
		t.Fatal(err)
	}
	var legacy ViewerRules
	if err := json.Unmarshal([]byte(good), &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Exclusions[0].MediaType = "movie"
	libraryRaw, _ := json.Marshal(legacy.Exclusions)
	if _, err := db.Exec(`UPDATE libraries SET rules=? WHERE id=1`, string(libraryRaw)); err != nil {
		t.Fatal(err)
	}
	exemptRaw, _ := json.Marshal(ViewerRules{Mode: "exempt", Exemptions: []string{RuleIdentity(legacy.Exclusions[0])}})
	if _, err := db.Exec(`INSERT INTO catalog_viewer_rules(viewer_id,document,revision) VALUES(4,?,1)`, string(exemptRaw)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE component='libraries' AND version=8`); err != nil {
		t.Fatal(err)
	}
	// Replay the actual one-time migration over pre-upgrade data.
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE component='catalog_viewer_rules' AND version=2`); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	var persisted string
	if err := db.QueryRow(`SELECT document FROM catalog_viewer_rules WHERE viewer_id=1`).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, `"value":"Movies"`) || !strings.Contains(persisted, `"value":"movie"`) || !strings.Contains(persisted, `"name":"Movies"`) {
		t.Fatalf("migration changed the wrong fields: %s", persisted)
	}
	var warnings bytes.Buffer
	c, err := New(db, nil, config.Catalog{}, slog.New(slog.NewTextHandler(&warnings, nil)))
	if err != nil {
		t.Fatal(err)
	}
	canonical := core.NormalizeLibraryRules(legacy.Exclusions)
	if got := c.ViewerRules(4).Exemptions; len(got) != 1 || got[0] != RuleIdentity(canonical[0]) {
		t.Fatalf("exemption lost during normalization: %v", got)
	}
	p := c.ViewerRules(1)
	if p.Broken || p.Exclusions[0].MediaType != "movie" {
		t.Fatalf("canonical rule: %+v", p)
	}
	for _, id := range []int64{2, 3} {
		if !c.ViewerRules(id).Broken {
			t.Fatalf("viewer %d not reported broken", id)
		}
	}
	if !strings.Contains(warnings.String(), "Broken type") || !strings.Contains(warnings.String(), "viewer_id=3") {
		t.Fatalf("missing diagnostics: %s", warnings.String())
	}
	title := core.Title{Key: core.MovieKey(22), Type: core.TypeMovie, Title: "Visible", Released: time.Now().Add(-48 * time.Hour)}
	if err := c.upsert(t.Context(), title, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO catalog_owner_includes(key) VALUES(?)`, int64(title.Key)); err != nil {
		t.Fatal(err)
	}
	if err := c.reloadOwnerIncludes(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{2, 3} {
		u := &core.User{ID: id}
		if c.VisibleForViewer(u, &title) || c.DiscoveryVisibleForViewer(u, &title) {
			t.Fatalf("broken viewer %d leaked direct title", id)
		}
		for _, search := range []bool{false, true} {
			ctx := core.WithCatalogViewer(t.Context(), u)
			if search {
				ctx = core.WithExplicitSearch(ctx)
			}
			page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 10})
			if err != nil || len(page.Items) != 0 {
				t.Fatalf("broken viewer %d browse: %+v %v", id, page, err)
			}
		}
	}
	if !c.VisibleForViewer(&core.User{ID: 4}, &title) {
		t.Fatal("healthy peer lost access")
	}
	if err := c.PutViewerRules(t.Context(), 2, ViewerRules{Mode: "custom", Exclusions: []core.LibraryExclusion{{MediaType: "Films", Match: "all", Rules: []core.CollectionRule{{Field: "browse:type", Op: "is", Value: json.RawMessage(`"TV Shows"`)}}}}}); err != nil {
		t.Fatal(err)
	}
	if c.ViewerRules(2).Broken || string(c.ViewerRules(2).Exclusions[0].Rules[0].Value) != `"show"` {
		t.Fatal("repair did not normalize or clear broken state")
	}
	if err := c.RefreshViewerRules(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c.ViewerRules(2).Broken || !c.ViewerRules(3).Broken {
		t.Fatal("reload lost independent recovery state")
	}
}
