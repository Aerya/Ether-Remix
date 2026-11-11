package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plextv"
)

func TestDisplayNameSurvivesPlexSync(t *testing.T) {
	ctx := context.Background()
	s, _, _ := setup(t)
	owner := plextv.Account{ID: 42, UUID: "u", Username: "plexname", Title: "Plex Title"}
	if err := s.SetOwner(ctx, owner, "server-token"); err != nil {
		t.Fatal(err)
	}
	id := s.byPlex[42]
	if err := s.SetDisplayName(ctx, id, "  Julien  "); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwner(ctx, owner, "server-token"); err != nil {
		t.Fatal(err)
	}
	u, err := s.User(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if u.Title != "Julien" || u.CustomName != "Julien" || u.Username != "plexname" || u.ActivityName() != "Julien" {
		t.Fatalf("after sync: %+v", u)
	}
	var stored string
	if err := s.db.QueryRowContext(ctx, `SELECT title FROM auth_users WHERE id=?`, id).Scan(&stored); err != nil || stored != "Plex Title" {
		t.Fatalf("plex title = %q (%v)", stored, err)
	}
	if err := s.SetDisplayName(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if u, _ = s.User(ctx, id); u.Title != "Plex Title" || u.CustomName != "" || u.ActivityName() != "plexname" {
		t.Fatalf("after clear: %+v", u)
	}
}

func TestDisplayNameRejected(t *testing.T) {
	ctx := context.Background()
	s, _, _ := setup(t)
	if err := s.SetDisplayName(ctx, 9999, "x"); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown viewer: %v", err)
	}
	for _, bad := range []string{strings.Repeat("é", core.MaxCustomNameLength+1), "a\nb"} {
		if err := s.SetDisplayName(ctx, 1, bad); !errors.Is(err, core.ErrInvalidCustomName) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}
