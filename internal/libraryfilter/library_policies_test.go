package libraryfilter

import (
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestCompileLibraryPolicies(t *testing.T) {
	movieRules := []core.LibraryExclusion{{
		Name: "Hide French Movies", MediaType: "movie", Match: "all",
		Rules: []core.CollectionRule{{Field: "language", Op: "is", Value: json.RawMessage(`"fr"`)}},
	}}
	showRules := []core.LibraryExclusion{{
		Name: "Allow Drama Shows", Action: "show", MediaType: "show", Match: "all",
		Rules: []core.CollectionRule{{Field: "genre", Op: "in", Value: json.RawMessage(`["Drama"]`)}},
	}}
	f, err := CompileLibraryPolicies(movieRules, "show", showRules, "hide")
	if err != nil {
		t.Fatal(err)
	}
	frMovie := &core.Title{Type: core.TypeMovie, Language: "fr"}
	enMovie := &core.Title{Type: core.TypeMovie, Language: "en"}
	dramaShow := &core.Title{Type: core.TypeShow, Genres: []string{"Drama"}}
	comedyShow := &core.Title{Type: core.TypeShow, Genres: []string{"Comedy"}}

	if !f.Hidden(frMovie) || f.Hidden(enMovie) {
		t.Fatal("movie rules/default mismatch")
	}
	if f.Hidden(dramaShow) || !f.Hidden(comedyShow) {
		t.Fatal("show rules/default mismatch")
	}
	if hidden, idx, byFallback := f.MatchedHideRule(frMovie); !hidden || byFallback || f.RuleName(idx) != "Hide French Movies" {
		t.Fatalf("MatchedHideRule(frMovie) = %v, %d (%q), %v", hidden, idx, f.RuleName(idx), byFallback)
	}
	if hidden, _, byFallback := f.MatchedHideRule(comedyShow); !hidden || !byFallback {
		t.Fatalf("MatchedHideRule(comedyShow) = %v, byFallback=%v", hidden, byFallback)
	}
}

func TestLibraryScopedRulesDecompositionAndHiddenKeys(t *testing.T) {
	aotKey := core.ShowKey(1429)
	showRules := []core.LibraryExclusion{
		{
			Name: "Keep AoT", Action: "show", MediaType: "show", Match: "all",
			Rules: []core.CollectionRule{{Field: "title", Op: "is", Value: json.RawMessage(`"` + aotKey.String() + `"`)}},
		},
		{
			Name: "Hide Anime From TV Shows", Action: "hide", MediaType: "show", Match: "all", Scope: core.VisibilityLibrary,
			Rules: []core.CollectionRule{{Field: "keyword", Op: "in", Value: json.RawMessage(`["anime"]`)}},
		},
		{
			Name: "Hide Reality Everywhere", Action: "hide", MediaType: "show", Match: "all", Scope: core.VisibilityEverywhere,
			Rules: []core.CollectionRule{{Field: "genre", Op: "in", Value: json.RawMessage(`["Reality"]`)}},
		},
	}

	// Canonical policy strips library-scoped rules but keeps canonical ones.
	canon, err := CompileLibraryPolicies(nil, "show", showRules, "show")
	if err != nil {
		t.Fatal(err)
	}
	naruto := &core.Title{Key: core.ShowKey(46260), Type: core.TypeShow, Keywords: []string{"anime"}}
	aot := &core.Title{Key: aotKey, Type: core.TypeShow, Keywords: []string{"anime"}}
	reality := &core.Title{Key: core.ShowKey(999), Type: core.TypeShow, Genres: []string{"Reality"}}
	if canon.Hidden(naruto) {
		t.Fatal("library-scoped hide rule leaked into canonical policy")
	}
	if !canon.Hidden(reality) {
		t.Fatal("canonical everywhere rule must stay active")
	}

	// Only library-scoped rules leave the canonical filter completely passive.
	onlyLib, err := CompileLibraryPolicies(nil, "show", showRules[:2], "show")
	if err != nil {
		t.Fatal(err)
	}
	if !onlyLib.Passive() {
		t.Fatal("canonical policy with only library-scoped hide rules should be passive")
	}

	// BuiltinLibraryExtraRules preserves preceding show overrides and drops canonical hide rules.
	extraSpecs := core.BuiltinLibraryExtraRules(showRules)
	if len(extraSpecs) != 2 || extraSpecs[0].Action != "show" || extraSpecs[1].Scope != core.VisibilityLibrary {
		t.Fatalf("unexpected extra specs: %+v", extraSpecs)
	}
	extra, err := CompilePolicy(extraSpecs, "show")
	if err != nil {
		t.Fatal(err)
	}
	if !extra.Hidden(naruto) {
		t.Fatal("expected anime show to be hidden by library-scoped extra overlay")
	}
	if extra.Hidden(aot) {
		t.Fatal("expected preceding show rule to exempt Attack on Titan from library-scoped hide rule")
	}
	if extra.Hidden(reality) {
		t.Fatal("canonical hide rule should not be duplicated into library-scoped extra overlay")
	}

	// SQLHiddenKeys produces a selective indexed driver for keyword/country/title hide rules.
	keySQL, keyArgs, ok := extra.SQLHiddenKeys(core.TypeShow)
	if !ok || keySQL == "" || len(keyArgs) != 1 || keyArgs[0] != "anime" {
		t.Fatalf("SQLHiddenKeys = (%q, %v, %v), want keyword driver for anime", keySQL, keyArgs, ok)
	}
	// Show action cannot use scope=library.
	if _, err := CompilePolicy([]core.LibraryExclusion{{
		Action: "show", MediaType: "show", Match: "all", Scope: core.VisibilityLibrary,
		Rules: []core.CollectionRule{{Field: "keyword", Op: "in", Value: json.RawMessage(`["anime"]`)}},
	}}, "show"); err == nil {
		t.Fatal("expected show rule with scope=library to be rejected")
	}
}
