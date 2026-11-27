package collections_test

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// TestCheckMembersBatchParity verifies that CheckMembers and CheckMembersBatch
// enforce visibility, mixed section scoping, scanLimit, and non-member rejection
// identically to Get + Items.
func TestCheckMembersBatchParity(t *testing.T) {
	e := newEnv(t)
	owner := &core.User{ID: 1, IsOwner: true}
	other := &core.User{ID: 2}

	m1, m2 := core.MovieKey(101), core.MovieKey(102)
	s1 := core.ShowKey(501)
	missing := core.MovieKey(999999)

	ownerOnly := spec("Owner Movies", "movie", []core.CollectionSource{{Kind: "titles", Keys: []string{m1.String(), m2.String()}}})
	ownerOnly.OwnerUserID = owner.ID
	cOwner, err := e.svc.CreateSpec(e.ctx, ownerOnly)
	if err != nil {
		t.Fatal(err)
	}
	ownerKey := core.SmartKey(mustInt(t, cOwner.ID))

	mixed := spec("Shared Mixed", "mixed", []core.CollectionSource{{Kind: "titles", Keys: []string{m1.String(), s1.String(), m2.String()}}})
	cMixed, err := e.svc.CreateSpec(e.ctx, mixed)
	if err != nil {
		t.Fatal(err)
	}
	mixedKey := core.SmartKey(mustInt(t, cMixed.ID))
	mixedMovies := core.SectionCollectionKey(mixedKey, core.TypeMovie)
	mixedShows := core.SectionCollectionKey(mixedKey, core.TypeShow)

	// Owner sees both; want matching actual members passes, wrong section fails.
	cols, matched, err := e.svc.CheckMembersBatch(e.ctx, owner,
		[]core.RatingKey{ownerKey, mixedMovies, mixedShows},
		map[core.RatingKey][]core.RatingKey{
			ownerKey:    {m1, m2},
			mixedMovies: {m1},
			mixedShows:  {m1}, // wrong section: movie is not in the show section
		}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if cols[ownerKey] == nil || !matched[ownerKey] {
		t.Fatalf("owner collection: col=%v matched=%v, want visible+matched", cols[ownerKey], matched[ownerKey])
	}
	if cols[mixedMovies] == nil || !matched[mixedMovies] {
		t.Fatalf("mixed movies section: col=%v matched=%v, want visible+matched", cols[mixedMovies], matched[mixedMovies])
	}
	if cols[mixedShows] == nil || matched[mixedShows] {
		t.Fatalf("mixed shows section with movie key: col=%v matched=%v, want visible+unmatched", cols[mixedShows], matched[mixedShows])
	}

	// Collections are content: another viewer sees the Studio collection
	// too (who sees a row is the page row's audience); a missing key must
	// fail membership.
	cols2, matched2, err := e.svc.CheckMembersBatch(e.ctx, other,
		[]core.RatingKey{ownerKey, mixedKey},
		map[core.RatingKey][]core.RatingKey{
			ownerKey: {m1},
			mixedKey: {m1, missing},
		}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if cols2[ownerKey] == nil || !matched2[ownerKey] {
		t.Fatalf("another viewer lost a shared collection: col=%v matched=%v", cols2[ownerKey], matched2[ownerKey])
	}
	if cols2[mixedKey] == nil || matched2[mixedKey] {
		t.Fatalf("missing member should fail match: col=%v matched=%v", cols2[mixedKey], matched2[mixedKey])
	}

	// scanLimit=1 must reject the second member of a 2-item collection.
	_, okLimit, err := e.svc.CheckMembers(e.ctx, owner, ownerKey, []core.RatingKey{m1, m2}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if okLimit {
		t.Fatal("scanLimit=1 unexpectedly matched 2 distinct members")
	}
}
