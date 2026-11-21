package auth

import (
	"context"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestRemixLibraryReservationsIgnoreInvalidViewerJSON(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	for _, row := range []struct {
		id            int
		plex, allowed string
	}{
		{1, `[1]`, `[5101]`},
		{2, `not json`, `[5102]`},
		{3, `[2]`, ``},
	} {
		if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,active,plex_sections,allowed_libraries,created_at,updated_at) VALUES(?,?,1,?,?,0,0)`, row.id, row.id, row.plex, row.allowed); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	got, err := RemixLibraryReservations(ctx, tx)
	if err != nil {
		t.Fatalf("invalid JSON must not fail: %v", err)
	}
	for _, want := range []core.LibraryID{5101, 5102} {
		if !slices.Contains(got, want) {
			t.Fatalf("reservations %v miss %d", got, want)
		}
	}
}
