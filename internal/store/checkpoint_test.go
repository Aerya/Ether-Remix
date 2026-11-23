package store

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// A reader pins old frames even though PASSIVE reports busy=0. Bulk writers
// wait for it rather than fail, report a typed error only past walWaitMax,
// and wait for an announced long read before writing at all.
func TestWALBudgetWaitsForPinnedReader(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE wal_budget(v BLOB); INSERT INTO wal_budget VALUES (zeroblob(1024))`); err != nil {
		t.Fatal(err)
	}
	reader, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := reader.ExecContext(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	defer reader.ExecContext(ctx, `ROLLBACK`)
	var n int
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM wal_budget`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO wal_budget VALUES (zeroblob(1048576))`); err != nil {
		t.Fatal(err)
	}
	if over, _, err := walOverBudget(ctx, db, 64<<10); err != nil || !over {
		t.Fatalf("pinned WAL beyond budget not seen: over=%v %v", over, err)
	}
	if _, err := reader.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if over, _, err := walOverBudget(ctx, db, 64<<10); err != nil || over {
		t.Fatalf("reader released, bulk writer still paused: over=%v %v", over, err)
	}
	// An announced long read holds bulk writers until it ends, however
	// long that is (walWaitMax bounds only readers we do not know of).
	defer func(d time.Duration) { walWaitMax = d }(walWaitMax)
	walWaitMax = 50 * time.Millisecond
	end := BeginLongRead(db, "integrity check")
	var released atomic.Bool
	go func() {
		time.Sleep(150 * time.Millisecond)
		released.Store(true)
		end()
	}()
	if err := WaitWALBudget(ctx, db); err != nil {
		t.Fatalf("long read ended, bulk writer still paused: %v", err)
	}
	if !released.Load() {
		t.Fatal("bulk writer did not wait for the long read")
	}
	// A reader we do not know of is reported once walWaitMax has passed.
	if _, err := reader.ExecContext(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if err := reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM wal_budget`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO wal_budget SELECT zeroblob(1048576) FROM (SELECT 1 UNION ALL SELECT 2) x, (SELECT 1 UNION ALL SELECT 2) y, (SELECT 1 UNION ALL SELECT 2) z, (SELECT 1 UNION ALL SELECT 2) w, (SELECT 1 UNION ALL SELECT 2) v, (SELECT 1 UNION ALL SELECT 2) u, (SELECT 1 UNION ALL SELECT 2) q`); err != nil {
		t.Fatal(err)
	}
	var blocked *WALBlockedError
	if err := WaitWALBudget(ctx, db); !errors.As(err, &blocked) {
		t.Fatalf("pinned WAL past journal_size_limit not reported: %v", err)
	}
}
