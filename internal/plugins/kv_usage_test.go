package plugins

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// The KV quota reads a running per-plugin tally instead of summing every
// stored value inside the write; puts, overwrites, deletes and uninstall
// keep that tally equal to what the plugin actually stores.
func TestKVUsageTallyFollowsWrites(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) { testKVUsageTally(t, store.OpenTest(t)) })
	t.Run("postgres", func(t *testing.T) {
		raw := os.Getenv(store.TestPostgresEnv)
		if raw == "" {
			t.Skip("set " + store.TestPostgresEnv + " to run PostgreSQL tests")
		}
		db, err := store.OpenTestPostgres(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		testKVUsageTally(t, db)
	})
}

func testKVUsageTally(t *testing.T, db *sql.DB) {
	ctx := context.Background()
	st := &storage{db: db, now: time.Now}
	for _, p := range []string{"a", "b"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO plugins_grants(plugin,grants) VALUES(?,'{}')`, p); err != nil {
			t.Fatal(err)
		}
	}
	check := func(plugin string) {
		t.Helper()
		var bytes, keys int64
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0),COUNT(*) FROM plugins_kv WHERE plugin=?`, plugin).Scan(&bytes, &keys); err != nil {
			t.Fatal(err)
		}
		u, err := st.kvUsage(ctx, plugin)
		if err != nil {
			t.Fatal(err)
		}
		if u.Keys != keys || u.Bytes != bytes+keys*KVKeyChargeBytes {
			t.Fatalf("%s usage = %+v, stored %d bytes in %d keys", plugin, u, bytes, keys)
		}
	}
	put := func(plugin, key string, n int) {
		t.Helper()
		if err := st.kvPut(ctx, plugin, key, make([]byte, n)); err != nil {
			t.Fatal(err)
		}
	}
	put("a", "one", 100)
	put("a", "two", 50)
	put("b", "one", 7)
	put("a", "one", 10) // overwrite shrinks
	check("a")
	check("b")
	if err := st.kvDelete(ctx, "a", "two"); err != nil {
		t.Fatal(err)
	}
	if err := st.kvDelete(ctx, "a", "missing"); err != nil {
		t.Fatal(err)
	}
	check("a")
	if err := st.kvPut(ctx, "a", "huge", make([]byte, KVQuotaBytes)); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("over-quota put = %v", err)
	}
	check("a")
	if err := st.kvPut(ctx, "nogrant", "k", []byte("v")); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("put without grants = %v", err)
	}
	if err := st.purge(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	check("a")
	check("b")
}
