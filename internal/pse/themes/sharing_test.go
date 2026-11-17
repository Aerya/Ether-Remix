package themes

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestLibrarySharingPersistsAndLegacyUpdatePreservesIt(t *testing.T) {
	s, _ := newTest(t)
	p := core.UserTargets{IDs: []int64{7}}
	l, err := s.Create(t.Context(), Library{Name: "Films", Type: TypeMovie, Collections: []string{"1"}, Users: &p})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	visible := s.Themes()[0].VisibleTo
	if visible(nil) || visible(&core.User{ID: 8}) || !visible(&core.User{ID: 7}) {
		t.Fatal("sharing not enforced")
	}
	l.Users = nil // old integrations omit the new field
	l.Name = "Renamed"
	if _, err := s.Update(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if s.Themes()[0].VisibleTo(&core.User{ID: 8}) {
		t.Fatal("legacy update widened sharing")
	}
	// Returned configuration must not alias the live access policy.
	copy := s.Libraries()
	copy[0].Users.IDs[0] = 8
	if s.Themes()[0].VisibleTo(&core.User{ID: 8}) {
		t.Fatal("alias widened sharing")
	}
	all := core.AllUsers()
	l.Users = &all
	if _, err := s.Update(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if !s.Themes()[0].VisibleTo(&core.User{ID: 8}) {
		t.Fatal("all-users sharing not saved")
	}
}

func TestLibrarySourcesCanBeMixedAndReused(t *testing.T) {
	s, cols := newTest(t)
	s.SetSources(func(_ context.Context) ([]SourceChoice, error) {
		return []SourceChoice{{ID: "local:x", Name: "Local", Collection: "1"}, {ID: "indexer:lumio", Name: "Lumio"}}, nil
	})
	l, err := s.Create(t.Context(), Library{Name: "Mixed", Type: TypeMovie, SourceIDs: []string{"local:x", "indexer:lumio"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Collections) != 2 || len(cols.created) != 1 {
		t.Fatalf("library=%+v created=%d", l, len(cols.created))
	}
	if cols.created[0].Sources[0].Kind != "releaseSource" {
		t.Fatal("not an observed source")
	}
	_, err = s.Create(t.Context(), Library{Name: "Second", Type: TypeShow, SourceIDs: []string{"indexer:lumio"}})
	if err != nil || len(cols.created) != 1 {
		t.Fatalf("source was duplicated: %v", err)
	}
}
