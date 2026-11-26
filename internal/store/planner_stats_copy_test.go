package store

import (
	"context"
	"database/sql"

	"reflect"
	"testing"
)

const copyStatsFixture = `
CREATE TABLE st_wr(k INTEGER NOT NULL, p INTEGER NOT NULL, kind TEXT NOT NULL DEFAULT 'cast', role TEXT NOT NULL DEFAULT '', PRIMARY KEY(k, p, kind, role)) WITHOUT ROWID;
CREATE INDEX st_wr_pk ON st_wr(p, kind, k);
CREATE TABLE st_rowid(name TEXT NOT NULL COLLATE NOCASE, v INT, g INT GENERATED ALWAYS AS (v % 3) VIRTUAL, UNIQUE(name, v));
CREATE INDEX st_rowid_lower ON st_rowid(lower(name));
CREATE INDEX st_rowid_partial ON st_rowid(v) WHERE v % 2 = 0;
CREATE INDEX st_rowid_g ON st_rowid(g DESC);
CREATE TABLE st_alias(id INTEGER PRIMARY KEY, a INT, b TEXT);
CREATE INDEX st_alias_ab ON st_alias(a, b);
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<3000)
INSERT INTO st_wr SELECT i % 400, i % 97, CASE i % 3 WHEN 0 THEN 'cast' ELSE 'crew' END, 'r' || (i % 5) FROM n;
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<3000)
INSERT INTO st_rowid(rowid, name, v) SELECT i * 7, CASE i % 2 WHEN 0 THEN 'Name' ELSE 'NAME' END || (i % 300), i FROM n;
DELETE FROM st_rowid WHERE rowid % 5 = 0;
WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<3000)
INSERT INTO st_alias SELECT i * 3, i % 11, 'b' || (i % 17) FROM n;
`

func statsOf(t *testing.T, db *sql.DB) (stat1, stat4 []string) {
	t.Helper()
	for _, q := range []struct {
		sql string
		out *[]string
	}{
		{`SELECT tbl || '|' || COALESCE(idx, '') || '|' || stat FROM sqlite_stat1 WHERE tbl LIKE 'st_%' ORDER BY 1`, &stat1},
		{`SELECT tbl || '|' || idx || '|' || neq || '|' || nlt || '|' || ndlt || '|' || hex(sample) FROM sqlite_stat4 WHERE tbl LIKE 'st_%' ORDER BY 1`, &stat4},
	} {
		rows, err := db.Query(q.sql)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			*q.out = append(*q.out, s)
		}
		rows.Close()
	}
	return stat1, stat4
}

// A large table is analysed in a scratch copy so ANALYZE never holds the
// database's writer for its scan. The statistics installed must be exactly
// those ANALYZE writes in place (WITHOUT ROWID keys, explicit and alias
// rowids, collations, expression, partial and generated-column indexes),
// and a WITHOUT ROWID primary key gets statistics too, so the next pass
// finds nothing stale.
func TestAnalyzeCopyMatchesInPlaceAnalyze(t *testing.T) {
	ctx := context.Background()
	defer func(n int64) { analyzeCopyRows = n }(analyzeCopyRows)
	analyzeCopyRows = 1000

	open := func() *sql.DB {
		db, err := Open(ctx, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if _, err := db.ExecContext(ctx, copyStatsFixture); err != nil {
			t.Fatal(err)
		}
		return db
	}
	want := open()
	if _, err := want.ExecContext(ctx, `PRAGMA analysis_limit=0; ANALYZE st_wr; ANALYZE st_rowid; ANALYZE st_alias`); err != nil {
		t.Fatal(err)
	}
	got := open()
	analysed, err := RefreshStatistics(ctx, got)
	if err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"st_wr", "st_rowid", "st_alias"} {
		found := false
		for _, a := range analysed {
			found = found || a == tbl
		}
		if !found {
			t.Fatalf("%s not analysed: %v", tbl, analysed)
		}
	}
	w1, w4 := statsOf(t, want)
	g1, g4 := statsOf(t, got)
	if !reflect.DeepEqual(w1, g1) {
		t.Fatalf("sqlite_stat1 differs\nin place: %v\ncopy:     %v", w1, g1)
	}
	if !reflect.DeepEqual(w4, g4) {
		t.Fatalf("sqlite_stat4 differs: %d in place rows, %d copied", len(w4), len(g4))
	}
	if len(g4) == 0 {
		t.Fatal("no sqlite_stat4 samples")
	}
	conn, err := got.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stale, err := staleTables(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range stale {
		if len(s) > 3 && s[:3] == "st_" {
			t.Fatalf("still stale after refresh: %v", stale)
		}
	}
	// Small tables are analysed in place, a WITHOUT ROWID key included.
	analyzeCopyRows = 1 << 40
	small := open()
	if _, err := RefreshStatistics(ctx, small); err != nil {
		t.Fatal(err)
	}
	s1, _ := statsOf(t, small)
	if !reflect.DeepEqual(w1, s1) {
		t.Fatalf("in-place sqlite_stat1 differs\nwant: %v\ngot:  %v", w1, s1)
	}
}
