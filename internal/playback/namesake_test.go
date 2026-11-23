package playback

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// namesakeCatalog overrides the harness catalogue's Namesakes answer and
// records how often playback asked.
type namesakeCatalog struct {
	*fakeCatalog
	namesakes map[core.RatingKey]bool
	asked     int
	updated   map[core.RatingKey]time.Duration
}

func (c *namesakeCatalog) Namesakes(_ context.Context, key core.RatingKey) (bool, error) {
	c.asked++
	return c.namesakes[key], nil
}

func (c *namesakeCatalog) UpdateRuntime(_ context.Context, key core.RatingKey, runtime time.Duration) error {
	if c.updated == nil {
		c.updated = map[core.RatingKey]time.Duration{}
	}
	c.updated[key] = runtime
	if t := c.titles[key]; t != nil {
		cp := *t
		cp.Runtime = runtime
		c.titles[key] = &cp
	}
	return nil
}

// withReleaseIDs stamps the reported identifiers the resolver would attach
// to a release, so the identifier gate sees an explicit conflict.
func (h *harness) withReleaseIDs(releaseID string, ids core.IDs) {
	for i := range h.resolver.candidates {
		if h.resolver.candidates[i].ID == releaseID {
			h.resolver.candidates[i].TitleIDs = ids
		}
	}
}

// TestNamesakeRuntimeRejectsWrongFilm is the lasting invariant: two namesake
// 2026 movies share a normalized title and year, a release is named for one
// but its probed duration belongs to the other, so it is rejected and the
// next candidate plays.
func TestNamesakeRuntimeRejectsWrongFilm(t *testing.T) {
	// "The Runner" (catalogue runtime 98m); the release holds an 85m file.
	catalogue := 98 * time.Minute
	probed := 85 * time.Minute
	h := newHarness(t, []*fakeRelease{mkv("wrong", 100_000, probed), mkv("right", 100_000, catalogue)}, []string{"wrong", "right"})
	h.catalog.titles[movieKey] = &core.Title{Key: movieKey, Type: core.TypeMovie, Title: "Runner", Year: 2026, Runtime: catalogue}
	cat := &namesakeCatalog{fakeCatalog: h.catalog, namesakes: map[core.RatingKey]bool{movieKey: true}}
	h.m.deps.Catalog = cat

	ps := h.prepare(owner, movieKey, "c1")
	if got := ps.Info.Duration; got != catalogue {
		t.Fatalf("played duration = %v, want %v (namesake with the wrong film played)", got, catalogue)
	}
	if h.resolver.isBad("wrong") {
		t.Fatal("title mismatch must not globally blacklist the release")
	}
	if h.resolver.isBad("right") {
		t.Fatal("correct release was blocked")
	}
}

// TestNamesakeNonAmbiguousKeepsExtendedCut proves the gate is namesake-only:
// an unambiguous title keeps a probed duration that differs from catalogue.
func TestNamesakeNonAmbiguousKeepsExtendedCut(t *testing.T) {
	catalogue := 98 * time.Minute
	probed := 130 * time.Minute // an extended cut
	h := newHarness(t, []*fakeRelease{mkv("extended", 100_000, probed)}, []string{"extended"})
	h.catalog.titles[movieKey] = &core.Title{Key: movieKey, Type: core.TypeMovie, Title: "Runner", Year: 2026, Runtime: catalogue}
	cat := &namesakeCatalog{fakeCatalog: h.catalog, namesakes: map[core.RatingKey]bool{}}
	h.m.deps.Catalog = cat

	ps := h.prepare(owner, movieKey, "c1")
	if got := ps.Info.Duration; got != probed {
		t.Fatalf("played duration = %v, want %v (extended cut should play)", got, probed)
	}
	if h.resolver.isBad("extended") {
		t.Fatal("extended cut of an unambiguous title was blocked")
	}
}

// TestNamesakeIdentifierConflictSkipsRelease rejects a release whose reported
// identifiers name the other title before it is opened.
func TestNamesakeIdentifierConflictSkipsRelease(t *testing.T) {
	// Both carry the catalogue runtime so the duration gate leaves them alone.
	h := newHarness(t, []*fakeRelease{mkv("other", 100_000, 98*time.Minute), mkv("this", 100_000, 98*time.Minute)}, []string{"other", "this"})
	h.catalog.titles[movieKey] = &core.Title{Key: movieKey, Type: core.TypeMovie, Title: "Runner", Year: 2026, Runtime: 98 * time.Minute, IDs: core.IDs{TMDB: 100}}
	h.withReleaseIDs("other", core.IDs{TMDB: 200})
	cat := &namesakeCatalog{fakeCatalog: h.catalog, namesakes: map[core.RatingKey]bool{movieKey: true}}
	h.m.deps.Catalog = cat

	ps := h.prepare(owner, movieKey, "c1")
	if h.m.lookup(ps.ID) == nil {
		t.Fatal("no session")
	}
	if h.tr.probes != 1 {
		// Only the surviving release was opened for probing.
		t.Fatalf("probes = %d, want 1 (the conflicting release should be skipped)", h.tr.probes)
	}
	if reason, ok := h.resolver.bad["other"]; !ok || reason != "release names the other title" {
		t.Fatalf("bad reason = %q, want identifier conflict", reason)
	}
}

// TestNamesakeLookupCachedOnce proves playback asks the catalogue once per
// title, not once per candidate.
func TestNamesakeLookupCachedOnce(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("wrong", 100_000, 85*time.Minute), mkv("right", 100_000, 98*time.Minute)}, []string{"wrong", "right"})
	h.catalog.titles[movieKey] = &core.Title{Key: movieKey, Type: core.TypeMovie, Title: "Runner", Year: 2026, Runtime: 98 * time.Minute}
	cat := &namesakeCatalog{fakeCatalog: h.catalog, namesakes: map[core.RatingKey]bool{movieKey: true}}
	h.m.deps.Catalog = cat

	h.prepare(owner, movieKey, "c1")
	if cat.asked != 1 {
		t.Fatalf("namesake lookups = %d, want 1 (cached per title)", cat.asked)
	}
}

// TestNamesakeDurationTolerance checks the tolerance bound in isolation.
func TestNamesakeDurationTolerance(t *testing.T) {
	cases := []struct {
		name     string
		runtime  time.Duration
		probed   time.Duration
		mismatch bool
	}{
		{"exact", 98 * time.Minute, 98 * time.Minute, false},
		{"within 8m slack short", 98 * time.Minute, 91 * time.Minute, false},
		{"beyond 8m slack short", 98 * time.Minute, 85 * time.Minute, true},
		{"within ratio long", 200 * time.Minute, 220 * time.Minute, false},
		{"beyond ratio long", 200 * time.Minute, 230 * time.Minute, true},
		{"unknown probe", 98 * time.Minute, 0, false},
		{"unknown runtime", 0, 85 * time.Minute, false},
	}
	for _, c := range cases {
		if got := namesakeDurationMismatch(c.runtime, c.probed); got != c.mismatch {
			t.Errorf("%s: mismatch = %v, want %v", c.name, got, c.mismatch)
		}
	}
}

type rejectCollector struct {
	core.Resolver
	rejected []string
}

func (r *rejectCollector) RejectRelease(_ context.Context, _ core.RatingKey, rel core.Release) error {
	r.rejected = append(r.rejected, rel.ID)
	return nil
}

// TestRuntimeConsensusOverridesStaleCatalogueMetadata verifies that when
// upstream catalogue metadata has the wrong runtime (e.g. "The Runner" 2026:
// 1h26m in catalogue vs 1h37m56s in every actual release), three independent
// releases agreeing on the probed duration establish consensus, play without
// permanently rejecting earlier candidates, and persist the corrected runtime.
func TestRuntimeConsensusOverridesStaleCatalogueMetadata(t *testing.T) {
	catalogue := 86 * time.Minute
	actual := 97*time.Minute + 56*time.Second
	h := newHarness(t, []*fakeRelease{
		mkv("r1", 100_000, actual),
		mkv("r2", 100_000, actual+2*time.Second),
		mkv("r3", 100_000, actual-1*time.Second),
	}, []string{"r1", "r2", "r3"})
	h.catalog.titles[movieKey] = &core.Title{Key: movieKey, Type: core.TypeMovie, Title: "The Runner", Year: 2026, Runtime: catalogue}
	cat := &namesakeCatalog{fakeCatalog: h.catalog, namesakes: map[core.RatingKey]bool{movieKey: true}}
	h.m.deps.Catalog = cat
	rec := &rejectCollector{Resolver: h.resolver}
	h.m.deps.Resolver = rec

	ps := h.prepare(owner, movieKey, "c1")
	if !durationsAgree(ps.Info.Duration, actual) {
		t.Fatalf("played duration = %v, want ~%v", ps.Info.Duration, actual)
	}
	if len(rec.rejected) != 0 {
		t.Fatalf("good releases were rejected despite consensus: %v", rec.rejected)
	}
	if got := cat.updated[movieKey]; !durationsAgree(got, actual) {
		t.Fatalf("stored runtime updated to %v, want ~%v", got, actual)
	}
}

// TestRuntimeConsensusTwoCandidateFallback verifies that when only two
// independent candidates exist and both agree on the true duration, exhausting
// candidates adopts the held fallback rather than failing playback.
func TestRuntimeConsensusTwoCandidateFallback(t *testing.T) {
	catalogue := 86 * time.Minute
	actual := 97*time.Minute + 56*time.Second
	h := newHarness(t, []*fakeRelease{
		mkv("r1", 100_000, actual),
		mkv("r2", 100_000, actual),
	}, []string{"r1", "r2"})
	h.catalog.titles[movieKey] = &core.Title{Key: movieKey, Type: core.TypeMovie, Title: "The Runner", Year: 2026, Runtime: catalogue}
	cat := &namesakeCatalog{fakeCatalog: h.catalog, namesakes: map[core.RatingKey]bool{movieKey: true}}
	h.m.deps.Catalog = cat
	rec := &rejectCollector{Resolver: h.resolver}
	h.m.deps.Resolver = rec

	ps := h.prepare(owner, movieKey, "c1")
	if ps.Info.Duration != actual {
		t.Fatalf("played duration = %v, want %v", ps.Info.Duration, actual)
	}
	if len(rec.rejected) != 0 {
		t.Fatalf("unexpected rejections: %v", rec.rejected)
	}
	if got := cat.updated[movieKey]; got != actual {
		t.Fatalf("stored runtime = %v, want %v", got, actual)
	}
}

// TestRuntimeConsensusIgnoresRepostsAndTrailers ensures that reposts of one
// wrong file and short trailers/samples can never manufacture consensus.
func TestRuntimeConsensusIgnoresRepostsAndTrailers(t *testing.T) {
	t.Run("reposts of one wrong file cannot form consensus", func(t *testing.T) {
		catalogue := 98 * time.Minute
		wrong := 85 * time.Minute
		h := newHarness(t, []*fakeRelease{
			mkv("rep1", 100_000, wrong),
			mkv("rep2", 100_000, wrong),
			mkv("rep3", 100_000, wrong),
			mkv("right", 100_000, catalogue),
		}, []string{"rep1", "rep2", "rep3", "right"})
		for i := 0; i < 3; i++ {
			h.resolver.candidates[i].InfoHash = "deadbeef01"
		}
		h.catalog.titles[movieKey] = &core.Title{Key: movieKey, Type: core.TypeMovie, Title: "Runner", Year: 2026, Runtime: catalogue}
		cat := &namesakeCatalog{fakeCatalog: h.catalog, namesakes: map[core.RatingKey]bool{movieKey: true}}
		h.m.deps.Catalog = cat
		rec := &rejectCollector{Resolver: h.resolver}
		h.m.deps.Resolver = rec

		ps := h.prepare(owner, movieKey, "c1")
		if ps.Info.Duration != catalogue {
			t.Fatalf("played duration = %v, want %v", ps.Info.Duration, catalogue)
		}
		if len(rec.rejected) != 3 {
			t.Fatalf("rejected = %v, want all 3 wrong reposts", rec.rejected)
		}
		if _, ok := cat.updated[movieKey]; ok {
			t.Fatalf("repost unexpectedly overwrote catalogue runtime: %v", cat.updated[movieKey])
		}
	})

	t.Run("trailers never form consensus", func(t *testing.T) {
		catalogue := 86 * time.Minute
		trailer := 2 * time.Minute
		h := newHarness(t, []*fakeRelease{
			mkv("t1", 100_000, trailer),
			mkv("t2", 100_000, trailer),
			mkv("t3", 100_000, trailer),
			mkv("real", 100_000, catalogue),
		}, []string{"t1", "t2", "t3", "real"})
		h.catalog.titles[movieKey] = &core.Title{Key: movieKey, Type: core.TypeMovie, Title: "The Runner", Year: 2026, Runtime: catalogue}
		cat := &namesakeCatalog{fakeCatalog: h.catalog, namesakes: map[core.RatingKey]bool{movieKey: true}}
		h.m.deps.Catalog = cat

		ps := h.prepare(owner, movieKey, "c1")
		if ps.Info.Duration != catalogue {
			t.Fatalf("played duration = %v, want %v", ps.Info.Duration, catalogue)
		}
		if _, ok := cat.updated[movieKey]; ok {
			t.Fatalf("trailers unexpectedly overwrote catalogue runtime: %v", cat.updated[movieKey])
		}
	})
}
