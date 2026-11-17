package layouts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
)

func TestAtomicApplyDuplicateUndoAndFailure(t *testing.T) {
	db := store.OpenTest(t)
	ctx := t.Context()
	if _, e := db.Exec(`CREATE TABLE test_layout(id INTEGER PRIMARY KEY,value TEXT,acl TEXT);INSERT INTO test_layout VALUES(1,'A','private')`); e != nil {
		t.Fatal(e)
	}
	fail := false
	notified := 0
	p := Part{Name: "test", Capture: func(ctx context.Context, tx *sql.Tx) (json.RawMessage, error) {
		var v string
		e := tx.QueryRowContext(ctx, `SELECT value FROM test_layout WHERE id=1`).Scan(&v)
		b, _ := json.Marshal(v)
		return b, e
	}, Restore: func(ctx context.Context, tx *sql.Tx, b json.RawMessage) error {
		var v string
		if e := json.Unmarshal(b, &v); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `UPDATE test_layout SET value=? WHERE id=1`, v)
		return e
	}, Changed: func(context.Context) { notified++ }}
	guard := Part{Name: "guard", Capture: func(context.Context, *sql.Tx) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }, Restore: func(context.Context, *sql.Tx, json.RawMessage) error {
		if fail {
			return errors.New("missing reference")
		}
		return nil
	}}
	s := New(db, p, guard)
	if e := s.Save(ctx, "A", ""); e != nil {
		t.Fatal(e)
	}
	entries, _ := s.List(ctx)
	id := entries[0].ID
	if e := s.Save(ctx, "Copy", id); e != nil {
		t.Fatal(e)
	}
	db.Exec(`UPDATE test_layout SET value='B',acl='changed' WHERE id=1`)
	fail = true
	if e := s.Apply(ctx, id); e == nil {
		t.Fatal("partial apply accepted")
	}
	check := func(want string) {
		t.Helper()
		var v, acl string
		if e := db.QueryRow(`SELECT value,acl FROM test_layout WHERE id=1`).Scan(&v, &acl); e != nil {
			t.Fatal(e)
		}
		if v != want || acl != "changed" {
			t.Fatalf("got %s %s", v, acl)
		}
	}
	check("B")
	entries, _ = s.List(ctx)
	if len(entries) != 2 || notified != 0 {
		t.Fatal("failed apply changed snapshots or caches")
	}
	fail = false
	if e := s.Apply(ctx, id); e != nil {
		t.Fatal(e)
	}
	check("A")
	// Restart uses persisted snapshots; undo itself also saves the replaced layout.
	s = New(db, p, guard)
	if e := s.Apply(ctx, "_previous"); e != nil {
		t.Fatal(e)
	}
	check("B")
	if e := s.Apply(ctx, "_previous"); e != nil {
		t.Fatal(e)
	}
	check("A")
	if notified != 3 {
		t.Fatal(notified)
	}
}
