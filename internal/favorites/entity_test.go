package favorites

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestParseEntityKey(t *testing.T) {
	valid := []struct {
		raw  string
		want EntityKey
	}{
		{
			raw:  "community.example.scores:scores:team:t1",
			want: EntityKey{PluginID: "community.example.scores", Feed: "scores", Kind: "team", ID: "t1"},
		},
		{
			raw:  "community.example.scores:live_feed:driver:series:44",
			want: EntityKey{PluginID: "community.example.scores", Feed: "live_feed", Kind: "driver", ID: "series:44"},
		},
	}
	for _, tc := range valid {
		got, ok := ParseEntityKey(tc.raw)
		if !ok || got != tc.want {
			t.Fatalf("ParseEntityKey(%q) = (%+v, %v), want (%+v, true)", tc.raw, got, ok, tc.want)
		}
		if got.String() != tc.raw {
			t.Fatalf("EntityKey.String() = %q, want %q", got.String(), tc.raw)
		}
	}

	invalid := []string{
		"",
		"community.example.scores:scores:team",
		"community.example.scores:scores:team:",
		":scores:team:t1",
		"community.example.scores::team:t1",
		"community.example.scores:scores::t1",
		"Community.Upper:scores:team:t1",
		"community.example.scores:BadFeed:team:t1",
		"community.example.scores:scores:BadKind:t1",
		"community.example.scores:scores:team: t1",
		"community.example.scores:scores:team:https://evil.invalid",
		"community.example.scores:scores:team:a\nb",
		"community.example.scores:scores:team:" + strings.Repeat("x", 500),
	}
	for _, raw := range invalid {
		if got, ok := ParseEntityKey(raw); ok {
			t.Fatalf("ParseEntityKey(%q) unexpectedly accepted: %+v", raw, got)
		}
	}
}

func TestEntityFavoritesPerViewerAndPolicy(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	db := store.OpenTest(t)
	deniedKey := FormatEntityKey("community.example.disabled", "scores", "team", "x")
	changedCount := 0
	svc, err := New(Options{
		DB:     db,
		Marked: state.New(db, state.Options{}).Playlists().(core.TransactionalMarkedPlaylists),
		Now:    func() time.Time { return now },
		Active: func(_ context.Context, id int64) (*core.User, error) {
			return &core.User{ID: id}, nil
		},
		Allowed: func(_ context.Context, _ *core.User, sub Subject) bool {
			return sub.ID != deniedKey
		},
		Changed: func(context.Context, int64, Subject) {
			changedCount++
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	v1 := &core.User{ID: 1}
	v2 := &core.User{ID: 2}
	k1 := FormatEntityKey("community.example.scores", "scores", "team", "alpha")
	k2 := FormatEntityKey("community.example.scores", "scores", "team", "beta")

	// Reject malformed entity key.
	if _, err := svc.Set(ctx, v1, Subject{Kind: KindEntity, ID: "not-a-four-part-key"}, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for malformed key, got %v", err)
	}
	// Reject policy-denied entity key.
	if _, err := svc.Set(ctx, v1, Subject{Kind: KindEntity, ID: deniedKey}, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden for denied key, got %v", err)
	}

	// Set two favorites for viewer 1; repeat write is idempotent with no duplicate event.
	if changed, err := svc.Set(ctx, v1, Subject{Kind: KindEntity, ID: k1}, true); err != nil || !changed {
		t.Fatalf("set k1: %v %v", changed, err)
	}
	if changed, err := svc.Set(ctx, v1, Subject{Kind: KindEntity, ID: k1}, true); err != nil || changed || changedCount != 1 {
		t.Fatalf("duplicate set k1: changed=%v err=%v events=%d", changed, err, changedCount)
	}
	now = now.Add(time.Second)
	if changed, err := svc.Set(ctx, v1, Subject{Kind: KindEntity, ID: k2}, true); err != nil || !changed {
		t.Fatalf("set k2: %v %v", changed, err)
	}

	// Viewer 2 sees none of viewer 1's entity favorites.
	if yes, err := svc.Has(ctx, v2, Subject{Kind: KindEntity, ID: k1}); err != nil || yes {
		t.Fatalf("viewer 2 saw viewer 1 entity favorite: yes=%v err=%v", yes, err)
	}
	if list2, err := svc.List(ctx, v2, KindEntity, 0); err != nil || len(list2) != 0 {
		t.Fatalf("viewer 2 list = %+v %v, want empty", list2, err)
	}

	// Viewer 1 lists both entities, newest first.
	list1, err := svc.List(ctx, v1, KindEntity, 0)
	if err != nil || len(list1) != 2 || list1[0].Subject.ID != k2 || list1[1].Subject.ID != k1 {
		t.Fatalf("viewer 1 list = %+v %v", list1, err)
	}

	// Membership works without scanning.
	if yes, at, err := svc.Membership(ctx, v1, Subject{Kind: KindEntity, ID: k2}); err != nil || !yes || at == nil || !at.Equal(now) {
		t.Fatalf("membership k2 = (%v, %v, %v)", yes, at, err)
	}

	// Removing k1 leaves only k2.
	if changed, err := svc.Set(ctx, v1, Subject{Kind: KindEntity, ID: k1}, false); err != nil || !changed {
		t.Fatalf("delete k1: %v %v", changed, err)
	}
	list1, err = svc.List(ctx, v1, KindEntity, 0)
	if err != nil || len(list1) != 1 || list1[0].Subject.ID != k2 {
		t.Fatalf("viewer 1 list after delete = %+v %v", list1, err)
	}
}
