package identity

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

type touchViewer struct{ db *sql.DB }

func (v touchViewer) ActiveUser(ctx context.Context, id int64) (*core.User, error) {
	var active int
	if err := v.db.QueryRowContext(ctx, `SELECT active FROM auth_users WHERE id=?`, id).Scan(&active); err != nil || active != 1 {
		return nil, core.ErrUnauthorized
	}
	return &core.User{ID: id}, nil
}

// #648: a valid grant must resolve even when the sliding-expiry write
// cannot run (here a trigger aborts every grant UPDATE, standing in for
// a write gate held past its wait). The queued touch still extends the
// grant from the caller's side, and lands once writes work again.
func TestResolveSucceedsWhenTouchWriteFails(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(10000, 0)
	g, err := NewGrants(db, touchViewer{db}, time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	token, err := g.Issue(ctx, 1, nil, "plex", core.Device{ID: "device"})
	if err != nil {
		t.Fatal(err)
	}

	ddl := `CREATE TRIGGER block_grant_update BEFORE UPDATE ON identity_grants BEGIN SELECT RAISE(ABORT, 'write slot busy'); END`
	drop := `DROP TRIGGER block_grant_update`
	if store.DialectOf(db) == store.Postgres {
		if _, err := db.ExecContext(ctx, `CREATE FUNCTION block_grant_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'write slot busy'; END $$`); err != nil {
			t.Fatal(err)
		}
		ddl = `CREATE TRIGGER block_grant_update BEFORE UPDATE ON identity_grants FOR EACH ROW EXECUTE FUNCTION block_grant_update()`
		drop = `DROP TRIGGER block_grant_update ON identity_grants`
	}
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		t.Fatal(err)
	}
	now = now.Add(59 * time.Minute)
	if _, err := g.Resolve(ctx, "plex", token); err != nil {
		t.Fatalf("Resolve with a failing touch write: %v", err)
	}
	// Past the originally stored expiry, the queued touch alone keeps
	// the grant valid.
	now = now.Add(2 * time.Minute)
	if _, err := g.Resolve(ctx, "plex", token); err != nil {
		t.Fatalf("Resolve past stored expiry on a queued touch: %v", err)
	}

	if _, err := db.ExecContext(ctx, drop); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Resolve(ctx, "plex", token); err != nil {
		t.Fatal(err)
	}
	g.flushTouches(ctx)
	var lastUsed, expires int64
	if err := db.QueryRowContext(ctx, `SELECT last_used, expires FROM identity_grants`).Scan(&lastUsed, &expires); err != nil {
		t.Fatal(err)
	}
	if expires <= now.UnixMilli() || lastUsed < now.Add(-time.Minute).UnixMilli() {
		t.Fatalf("touch never landed: last_used=%d expires=%d now=%d", lastUsed, expires, now.UnixMilli())
	}
}
