package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
)

// Reopen a database carrying the published Remix migration numbers. Incoming
// migrations must still run and must not re-add the fork's existing columns.
func TestRemixUpgradeKeepsPublishedMigrations(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	exec(`DROP TABLE plugins_schedule_runs`)
	exec(`DELETE FROM schema_migrations WHERE (component='plugins' AND version=11) OR (component='collections' AND version=10)`)
	exec(`INSERT INTO plugins_installed(id,name,version,manifest,source_kind,installed_at,updated_at,manifest_refresh_hours) VALUES('upgrade-test','Upgrade','1','{}','url',1,1,24)`)
	exec(`INSERT INTO plugins_calls(plugin,route,method,status,ms,created) VALUES('upgrade-test','scheduled','GET',200,1,123)`)
	exec(`INSERT INTO collections_specs(id,spec_json,build_hash) VALUES(987654,'{}','kept')`)
	exec(`INSERT INTO collections_members(spec_id,pos,key,shared,origins_json) VALUES(987654,0,1000000000000001,1,'["local"]')`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var hours, last int64
	if err := db.QueryRowContext(ctx, `SELECT manifest_refresh_hours FROM plugins_installed WHERE id='upgrade-test'`).Scan(&hours); err != nil || hours != 24 {
		t.Fatalf("manifest interval %d: %v", hours, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT last_run FROM plugins_schedule_runs WHERE plugin='upgrade-test' AND call='scheduled'`).Scan(&last); err != nil || last != 123000 {
		t.Fatalf("schedule migration %d: %v", last, err)
	}
	var origins, hash string
	if err := db.QueryRowContext(ctx, `SELECT origins_json FROM collections_members WHERE spec_id=987654`).Scan(&origins); err != nil || origins != `["local"]` {
		t.Fatalf("origins %q: %v", origins, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT build_hash FROM collections_specs WHERE id=987654`).Scan(&hash); err != nil || hash != "kept" {
		t.Fatalf("collection %q: %v", hash, err)
	}
	var versions int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE (component='plugins' AND version IN (7,11)) OR (component='collections' AND version IN (9,10))`).Scan(&versions); err != nil || versions != 4 {
		t.Fatalf("versions %d: %v", versions, err)
	}
}
