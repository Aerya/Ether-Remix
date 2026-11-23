package libraries

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestDefaultLibraryLifecycle(t *testing.T) {
	_, db := openService(t)
	ctx := context.Background()
	// Simulate an upgrade from the old writer, including explicit owner edits.
	if _, err := db.Exec(`DELETE FROM libraries_state WHERE k='defaults_seeded'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE libraries SET source='builtin', name='Films', present='{"adapterOnly":true}', visibility_default='hide', rules='[]', quality_profile='Cinema' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE libraries SET source='builtin', present='{}' WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	s, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	movie, err := s.Get(ctx, 1)
	if err != nil || movie.Source != core.LibraryCatalog || movie.Name != "Films" || !movie.Present.AdapterOnly || movie.VisibilityDefault != "hide" || movie.QualityProfile != "Cinema" {
		t.Fatalf("upgrade lost owner settings: %+v %v", movie, err)
	}
	show, err := s.Get(ctx, 2)
	if err != nil || show.Present.Plex == nil || show.Present.Plex.Base != "show" || show.Present.Silo == nil {
		t.Fatalf("lost implicit publication: %+v %v", show, err)
	}
	before, _ := json.Marshal(movie)
	s, err = New(ctx, db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	movie, _ = s.Get(ctx, 1)
	after, _ := json.Marshal(movie)
	if string(before) != string(after) {
		t.Fatal("restart changed migrated row")
	}
	for _, id := range []core.LibraryID{1, 2} {
		if err := s.Delete(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	// Both the existing service's cached reads and a restart must remain empty.
	for i := 0; i < 2; i++ {
		libs, err := s.List(ctx)
		if err != nil || len(libs) != 0 {
			t.Fatalf("deleted libraries resurrected: %+v %v", libs, err)
		}
		s, err = New(ctx, db, Options{})
		if err != nil {
			t.Fatal(err)
		}
	}
	recreated, err := s.Create(ctx, "Movies", core.TypeMovie, nil, "show", core.Presentations{})
	if err != nil || recreated.ID <= 2 {
		t.Fatalf("recreate reused reserved identity: %+v %v", recreated, err)
	}
}

func TestPluginDeletionIdentitySurvivesRestartAndExplicitRestore(t *testing.T) {
	ctx := context.Background()
	s, db := openService(t)
	decl := PluginLibraryDecl{Name: "Recordings"}
	lib, err := s.UpsertPluginLibrary(ctx, "plugin.one", "recording", decl)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, lib.ID, "Owner name"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Get(ctx, lib.ID); err != core.ErrNotFound {
		t.Fatalf("published deleted library: %v", err)
	}
	if _, err := reopened.UpsertPluginLibrary(ctx, "plugin.one", "recording", decl); err != ErrDeleted {
		t.Fatalf("resurrected: %v", err)
	}
	if err := reopened.Purge(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.UpsertPluginLibrary(ctx, "plugin.one", "recording", decl); err != ErrDeleted {
		t.Fatalf("uninstall cleared owner tombstone: %v", err)
	}
	for _, pair := range [][2]string{{"plugin.two", "recording"}, {"plugin.one", "other"}} {
		if _, err := reopened.UpsertPluginLibrary(ctx, pair[0], pair[1], decl); err != nil {
			t.Fatal(err)
		}
	}
	restored, err := reopened.RestorePluginLibrary(ctx, "plugin.one", "recording", decl)
	if err != nil || restored.ID != lib.ID || restored.Key != lib.Key || restored.Name != "Owner name" {
		t.Fatalf("restore: %+v %v", restored, err)
	}
}
