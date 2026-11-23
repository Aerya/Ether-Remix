package experience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/viewerpolicy"
)

// Proof reuse is legal only for the epoch the outer bootstrap will check.
// A changed epoch must not stamp old, memoized policy onto new receipts.
func TestBootstrapProofReuseAndReceiptEpochFence(t *testing.T) {
	db := store.OpenTest(t)
	ctx := t.Context()
	epoch, err := viewerpolicy.Read(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	ctx = WithBootstrapProof(ctx, 7, epoch)
	reads := 0
	active := func(context.Context, int64) (*core.User, error) { reads++; return &core.User{ID: 7}, nil }
	for range 3 {
		if _, err := bootstrapActive(ctx, active, 7); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 1 {
		t.Fatalf("active reads=%d", reads)
	}
	now := time.Unix(1700000000, 0)
	opts := relayOptions(db, &now, 100, true, nil)
	receipts, err := NewReceipts(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err = viewerpolicy.Bump(ctx, db); err != nil {
		t.Fatal(err)
	}
	tokens, err := receipts.IssueBatch(ctx, &core.User{ID: 7}, core.Device{ID: "tv"}, "vela", []ReceiptSubject{{Kind: "title", ID: "one"}})
	if !errors.Is(err, viewerpolicy.ErrChanged) || tokens != nil {
		t.Fatalf("stale proof issued tokens=%v err=%v", tokens, err)
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM experience_receipts`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("receipts=%d err=%v", count, err)
	}
}
