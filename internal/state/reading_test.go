package state

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func openReadingStateDB(t *testing.T, clock *time.Time) *State {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return New(db, Options{Now: func() time.Time { return *clock }})
}

func TestReadingMigrationPostgresDDL(t *testing.T) {
	const ddl = `ALTER TABLE state_watch ADD COLUMN reading_locator TEXT NOT NULL DEFAULT '';
ALTER TABLE state_watch ADD COLUMN reading_fraction REAL NOT NULL DEFAULT 0;
ALTER TABLE state_watch ADD COLUMN reading_label TEXT NOT NULL DEFAULT '';
ALTER TABLE state_watch ADD COLUMN reading_observed_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE state_watch ADD COLUMN reading_updated_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX state_watch_reading_inprogress ON state_watch(user_id, last_viewed_at DESC, updated_at DESC) WHERE reading_locator <> '' AND reading_fraction > 0 AND reading_fraction < 0.98;`
	translated := store.TranslateDDL(ddl)
	if translated == "" {
		t.Fatal("empty translated DDL")
	}
}

func TestSetReadingProgressOrderingStickyCompletionAndUnscrobble(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	st := openReadingStateDB(t, &now)
	bookKey := core.PluginKey(42)

	// Invalid inputs rejected.
	for _, bad := range []core.ReadingProgress{
		{Locator: "", Fraction: 0.5},
		{Locator: "cfi(/6/2)", Fraction: -0.1},
		{Locator: "cfi(/6/2)", Fraction: 1.1},
		{Locator: "cfi(/6/2)", Fraction: math.NaN()},
		{Locator: "cfi(/6/2)", Fraction: math.Inf(1)},
	} {
		if err := st.SetReadingProgress(ctx, 1, bookKey, bad); !errors.Is(err, ErrInvalidReadingProgress) {
			t.Fatalf("SetReadingProgress(%+v) = %v, want ErrInvalidReadingProgress", bad, err)
		}
	}

	// Write with observedAt = now -> stored and enters InProgress.
	if err := st.SetReadingProgress(ctx, 1, bookKey, core.ReadingProgress{
		Locator:    "epubcfi(/6/4!/4/2)",
		Fraction:   0.42,
		Label:      "Chapter 4",
		ObservedAt: now,
	}); err != nil {
		t.Fatalf("SetReadingProgress initial: %v", err)
	}
	got, err := st.Get(ctx, 1, bookKey)
	if err != nil {
		t.Fatal(err)
	}
	if got.Reading == nil || got.Reading.Locator != "epubcfi(/6/4!/4/2)" || got.Reading.Fraction != 0.42 || got.Reading.Label != "Chapter 4" {
		t.Fatalf("Get Reading = %+v", got.Reading)
	}
	if !IsInProgress(got) || got.ViewCount != 0 {
		t.Fatalf("in-progress book status = %+v", got)
	}
	inProg, err := st.InProgress(ctx, 1, 10)
	if err != nil || len(inProg) != 1 || inProg[0].Key != bookKey || inProg[0].Reading == nil {
		t.Fatalf("InProgress = %+v, err=%v", inProg, err)
	}
	anyKeys, err := st.InProgressAny(ctx)
	if err != nil || len(anyKeys) != 1 || anyKeys[0] != bookKey {
		t.Fatalf("InProgressAny = %+v, err=%v", anyKeys, err)
	}

	// Stale observedAt (before stored observedAt) is ignored.
	if err := st.SetReadingProgress(ctx, 1, bookKey, core.ReadingProgress{
		Locator:    "epubcfi(/6/2!/4/1)",
		Fraction:   0.10,
		ObservedAt: now.Add(-1 * time.Minute),
	}); err != nil {
		t.Fatalf("SetReadingProgress stale: %v", err)
	}
	got, _ = st.Get(ctx, 1, bookKey)
	if got.Reading.Locator != "epubcfi(/6/4!/4/2)" || got.Reading.Fraction != 0.42 {
		t.Fatalf("stale write overwrote reading: %+v", got.Reading)
	}

	// Far-future observedAt is clamped to now + 5m so a skewed clock cannot pin progress forever.
	if err := st.SetReadingProgress(ctx, 1, bookKey, core.ReadingProgress{
		Locator:    "epubcfi(/6/6!/4/1)",
		Fraction:   0.55,
		ObservedAt: now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("SetReadingProgress skewed: %v", err)
	}
	got, _ = st.Get(ctx, 1, bookKey)
	wantClamped := now.Add(ReadingObservedMaxSkew)
	if !got.Reading.ObservedAt.Equal(wantClamped) {
		t.Fatalf("clamped ObservedAt = %v, want %v", got.Reading.ObservedAt, wantClamped)
	}

	// A write without observedAt wins on arrival even if previous was clamped ahead.
	now = now.Add(10 * time.Second)
	if err := st.SetReadingProgress(ctx, 1, bookKey, core.ReadingProgress{
		Locator:  "epubcfi(/6/8!/4/1)",
		Fraction: 0.70,
	}); err != nil {
		t.Fatalf("SetReadingProgress no observedAt: %v", err)
	}
	got, _ = st.Get(ctx, 1, bookKey)
	if got.Reading.Locator != "epubcfi(/6/8!/4/1)" || got.Reading.Fraction != 0.70 {
		t.Fatalf("arrival write did not win: %+v", got.Reading)
	}

	// Fraction >= 0.98 marks played (ViewCount == 1) and leaves InProgress.
	now = now.Add(10 * time.Second)
	if err := st.SetReadingProgress(ctx, 1, bookKey, core.ReadingProgress{
		Locator:  "epubcfi(/6/20!/4/9)",
		Fraction: 0.98,
		Label:    "Epilogue",
	}); err != nil {
		t.Fatalf("SetReadingProgress complete: %v", err)
	}
	got, _ = st.Get(ctx, 1, bookKey)
	if got.ViewCount != 1 || !IsWatched(got) || IsInProgress(got) {
		t.Fatalf("completed book status = %+v", got)
	}
	inProg, _ = st.InProgress(ctx, 1, 10)
	if len(inProg) != 0 {
		t.Fatalf("completed book still in InProgress: %+v", inProg)
	}

	// Sticky completion: turning back to an earlier page updates locator/fraction
	// without clearing ViewCount, and reaching >=0.98 again doesn't double-count.
	now = now.Add(10 * time.Second)
	if err := st.SetReadingProgress(ctx, 1, bookKey, core.ReadingProgress{
		Locator:  "epubcfi(/6/10!/4/1)",
		Fraction: 0.50,
	}); err != nil {
		t.Fatalf("SetReadingProgress revisit: %v", err)
	}
	got, _ = st.Get(ctx, 1, bookKey)
	if got.ViewCount != 1 || got.Reading.Fraction != 0.50 {
		t.Fatalf("sticky completion broken: %+v", got)
	}
	now = now.Add(10 * time.Second)
	if err := st.SetReadingProgress(ctx, 1, bookKey, core.ReadingProgress{
		Locator:  "epubcfi(/6/20!/4/10)",
		Fraction: 1.0,
	}); err != nil {
		t.Fatalf("SetReadingProgress re-finish: %v", err)
	}
	got, _ = st.Get(ctx, 1, bookKey)
	if got.ViewCount != 1 {
		t.Fatalf("ViewCount inflated to %d, want 1", got.ViewCount)
	}

	// Unscrobble clears both ViewCount and Reading.
	if err := st.Unscrobble(ctx, 1, bookKey); err != nil {
		t.Fatalf("Unscrobble: %v", err)
	}
	got, _ = st.Get(ctx, 1, bookKey)
	if got.ViewCount != 0 || got.Reading != nil || IsWatched(got) || IsInProgress(got) {
		t.Fatalf("post-Unscrobble status = %+v, want cleared", got)
	}
}
