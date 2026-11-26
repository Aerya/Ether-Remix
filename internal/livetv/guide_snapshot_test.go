package livetv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// The materialized representations must preserve scheduling and policy while
// request cost depends only on the requested time slice, never the recipe.
func TestGuideSnapshotEquivalenceAndCost(t *testing.T) {
	for _, mode := range []string{"order", "marathon", "personal"} {
		for _, size := range []int{10, 500} {
			t.Run(fmt.Sprintf("%s/%d", mode, size), func(t *testing.T) {
				ctx := context.Background()
				now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
				db, err := store.OpenMemory(ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { db.Close() })
				if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS resolver_availability (rating_key INTEGER PRIMARY KEY)`); err != nil {
					t.Fatal(err)
				}
				s := testService(t, Options{DB: db, Now: func() time.Time { return now }, Picker: costPersonalPicker{size}, Availability: sqlAvailability{db}})
				c := testChannel()
				c.Items = nil
				c.Mode = mode
				for n := 0; n < size; n++ {
					c.Items = append(c.Items, Item{Key: core.MovieKey(int64(n + 1)), Title: fmt.Sprint(n), DurationMS: 3600000, Rating: "G"})
				}
				u := &core.User{ID: 1, IsOwner: true}
				if mode == "personal" {
					c.Mode = ""
					c.Personal = &PersonalRecipe{}
					c.Items = nil
				}
				if _, err := s.SaveChannel(ctx, c); err != nil {
					t.Fatal(err)
				}
				if mode == "personal" {
					if err := s.buildPersonal(ctx, c, u); err != nil {
						t.Fatal(err)
					}
				}
				until := now.Add(2 * time.Hour)
				for _, view := range []SnapshotView{SnapshotRaw, SnapshotPlayback, SnapshotGuide} {
					var want []Programme
					switch view {
					case SnapshotRaw:
						want = Schedule(c, now, until)
					case SnapshotPlayback:
						want = Schedule(s.guideChannel(ctx, c), now, until)
					case SnapshotGuide:
						want = s.broadcastGuide(s.guideChannel(ctx, c), now, until)
					}
					if mode == "personal" {
						row, err := s.readPersonal(ctx, personalKey{u.ID, c.ID})
						if err != nil {
							t.Fatal(err)
						}
						want = programmeSlice(row.programmes, now, until)
					}
					got, err := s.ProgrammesSnapshot(ctx, u, c.ID, now, until, view)
					if err != nil {
						t.Fatal(err)
					}
					a, _ := json.Marshal(got)
					b, _ := json.Marshal(want)
					if string(a) != string(b) {
						t.Fatalf("%s snapshot changed programme bytes\ngot %s\nwant %s", view, a, b)
					}
				}
				measuredBefore, beforeStats := store.WithQueryStats(ctx)
				beforeStart := time.Now()
				beforeCtx, _, _ := s.withPolicy(measuredBefore, u)
				beforeChannel, _ := s.Channel(beforeCtx, c.ID)
				if mode == "personal" {
					_, _ = s.readPersonal(beforeCtx, personalKey{u.ID, c.ID})
				} else {
					for _, p := range s.broadcastGuide(s.guideChannel(beforeCtx, beforeChannel), now, until) {
						_ = s.allowed(beforeCtx, u, beforeChannel, &p)
					}
				}
				t.Logf("legacy guide titles=%d queries=%d elapsed=%s", size, beforeStats.Queries(), time.Since(beforeStart))
				for _, kind := range []string{"guide", "on now"} {
					measured, stats := store.WithQueryStats(ctx)
					start := time.Now()
					if kind == "guide" {
						ps, err := s.GuideForChannels(measured, u, now, until, []string{c.ID})
						if err != nil || len(ps) == 0 {
							t.Fatalf("guide %d %v", len(ps), err)
						}
					} else {
						_, ps, err := s.OnNow(measured, u, now, until)
						if err != nil || len(ps) == 0 {
							t.Fatalf("on now %d %v", len(ps), err)
						}
					}
					if stats.Queries() > 5 {
						t.Fatalf("%s: %d queries", kind, stats.Queries())
					}
					for _, statement := range stats.Statements() {
						if strings.Contains(statement.SQL, "resolver_availability") {
							t.Fatal("availability queried by viewer")
						}
					}
					t.Logf("%s titles=%d queries=%d elapsed=%s", kind, size, stats.Queries(), time.Since(start))
				}
				// A channel save publishes its replacement before returning.
				if mode != "personal" {
					c.Items[0].Title = "Edited"
					if _, err := s.SaveChannel(ctx, c); err != nil {
						t.Fatal(err)
					}
					got, err := s.ProgrammesSnapshot(ctx, u, c.ID, now, until, SnapshotRaw)
					if err != nil {
						t.Fatal(err)
					}
					want := Schedule(c, now, until)
					for i := range got {
						got[i].policyTag = nil
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatal("owner save left stale snapshot")
					}
				}
			})
		}
	}
}

type costPersonalPicker struct{ size int }

func (p costPersonalPicker) Picks(context.Context, *core.User, map[string]float64, int) ([]core.PersonalPick, error) {
	out := make([]core.PersonalPick, p.size)
	for i := range out {
		out[i] = core.PersonalPick{Title: &core.Title{Key: core.MovieKey(int64(i + 1)), Type: core.TypeMovie, Title: fmt.Sprint(i), Runtime: 5 * time.Minute, ContentRating: "G"}, Kind: "picked", Score: 1}
	}
	return out, nil
}

// OnNow must return immediately with available channels instead of failing
// Home assembly when a virtual channel's guide is cold or a newly signed-in
// viewer's personal channel has not been built yet; direct cold snapshot reads
// must surface a retryable PreparingError rather than bare ErrNotAvailable.
func TestColdVirtualGuideOnNowDegradesAndPrepares(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := testService(t, Options{DB: db, Now: func() time.Time { return now }, Picker: costPersonalPicker{4}})
	u := &core.User{ID: 1, IsOwner: true}

	ready := testChannel()
	ready.ID = "ready"
	ready.Number = "101"
	ready.Items = []Item{{Key: core.MovieKey(1), Title: "Ready", DurationMS: 3600000, Rating: "G"}}
	if _, err := s.SaveChannel(ctx, ready); err != nil {
		t.Fatal(err)
	}

	cold := testChannel()
	cold.ID = "cold"
	cold.Number = "102"
	cold.Items = []Item{{Key: core.MovieKey(2), Title: "Cold", DurationMS: 3600000, Rating: "G"}}
	if _, err := s.SaveChannel(ctx, cold); err != nil {
		t.Fatal(err)
	}

	personal := testChannel()
	personal.ID = "personal"
	personal.Number = "103"
	personal.Mode = ""
	personal.Items = nil
	personal.Personal = &PersonalRecipe{}
	if _, err := s.SaveChannel(ctx, personal); err != nil {
		t.Fatal(err)
	}
	if err := s.refreshVirtualGuide(ctx); err != nil {
		t.Fatal(err)
	}

	// Simulate an idle eviction of the cold showcase channel while leaving
	// the ready channel warm, and leave the personal channel unbuilt.
	s.virtualGuide.mu.Lock()
	delete(s.virtualGuide.entries, cold.ID)
	s.virtualGuide.mu.Unlock()

	_, ps, err := s.OnNow(ctx, u, now, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("OnNow with cold virtual and unbuilt personal channels failed: %v", err)
	}
	foundReady := false
	for _, p := range ps {
		if p.ChannelID == ready.ID {
			foundReady = true
		}
	}
	if !foundReady {
		t.Fatalf("OnNow omitted warm channel %q: got %+v", ready.ID, ps)
	}

	// Even when every virtual channel has been idle-evicted, OnNow must
	// return cleanly rather than stalling or returning ErrNotAvailable.
	s.virtualGuide.mu.Lock()
	s.virtualGuide.entries = map[string]*virtualGuideSnapshot{}
	s.virtualGuide.mu.Unlock()
	if _, _, err := s.OnNow(ctx, u, now, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("OnNow with all virtual channels cold failed: %v", err)
	}

	// Direct snapshot read of an unbuilt personal channel must be retryable.
	_, err = s.ProgrammesSnapshot(ctx, u, personal.ID, now, now.Add(2*time.Hour), SnapshotGuide)
	if !errors.Is(err, core.ErrPreparing) || !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("unbuilt personal snapshot err = %v, want PreparingError wrapping ErrNotAvailable", err)
	}
	var prep *core.PreparingError
	if !errors.As(err, &prep) || prep.RetryAfter <= 0 {
		t.Fatalf("unbuilt personal snapshot err = %v, want positive RetryAfter", err)
	}
}
