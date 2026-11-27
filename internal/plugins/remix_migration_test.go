package plugins

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestRemixPluginUpgradePreservesSettings(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	// Recreate the previously shipped Remix v12 schema with existing data.
	for _, q := range []string{
		`ALTER TABLE plugins_installed DROP COLUMN replace_libraries`,
		`DELETE FROM schema_migrations WHERE component='plugins' AND version=13`,
		`INSERT INTO plugins_installed(id,name,version,manifest,installed_at,updated_at,manifest_refresh_hours) VALUES('upgrade','Upgrade','1','{}',1,1,24)`,
		`INSERT INTO plugins_schedule_runs(plugin,call,last_run) VALUES('upgrade','sync',123000)`,
		`INSERT INTO plugins_kv(plugin,kkey,value,size) VALUES('upgrade','key',X'616263',3)`,
		`INSERT INTO plugins_kv_usage(plugin,byte_count,key_count) VALUES('upgrade',3,1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := store.Migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	st := &storage{db: db, now: time.Now}
	r, err := st.record(ctx, "upgrade")
	if err != nil {
		t.Fatal(err)
	}
	if r.ManifestRefreshHours != 24 || r.ReplaceLibraries {
		t.Fatalf("settings after upgrade: %+v", r)
	}
	r.ReplaceLibraries = true
	if err := st.upsert(ctx, r); err != nil {
		t.Fatal(err)
	}
	r, err = st.record(ctx, "upgrade")
	if err != nil || !r.ReplaceLibraries || r.ManifestRefreshHours != 24 {
		t.Fatalf("settings after update: %+v, %v", r, err)
	}
	at, err := st.scheduleLastRun(ctx, "upgrade", "sync")
	if err != nil || at.UnixMilli() != 123000 {
		t.Fatalf("schedule: %v, %v", at, err)
	}
	usage, err := st.kvUsage(ctx, "upgrade")
	if err != nil || usage.Keys != 1 || usage.Bytes != 3+KVKeyChargeBytes {
		t.Fatalf("usage: %+v, %v", usage, err)
	}
	var value []byte
	if err := db.QueryRowContext(ctx, `SELECT value FROM plugins_kv WHERE plugin='upgrade' AND kkey='key'`).Scan(&value); err != nil || string(value) != "abc" {
		t.Fatalf("KV value: %q, %v", value, err)
	}
}
