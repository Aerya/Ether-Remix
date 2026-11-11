package mediaaccess

import (
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
)

func TestPrivateSharingAndRevocation(t *testing.T) {
	db := store.OpenTest(t)
	ctx := t.Context()
	if err := Create(ctx, db, "local:family", 7); err != nil {
		t.Fatal(err)
	}
	check := func(want map[int64]bool) {
		t.Helper()
		for viewer, expected := range want {
			if got := Allowed(ctx, db, "local:family", viewer); got != expected {
				t.Fatalf("viewer %d: got %v, want %v", viewer, got, expected)
			}
		}
	}
	check(map[int64]bool{0: false, 7: true, 8: false, 9: false})
	if Allowed(ctx, db, "missing", 7) {
		t.Fatal("missing policy allowed")
	}
	if err := Update(ctx, db, "local:family", 8, Policy{SharedAll: true}); err == nil {
		t.Fatal("reader changed policy")
	}
	if err := Update(ctx, db, "local:family", 7, Policy{Viewers: []int64{8, 8}}); err != nil {
		t.Fatal(err)
	}
	check(map[int64]bool{0: false, 7: true, 8: true, 9: false})
	if err := Update(ctx, db, "local:family", 7, Policy{SharedAll: true}); err != nil {
		t.Fatal(err)
	}
	check(map[int64]bool{0: false, 7: true, 8: true, 9: true})
	// Invalid replacement must preserve the complete prior policy.
	if err := Update(ctx, db, "local:family", 7, Policy{Viewers: []int64{-1}}); err == nil {
		t.Fatal("invalid recipient accepted")
	}
	check(map[int64]bool{7: true, 8: true, 9: true})
	if err := Update(ctx, db, "local:family", 7, Policy{}); err != nil {
		t.Fatal(err)
	}
	check(map[int64]bool{7: true, 8: false, 9: false})
}
