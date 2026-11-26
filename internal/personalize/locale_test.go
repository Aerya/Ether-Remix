package personalize

import (
	"context"
	"fmt"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

// A keyword starting with a multi-byte letter must name its cluster with
// valid UTF-8, not a split first byte.
func TestThemeWordsMultiByteFirstLetter(t *testing.T) {
	for in, want := range map[string]string{
		"ménage à trois": "Ménage-À-Trois",
		"émigré":         "Émigré",
		"time travel":    "Time-Travel",
		"coming of age":  "Coming-of-Age",
	} {
		got := themeWords(in)
		if !utf8.ValidString(got) || got != want {
			t.Errorf("themeWords(%q) = %q, want %q", in, got, want)
		}
	}
}

// Year in Review counts the server's calendar year: a film finished on New
// Year's Eve evening local time (already January 1 in UTC) belongs to the
// year that is ending.
func TestYearInReviewUsesServerLocalYear(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	est := time.FixedZone("EST", -5*3600)
	nye := time.Date(2025, 12, 31, 22, 0, 0, 0, est) // 2026-01-01 03:00 UTC
	cat := newFakeCatalog(testNow)
	st := state.New(db, state.Options{Now: func() time.Time { return nye }})
	key := movieKey(0, 1)
	if err := st.Scrobble(ctx, 1, key); err != nil {
		t.Fatal(err)
	}
	d := NewDiscovery(db, cat, st, nil)
	d.now = func() time.Time { return time.Date(2026, 3, 1, 12, 0, 0, 0, est) }
	h, err := d.YearInReview(ctx, &core.User{ID: 1}, 2025, 20)
	if err != nil || len(h.Items) != 1 {
		t.Fatalf("2025 review = %d items, %v; want the New Year's Eve film", len(h.Items), err)
	}
	h, err = d.YearInReview(ctx, &core.User{ID: 1}, 2026, 20)
	if err != nil || len(h.Items) != 0 {
		t.Fatalf("2026 review = %d items, %v; want none", len(h.Items), err)
	}
}

// Year in Review reads the whole covered year however much the viewer
// watched in it or since (#799, thanks theatarr): it once read a 500-row
// newest-first page, so later plays crowded out the start of the year.
func TestYearInReviewCountsWholeBusyYear(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := newFakeCatalog(testNow)
	at := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	st := state.New(db, state.Options{Now: func() time.Time { return at }})
	for i := 0; i < 600; i++ {
		key := core.MovieKey(int64(500_000 + i))
		cat.set(core.Title{Key: key, Type: core.TypeMovie, Title: fmt.Sprint("Film ", i)})
		if err := st.Scrobble(ctx, 1, key); err != nil {
			t.Fatal(err)
		}
		at = at.Add(12 * time.Hour)
	}
	at = time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	later := movieKey(0, 1)
	for i := 0; i < 500; i++ {
		if err := st.Scrobble(ctx, 1, later); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Hour)
	}
	d := NewDiscovery(db, cat, st, nil)
	d.now = func() time.Time { return testNow }
	h, err := d.YearInReview(ctx, &core.User{ID: 1}, 2025, 10000)
	if err != nil || len(h.Items) != 600 {
		t.Fatalf("2025 review = %d items, %v; want all 600 films", len(h.Items), err)
	}
	if h.Items[0].Key != core.MovieKey(500_599) || h.Items[599].Key != core.MovieKey(500_000) {
		t.Fatalf("review order %v … %v; want latest completion first", h.Items[0].Key, h.Items[599].Key)
	}
}
