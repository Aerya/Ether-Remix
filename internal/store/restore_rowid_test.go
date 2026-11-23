package store

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
)

// TestSQLiteReplayPreservesRowidAndInsertOrReplace pins two SQLite logical
// restore trigger-replay invariants:
//  1. A trigger writing an absent FTS5 table via INSERT OR REPLACE INTO (like
//     livetv_search_virtual_insert) is detected and replayed, preserving sparse
//     INTEGER PRIMARY KEY rowids.
//  2. A trigger indexing new.rowid on a table with a covering composite PK
//     autoindex (like catalog_alt_titles_fts_insert / catalog_studio_names_fts_insert)
//     keeps the source table's exact rowids even when rows were restored out of
//     PK order (e.g. PostgreSQL heap order -> SQLite).
func TestSQLiteReplayPreservesRowidAndInsertOrReplace(t *testing.T) {
	ctx := context.Background()
	setup := func() *sql.DB {
		db := openSQLiteMemory(t)
		for _, q := range []string{
			`CREATE TABLE rt_alt(k INTEGER NOT NULL, title TEXT NOT NULL, PRIMARY KEY(k, title))`,
			`CREATE VIRTUAL TABLE rt_alt_fts USING fts5(title, content='rt_alt', tokenize='trigram')`,
			`CREATE TRIGGER rt_alt_fts_insert AFTER INSERT ON rt_alt BEGIN INSERT INTO rt_alt_fts(rowid, title) VALUES (new.rowid, new.title); END`,
			`CREATE TRIGGER rt_alt_fts_delete AFTER DELETE ON rt_alt BEGIN INSERT INTO rt_alt_fts(rt_alt_fts, rowid, title) VALUES ('delete', old.rowid, old.title); END`,
			`CREATE TABLE rt_virt(docid INTEGER PRIMARY KEY, body TEXT NOT NULL)`,
			`CREATE VIRTUAL TABLE rt_virt_fts USING fts5(body, tokenize='unicode61')`,
			`CREATE TRIGGER rt_virt_fts_insert AFTER INSERT ON rt_virt BEGIN INSERT OR REPLACE INTO rt_virt_fts(rowid, body) VALUES (new.rowid, new.body); END`,
		} {
			if _, err := db.ExecContext(ctx, q); err != nil {
				t.Fatalf("setup (%s): %v", q, err)
			}
		}
		return db
	}

	src := setup()
	// Insert out of PK order so SQLite's covering autoindex (k, title) differs
	// from rowid order (rowid 1 = k:20/Zeta, rowid 2 = k:10/Alpha).
	for _, q := range []string{
		`INSERT INTO rt_alt(k, title) VALUES(20, 'Zeta Horizon')`,
		`INSERT INTO rt_alt(k, title) VALUES(10, 'Alpha Beacon')`,
		// Sparse docids: 5 and 42 (not 1 and 2).
		`INSERT INTO rt_virt(docid, body) VALUES(5, 'Morning Newsroom')`,
		`INSERT INTO rt_virt(docid, body) VALUES(42, 'Late Cinema Special')`,
	} {
		if _, err := src.ExecContext(ctx, q); err != nil {
			t.Fatalf("seed (%s): %v", q, err)
		}
	}

	var dump bytes.Buffer
	if err := ExportLogical(ctx, src, &dump); err != nil {
		t.Fatalf("ExportLogical: %v", err)
	}

	dst := setup()
	if err := ImportLogical(ctx, dst, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatalf("ImportLogical: %v", err)
	}

	// External-content FTS5 integrity check fails if any indexed rowid disagrees
	// with rt_alt's actual rowid.
	if _, err := dst.ExecContext(ctx, `INSERT INTO rt_alt_fts(rt_alt_fts) VALUES('integrity-check')`); err != nil {
		t.Fatalf("rt_alt_fts integrity-check failed after restore: %v", err)
	}
	var gotKey int64
	if err := dst.QueryRowContext(ctx, `SELECT a.k FROM rt_alt_fts f JOIN rt_alt a ON a.rowid = f.rowid WHERE rt_alt_fts MATCH '"Alpha"'`).Scan(&gotKey); err != nil || gotKey != 10 {
		t.Fatalf("rt_alt_fts lookup for Alpha = (%d, %v), want (10, nil)", gotKey, err)
	}
	if err := dst.QueryRowContext(ctx, `SELECT a.k FROM rt_alt_fts f JOIN rt_alt a ON a.rowid = f.rowid WHERE rt_alt_fts MATCH '"Zeta"'`).Scan(&gotKey); err != nil || gotKey != 20 {
		t.Fatalf("rt_alt_fts lookup for Zeta = (%d, %v), want (20, nil)", gotKey, err)
	}

	// INSERT OR REPLACE INTO trigger must have replayed with exact docids 5 and 42.
	var gotDoc int64
	if err := dst.QueryRowContext(ctx, `SELECT v.docid FROM rt_virt_fts f JOIN rt_virt v ON v.docid = f.rowid WHERE rt_virt_fts MATCH 'Cinema'`).Scan(&gotDoc); err != nil || gotDoc != 42 {
		t.Fatalf("rt_virt_fts lookup for Cinema = (%d, %v), want (42, nil)", gotDoc, err)
	}
	if err := dst.QueryRowContext(ctx, `SELECT v.docid FROM rt_virt_fts f JOIN rt_virt v ON v.docid = f.rowid WHERE rt_virt_fts MATCH 'Newsroom'`).Scan(&gotDoc); err != nil || gotDoc != 5 {
		t.Fatalf("rt_virt_fts lookup for Newsroom = (%d, %v), want (5, nil)", gotDoc, err)
	}
}
