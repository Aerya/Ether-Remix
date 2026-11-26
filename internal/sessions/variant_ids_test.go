package sessions

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestVariantRegistryPointLookups(t *testing.T) {
	db := store.OpenTest(t)
	key := core.MovieKey(603)
	first := NewVariantIDs(db, time.Now)
	wanted := first.IDFor(key, "selected")
	for i := int64(1); i <= 8; i++ {
		first.IDFor(core.MovieKey(i), "unrelated")
	}
	restarted := NewVariantIDs(db, time.Now)
	if len(restarted.byID) != 0 {
		t.Fatal("eager registry load")
	}
	ref, ok := restarted.Lookup(wanted)
	if !ok || ref.Key != key || ref.ReleaseID != "selected" {
		t.Fatalf("persisted lookup: %+v %v", ref, ok)
	}
	if len(restarted.byID) != 1 {
		t.Fatalf("lookup loaded %d mappings, want one", len(restarted.byID))
	}
	if id := restarted.IDFor(key, "selected"); id != wanted {
		t.Fatal("restart changed id")
	}
	// Occupied hash values are checked using a point read too.
	collision := VariantHash(key, "collision")
	if _, err := db.Exec(`INSERT INTO variants_ids(media_id,rating_key,release_id,last_seen) VALUES(?,?,?,?)`, collision, int64(core.MovieKey(99)), "occupied", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if got := restarted.IDFor(key, "collision"); got == collision {
		t.Fatal("persisted collision was overwritten")
	}
	if len(restarted.byID) > 3 {
		t.Fatal("collision scanned registry")
	}
}

// A cache generation may end between a touch and its flush, or between
// a client receiving an ID and pruning protecting that client's live pair.
func TestVariantRegistryRetentionAcrossEviction(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Now()
	r := NewVariantIDs(db, func() time.Time { return now })
	touched := VariantRef{Key: core.MovieKey(1), ReleaseID: "touched"}
	live := VariantRef{Key: core.MovieKey(2), ReleaseID: "live"}
	expired := VariantRef{Key: core.MovieKey(3), ReleaseID: "expired"}
	ids, err := r.IDsFor(ctx, []VariantRef{touched, live, expired})
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(variantRetention + time.Hour)
	if _, ok := r.Lookup(ids[touched]); !ok {
		t.Fatal("lookup before eviction missed")
	}
	for i := int64(0); len(r.byID) < maxVariantIDs; i++ {
		r.cacheLocked(-i-1, variantEntry{ref: VariantRef{Key: core.MovieKey(i + 4)}})
	}
	// A reverse lookup of a row outside the cache must enforce the same cap.
	miss := VariantRef{Key: core.MovieKey(4), ReleaseID: "uncached"}
	missID := NewVariantIDs(db, func() time.Time { return now }).IDFor(miss.Key, miss.ReleaseID)
	if ref, ok := r.Lookup(missID); !ok || ref != miss {
		t.Fatalf("reverse lookup across eviction = %+v, %v", ref, ok)
	}
	if len(r.byID) > maxVariantIDs || len(r.byPair) != len(r.byID) {
		t.Fatal("reverse lookup left the cache over capacity or its indexes out of sync")
	}

	if _, err := r.Prune(ctx, nil, []VariantRef{live}); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []VariantRef{touched, live} {
		if got, ok := r.Lookup(ids[ref]); !ok || got != ref {
			t.Fatalf("prune lost %v after eviction", ref)
		}
	}
	if _, ok := r.Lookup(ids[expired]); ok {
		t.Fatal("prune kept an expired, unreferenced mapping")
	}
}
