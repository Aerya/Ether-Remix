package libraries

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// TestMigrateConfigNames is the §12 slice-A acceptance for the config
// side: a pre-v0.3.8 config document's library names become the
// names of libraries 1/2, exactly once.
func TestMigrateConfigNames(t *testing.T) {
	s, _ := openService(t)
	ctx := context.Background()

	legacy := []byte(`{"server":{"friendlyName":"Ether","moviesLibraryName":"Basement Films","tvShowsLibraryName":"Basement Series"}}`)
	movies, shows, err := LegacyConfigNames(legacy)
	if err != nil || movies != "Basement Films" || shows != "Basement Series" {
		t.Fatalf("LegacyConfigNames = %q, %q, %v", movies, shows, err)
	}
	if err := s.MigrateConfigNames(ctx, movies, shows); err != nil {
		t.Fatal(err)
	}
	libs, err := s.List(ctx)
	if err != nil || libs[0].Name != "Basement Films" || libs[1].Name != "Basement Series" {
		t.Fatalf("after migration = %+v, %v", libs, err)
	}
	// An owner rename after the migration is never reverted by
	// re-running it against the same stale config.
	if err := s.Rename(ctx, 1, "Owner Films"); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateConfigNames(ctx, movies, shows); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, 1)
	if err != nil || got.Name != "Owner Films" {
		t.Fatalf("owner rename reverted: %+v, %v", got, err)
	}
}

func TestMigrateConfigNamesKeepsDefaults(t *testing.T) {
	s, _ := openService(t)
	ctx := context.Background()
	movies, shows, err := LegacyConfigNames([]byte(`{"server":{}}`))
	if err != nil || movies != "" || shows != "" {
		t.Fatalf("LegacyConfigNames(empty) = %q, %q, %v", movies, shows, err)
	}
	if err := s.MigrateConfigNames(ctx, movies, shows); err != nil {
		t.Fatal(err)
	}
	libs, err := s.List(ctx)
	if err != nil || libs[0].Name != "Movies" || libs[1].Name != "TV Shows" {
		t.Fatalf("defaults lost: %+v, %v", libs, err)
	}
	if _, _, err := LegacyConfigNames([]byte(`{`)); err == nil {
		t.Fatal("malformed config accepted")
	}
}

func TestMigrateVelaPresentation(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "libraries.sqlite")

	db, err := store.OpenPath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Seed pre-migration library rows with legacy present payloads (no vela key)
	// and rewind schema_migrations for version 10 so reopen runs migration 10.
	rows := []struct {
		id      int64
		key     string
		present string
	}{
		{10, "mb-movies", `{"mediaBrowser":{"collectionType":"movies"},"plex":{"base":"show"}}`},
		{11, "mb-shows", `{"mediaBrowser":{"collectionType":"tvshows"}}`},
		{12, "plex-movie", `{"plex":{"base":"movie"}}`},
		{13, "plex-show", `{"mediaBrowser":{"collectionType":"music"},"plex":{"base":"show"}}`},
		{14, "console-only", `{}`},
		{15, "stremio-only", `{"stremio":{"type":"movie"}}`},
		{16, "adapter-only", `{"adapterOnly":true,"plex":{"base":"movie"}}`},
		{17, "already-set", `{"vela":{"type":"shows"},"plex":{"base":"movie"}}`},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO libraries(id,key,name,source,sources,media_type,present,sort,hidden,created_at,updated_at)
			 VALUES(?,?,?,'catalogue','["tmdb-movies"]','movie',?,?,0,1,1)`,
			r.id, r.key, r.key, r.present, r.id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE component='libraries' AND version=10`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = store.OpenPath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(ctx, db, Options{})
	if err != nil {
		db.Close()
		t.Fatal(err)
	}

	want := map[core.LibraryID]string{
		10: "movies", // MediaBrowser wins over Plex
		11: "shows",
		12: "movies",
		13: "shows", // MediaBrowser music ignored; falls through to Plex show
		14: "",
		15: "",
		16: "",
		17: "shows", // existing vela preserved
	}
	for id, wantType := range want {
		got, err := s.Get(ctx, id)
		if err != nil {
			db.Close()
			t.Fatalf("Get(%d): %v", id, err)
		}
		gotType := ""
		if got.Present.Vela != nil {
			gotType = got.Present.Vela.Type
		}
		if gotType != wantType {
			db.Close()
			t.Fatalf("id %d (%s): Present.Vela = %q, want %q (full present: %+v)", id, got.Key, gotType, wantType, got.Present)
		}
	}

	// Owner turns Vela off while keeping Plex on; reopening must NOT re-infer Vela.
	if err := s.SetPresent(ctx, 12, core.Presentations{
		Plex: &core.PlexPresentation{Base: "movie"},
	}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = store.OpenPath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err = New(ctx, db, Options{})
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.Get(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	if after.Present.Vela != nil {
		t.Fatalf("reopening re-populated owner-cleared vela: %+v", after.Present)
	}
}

func TestMigratePresentationOverrides(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "overrides.sqlite")

	db, err := store.OpenPath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Seed a normal stale plugin library and an adapterOnly plugin library,
	// then rewind schema_migrations for version 13 (and drop present_overrides
	// by resetting it to '{}' and re-running the migration update) to verify
	// backfill behaviour.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO libraries(id,key,name,source,sources,media_type,present,present_overrides,sort,hidden,plugin_id,kind_key,created_at,updated_at)
		 VALUES(20,'plugin:demo:clips','Studio Clips','plugin','[]','movie','{"mediaBrowser":{"collectionType":"movies"}}','{}',20,0,'demo','clips',1,1),
		       (21,'plugin:demo:private','Private Only','plugin','[]','movie','{"adapterOnly":true}','{}',21,0,'demo','private',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE libraries SET present_overrides = '{}' WHERE id IN (20, 21)`); err != nil {
		t.Fatal(err)
	}
	// Simulate pre-migration-13 DB where only versions <= 12 ran by executing
	// the migration 13 backfill statement through a fresh table without the column.
	if _, err := db.ExecContext(ctx, `ALTER TABLE libraries DROP COLUMN present_overrides`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE component='libraries' AND version=13`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = store.OpenPath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s, err := New(ctx, db, Options{})
	if err != nil {
		t.Fatal(err)
	}

	normal, err := s.Get(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !normal.PresentOverrides.IsEmpty() {
		t.Fatalf("normal plugin row got unexpected overrides: %+v", normal.PresentOverrides)
	}

	adapterOnly, err := s.Get(ctx, 21)
	if err != nil {
		t.Fatal(err)
	}
	if adapterOnly.PresentOverrides.AdapterOnly == nil || !*adapterOnly.PresentOverrides.AdapterOnly {
		t.Fatalf("adapterOnly plugin row lost sticky override: %+v", adapterOnly.PresentOverrides)
	}
}
