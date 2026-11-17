package collections

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
	"github.com/braedonsaunders/ether/internal/store"
	"slices"
	"testing"
)

func TestMixedMembershipChecksEachOriginAndRevocation(t *testing.T) {
	db := store.OpenTest(t)
	ctx := t.Context()
	s := &Service{db: db}
	for _, id := range []string{"local:a", "local:b"} {
		if err := mediaaccess.Create(ctx, db, id, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := mediaaccess.Update(ctx, db, "local:a", 1, mediaaccess.Policy{Viewers: []int64{2}}); err != nil {
		t.Fatal(err)
	}
	row := &specRow{id: 123, spec: core.CollectionSpec{Sources: []core.CollectionSource{{Kind: "localFiles", Label: "a"}, {Kind: "localFiles", Label: "b"}, {Kind: "titles"}}}}
	origins := [][]string{{"local:a"}, {"local:b"}, {"local:b", ""}, {""}, nil}
	keys := []core.RatingKey{}
	for i, origin := range origins {
		key := core.MovieKey(int64(i + 1))
		keys = append(keys, key)
		raw, _ := json.Marshal(origin)
		if _, err := db.ExecContext(ctx, "INSERT INTO collections_members(spec_id,pos,key,shared,origins_json) VALUES(?,?,?,?,?)", 123, i, key, 1, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	check := func(viewer int64, want []core.RatingKey) {
		t.Helper()
		got, err := s.authorizedMembers(ctx, row, keys, viewer)
		if err != nil || !slices.Equal(got, want) {
			t.Fatalf("viewer %d: %v, err=%v; want %v", viewer, got, err, want)
		}
	}
	check(1, keys)
	check(2, []core.RatingKey{keys[0], keys[2], keys[3]})
	check(3, []core.RatingKey{keys[2], keys[3]})
	if err := mediaaccess.Update(ctx, db, "local:a", 1, mediaaccess.Policy{}); err != nil {
		t.Fatal(err)
	}
	check(2, []core.RatingKey{keys[2], keys[3]})
	if _, err := db.ExecContext(ctx, "UPDATE collections_members SET origins_json='broken' WHERE key=?", keys[2]); err != nil {
		t.Fatal(err)
	}
	check(2, []core.RatingKey{keys[3]})
}
