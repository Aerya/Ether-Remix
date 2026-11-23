package store

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// ANALYZE reloads statistics only on its own connection. A pooled
// connection that read the schema before must plan with the new
// statistics once they are refreshed, not with the ones it loaded.
func TestRefreshedStatisticsReachEveryPooledConnection(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE stats_t(id INTEGER PRIMARY KEY, type INT, show INT, x INT);
		CREATE INDEX stats_t_type ON stats_t(type, x); CREATE INDEX stats_t_show ON stats_t(show);
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<40000) INSERT INTO stats_t SELECT i, i%4, i/10, i FROM n`); err != nil {
		t.Fatal(err)
	}
	held, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	plans := 0
	plan := func() string {
		t.Helper()
		plans++
		// A statement that reads starts a transaction, which is when a
		// connection notices a schema change; EXPLAIN alone does not, and
		// a cached EXPLAIN keeps its plan, so each one is new text.
		var one int
		if err := held.QueryRowContext(ctx, `SELECT 1 FROM stats_t LIMIT 1`).Scan(&one); err != nil {
			t.Fatal(err)
		}
		var id, parent, unused int
		var detail string
		if err := held.QueryRowContext(ctx, `EXPLAIN QUERY PLAN SELECT * FROM stats_t WHERE type=1 AND show IN (1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20) ORDER BY x -- `+strconv.Itoa(plans)).Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		return detail
	}
	if p := plan(); p != "SEARCH stats_t USING INDEX stats_t_type (type=?)" {
		t.Fatalf("without statistics: %s", p)
	}
	if err := Maintain(ctx, db); err != nil {
		t.Fatal(err)
	}
	if p := plan(); p != "SEARCH stats_t USING INDEX stats_t_show (show=?)" {
		t.Fatalf("held connection still plans without the new statistics: %s", p)
	}
}

func TestAnalyzeFullIgnoresAnalysisLimitAndUpdatesPooledConnections(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE stats_libs(id INTEGER PRIMARY KEY, lib_id INT NOT NULL, tag INT NOT NULL, title TEXT NOT NULL);
		CREATE INDEX stats_libs_lib ON stats_libs(lib_id, title);
		CREATE INDEX stats_libs_tag ON stats_libs(tag);
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<1000)
		INSERT INTO stats_libs SELECT i, 1, i%100, 'item ' || i FROM n;
	`); err != nil {
		t.Fatal(err)
	}

	parseStat := func(tbl, idx string) (total, firstCol int) {
		t.Helper()
		var stat string
		if err := db.QueryRowContext(ctx, `SELECT stat FROM sqlite_stat1 WHERE tbl=? AND idx=?`, tbl, idx).Scan(&stat); err != nil {
			t.Fatalf("read sqlite_stat1(%s, %s): %v", tbl, idx, err)
		}
		fields := strings.Fields(stat)
		if len(fields) < 2 {
			t.Fatalf("unexpected sqlite_stat1(%s, %s) = %q", tbl, idx, stat)
		}
		var err error
		if total, err = strconv.Atoi(fields[0]); err != nil {
			t.Fatalf("parse total in %q: %v", stat, err)
		}
		if firstCol, err = strconv.Atoi(fields[1]); err != nil {
			t.Fatalf("parse leading selectivity in %q: %v", stat, err)
		}
		return total, firstCol
	}

	// Verify withFullAnalysis temporarily clears a non-zero analysis_limit so
	// ANALYZE reads every row, then restores the caller's previous limit.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA analysis_limit=50`); err != nil {
		t.Fatal(err)
	}
	if err := withFullAnalysis(ctx, conn, func() error {
		_, _, err := analyzeFull(ctx, conn, "stats_libs")
		return err
	}); err != nil {
		t.Fatalf("withFullAnalysis: %v", err)
	}
	var restoredLimit int
	if err := conn.QueryRowContext(ctx, `PRAGMA analysis_limit`).Scan(&restoredLimit); err != nil {
		t.Fatal(err)
	}
	if restoredLimit != 50 {
		t.Fatalf("restored analysis_limit = %d, want 50", restoredLimit)
	}
	total1, perLib1 := parseStat("stats_libs", "stats_libs_lib")
	if total1 < 1000 || perLib1 < 1000 {
		t.Fatalf("unsampled analyzeFull with 1 lib gave total=%d perLib=%d, want >= 1000", total1, perLib1)
	}

	// Hold a pooled connection open before library 2 is added and analyzed so
	// we prove reloadStatistics pushes the new statistics to existing conns.
	held, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	plans := 0
	plan := func() string {
		t.Helper()
		plans++
		var one int
		if err := held.QueryRowContext(ctx, `SELECT 1 FROM stats_libs LIMIT 1`).Scan(&one); err != nil {
			t.Fatal(err)
		}
		var id, parent, unused int
		var detail string
		if err := held.QueryRowContext(ctx,
			`EXPLAIN QUERY PLAN SELECT * FROM stats_libs WHERE lib_id=2 AND tag=1 ORDER BY title -- `+strconv.Itoa(plans),
		).Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		return detail
	}

	// Add a second library (2x growth, well under PRAGMA optimize's 10x rule).
	if _, err := db.ExecContext(ctx, `
		WITH RECURSIVE n(i) AS (SELECT 1001 UNION ALL SELECT i+1 FROM n WHERE i<2000)
		INSERT INTO stats_libs SELECT i, 2, i%100, 'item ' || i FROM n;
	`); err != nil {
		t.Fatal(err)
	}
	if p := plan(); p != "SEARCH stats_libs USING INDEX stats_libs_lib (lib_id=?)" {
		t.Fatalf("before AnalyzeFull on unseen lib_id=2: got %q, want lib_id index scan", p)
	}

	if err := AnalyzeFull(ctx, db, "stats_libs"); err != nil {
		t.Fatalf("AnalyzeFull: %v", err)
	}
	total2, perLib2 := parseStat("stats_libs", "stats_libs_lib")
	if total2 <= total1 || perLib2 >= total2 {
		t.Fatalf("after AnalyzeFull with 2 libs: total=%d perLib=%d (was total=%d)", total2, perLib2, total1)
	}
	if p := plan(); p != "SEARCH stats_libs USING INDEX stats_libs_tag (tag=?)" {
		t.Fatalf("held connection after AnalyzeFull: got %q, want tag index scan", p)
	}

	// An already-canceled context returns promptly without mutating statistics.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := AnalyzeFull(cctx, db, "stats_libs"); err == nil {
		t.Fatal("AnalyzeFull with canceled context succeeded, want error")
	}
}
