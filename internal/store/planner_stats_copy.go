package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Analysing a large table off the writer.
//
// ANALYZE holds SQLite's write lock (and this process's write gate) while it
// scans the index it analyses: about two seconds for the 1.4M-row
// catalog_credits index on a loaded host, during which every playback and
// session write queues. Its result depends only on the rows in the table, so
// a large table is analysed in a private scratch database instead: its rows
// are copied there through a read-only connection, ANALYZE runs on the copy,
// and the statistics rows it produced (sqlite_stat1 and sqlite_stat4) are
// installed in one short write. The scratch table is created from the
// table's own CREATE statements, so its indexes, their collations and its
// automatic indexes carry the same names and order the same rows: the
// statistics are the ones ANALYZE would have written in place.

// analyzeCopyRows is the row count from which a table is analysed in a
// scratch copy rather than in place. In place, ANALYZE of one index costs up
// to about 2µs per row under load, so below this a statement holds the
// writer for well under 50 ms.
var analyzeCopyRows int64 = 20_000

// errNotCopyable marks a table the scratch copy cannot reproduce (a virtual
// table); it is analysed in place.
var errNotCopyable = errors.New("store: table cannot be analysed in a copy")

// analyzeScratchMaxAge is how old a scratch database left by a crashed pass
// must be before a later pass removes it.
const analyzeScratchMaxAge = time.Hour

// analyzeTable gives one table complete statistics: in a scratch copy when
// it is large and the database is a file, in place otherwise. It returns the
// longest write statement and the number of statements that changed
// statistics.
func analyzeTable(ctx context.Context, conn *sql.Conn, name string) (time.Duration, int, error) {
	path, large, err := copyCandidate(ctx, conn, name)
	if err != nil {
		return 0, 0, err
	}
	if !large {
		return analyzeFull(ctx, conn, name)
	}
	took, err := analyzeCopy(ctx, conn, path, name)
	if err == nil {
		return took, 1, nil
	}
	if ctx.Err() != nil {
		return 0, 0, ctx.Err()
	}
	if !errors.Is(err, errNotCopyable) {
		slog.Warn("store: planner statistics copy failed; analysing in place", "table", name, "err", err)
	}
	return analyzeFull(ctx, conn, name)
}

// copyCandidate reports the database file and whether name is a table large
// enough to analyse in a copy. The count is a read: it never waits for or
// holds the writer.
func copyCandidate(ctx context.Context, conn *sql.Conn, name string) (string, bool, error) {
	var path string
	if err := conn.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		return "", false, err
	}
	if path == "" {
		return "", false, nil
	}
	var isTable int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name=?`, name).Scan(&isTable); err != nil {
		return "", false, err
	}
	if isTable == 0 {
		return "", false, nil
	}
	var rows int64
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT 1 FROM `+quoteIdent(name)+` LIMIT ?)`, analyzeCopyRows).Scan(&rows); err != nil {
		return "", false, err
	}
	return path, rows >= analyzeCopyRows, nil
}

// statRows is what ANALYZE recorded for one table.
type statRows struct {
	stat1 [][2]any // idx, stat
	stat4 [][5]any // idx, neq, nlt, ndlt, sample
}

// analyzeCopy analyses table in a scratch copy of its rows and installs the
// statistics in one write on conn, returning how long that write took.
func analyzeCopy(ctx context.Context, conn *sql.Conn, path, table string) (time.Duration, error) {
	schema, err := copySchema(ctx, conn, table)
	if err != nil {
		return 0, err
	}
	stats, err := scratchAnalyze(ctx, path, table, schema)
	if err != nil {
		return 0, err
	}
	return installStats(ctx, conn, table, stats)
}

// tableSchema is what the scratch database needs to rebuild one table.
type tableSchema struct {
	create  string   // CREATE TABLE
	indexes []string // CREATE INDEX, in sqlite_schema order
	columns []string // stored, insertable columns, quoted
	rowid   bool     // copy the rowid explicitly (no INTEGER PRIMARY KEY alias)
}

func copySchema(ctx context.Context, conn *sql.Conn, table string) (tableSchema, error) {
	var s tableSchema
	rows, err := conn.QueryContext(ctx, `SELECT type, sql FROM sqlite_schema WHERE tbl_name=? AND type IN ('table','index') AND sql IS NOT NULL ORDER BY type='index', rowid`, table)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var typ, stmt string
		if err := rows.Scan(&typ, &stmt); err != nil {
			rows.Close()
			return s, err
		}
		if typ == "table" {
			s.create = stmt
		} else {
			s.indexes = append(s.indexes, stmt)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return s, err
	}
	if !strings.HasPrefix(s.create, "CREATE TABLE ") {
		return s, errNotCopyable
	}
	var withoutRowid int
	if err := conn.QueryRowContext(ctx, `SELECT wr FROM pragma_table_list WHERE schema='main' AND name=?`, table).Scan(&withoutRowid); err != nil {
		return s, err
	}
	cols, err := conn.QueryContext(ctx, `SELECT name, type, pk, hidden FROM pragma_table_xinfo(?)`, table)
	if err != nil {
		return s, err
	}
	var pks int
	var integerPK bool
	for cols.Next() {
		var name, typ string
		var pk, hidden int
		if err := cols.Scan(&name, &typ, &pk, &hidden); err != nil {
			cols.Close()
			return s, err
		}
		if hidden == 1 {
			cols.Close()
			return s, errNotCopyable
		}
		if pk > 0 {
			pks++
			integerPK = strings.EqualFold(typ, "INTEGER")
		}
		if hidden == 0 {
			s.columns = append(s.columns, quoteIdent(name))
		}
	}
	cols.Close()
	if err := cols.Err(); err != nil {
		return s, err
	}
	// An INTEGER PRIMARY KEY column is the rowid; otherwise the rowid is
	// copied so the index records sqlite_stat4 samples hold match.
	s.rowid = withoutRowid == 0 && !(pks == 1 && integerPK)
	return s, nil
}

// scratchAnalyze copies table's rows into a new scratch database beside path,
// analyses it there and returns the statistics rows. The source is attached
// read-only: the copy never takes the database's write lock.
func scratchAnalyze(ctx context.Context, path, table string, s tableSchema) (statRows, error) {
	var out statRows
	dir, base := filepath.Dir(path), filepath.Base(path)
	sweepAnalyzeScratch(dir, base, time.Now())
	f, err := os.CreateTemp(dir, base+".analyze-*")
	if err != nil {
		return out, err
	}
	scratch := f.Name()
	f.Close()
	defer func() {
		for _, p := range []string{scratch, scratch + "-journal"} {
			_ = os.Remove(p)
		}
	}()
	sdb, err := sql.Open("sqlite", sqliteFileURI(scratch)+"?_pragma=journal_mode(OFF)&_pragma=synchronous(OFF)&_pragma=foreign_keys(0)")
	if err != nil {
		return out, err
	}
	defer sdb.Close()
	sdb.SetMaxOpenConns(1)
	c, err := sdb.Conn(ctx)
	if err != nil {
		return out, err
	}
	defer c.Close()
	cols := strings.Join(s.columns, ",")
	if s.rowid {
		cols = "rowid," + cols
	}
	steps := []string{
		`ATTACH '` + strings.ReplaceAll(sqliteFileURI(path)+"?mode=ro", "'", "''") + `' AS src`,
		`PRAGMA ignore_check_constraints=1`,
		`PRAGMA analysis_limit=0`,
		s.create,
		`INSERT INTO main.` + quoteIdent(table) + `(` + cols + `) SELECT ` + cols + ` FROM src.` + quoteIdent(table),
		`DETACH src`,
	}
	steps = append(steps, s.indexes...)
	steps = append(steps, `ANALYZE main.`+quoteIdent(table))
	for _, q := range steps {
		if _, err := c.ExecContext(ctx, q); err != nil {
			return out, fmt.Errorf("%.60s: %w", q, err)
		}
	}
	rows, err := c.QueryContext(ctx, `SELECT idx, stat FROM sqlite_stat1 WHERE tbl=?`, table)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r [2]any
		if err := rows.Scan(&r[0], &r[1]); err != nil {
			rows.Close()
			return out, err
		}
		out.stat1 = append(out.stat1, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	var stat4 int
	if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name='sqlite_stat4'`).Scan(&stat4); err != nil || stat4 == 0 {
		return out, err
	}
	rows, err = c.QueryContext(ctx, `SELECT idx, neq, nlt, ndlt, sample FROM sqlite_stat4 WHERE tbl=?`, table)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r [5]any
		if err := rows.Scan(&r[0], &r[1], &r[2], &r[3], &r[4]); err != nil {
			rows.Close()
			return out, err
		}
		out.stat4 = append(out.stat4, r)
	}
	rows.Close()
	return out, rows.Err()
}

// sweepAnalyzeScratch removes scratch databases a crashed pass left behind.
func sweepAnalyzeScratch(dir, base string, now time.Time) {
	matches, _ := filepath.Glob(filepath.Join(dir, base+".analyze-*"))
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && now.Sub(fi.ModTime()) > analyzeScratchMaxAge {
			_ = os.Remove(m)
		}
	}
}

// installStats replaces table's statistics rows with stats in one write
// transaction: a delete and a few dozen inserts.
func installStats(ctx context.Context, conn *sql.Conn, table string, stats statRows) (time.Duration, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	began := time.Now()
	defer tx.Rollback()
	var has1, has4 int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE name='sqlite_stat1'), COUNT(*) FILTER (WHERE name='sqlite_stat4') FROM sqlite_schema WHERE name IN ('sqlite_stat1','sqlite_stat4')`).Scan(&has1, &has4); err != nil {
		return 0, err
	}
	if has1 == 0 || has4 == 0 && len(stats.stat4) > 0 {
		// ANALYZE of the index-less schema table creates the statistics
		// tables.
		if _, err := tx.ExecContext(ctx, `ANALYZE sqlite_schema`); err != nil {
			return 0, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name='sqlite_stat4'`).Scan(&has4); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sqlite_stat1 WHERE tbl=?`, table); err != nil {
		return 0, err
	}
	for _, r := range stats.stat1 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sqlite_stat1(tbl, idx, stat) VALUES(?,?,?)`, table, r[0], r[1]); err != nil {
			return 0, err
		}
	}
	if has4 > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sqlite_stat4 WHERE tbl=?`, table); err != nil {
			return 0, err
		}
		for _, r := range stats.stat4 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO sqlite_stat4(tbl, idx, neq, nlt, ndlt, sample) VALUES(?,?,?,?,?,?)`, table, r[0], r[1], r[2], r[3], r[4]); err != nil {
				return 0, err
			}
		}
	}
	// Other connections load the new rows with the schema change that
	// reloadStatistics makes; this one made that change itself and would
	// keep what it loaded before. ANALYZE of the index-less schema table
	// reloads them here, at the cost of counting the schema.
	if _, err := tx.ExecContext(ctx, `ANALYZE sqlite_schema`); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return time.Since(began), nil
}
