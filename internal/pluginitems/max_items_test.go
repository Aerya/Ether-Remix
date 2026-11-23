package pluginitems

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestDynamicMaxPluginItemsAndPluginItemCount(t *testing.T) {
	ctx := context.Background()
	var liveCap atomic.Int64
	liveCap.Store(2)

	s, _ := openTestService(t, Options{
		MaxPluginItemsFor: func(pluginID string) int {
			if pluginID == "community.books" {
				return int(liveCap.Load())
			}
			return DefaultMaxPluginItems
		},
	})
	decl := sampleBookDecl()
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatal(err)
	}

	if got := s.MaxItemsFor("community.books"); got != 2 {
		t.Fatalf("MaxItemsFor(community.books) = %d, want 2", got)
	}
	if got := s.MaxItemsFor("other.plugin"); got != DefaultMaxPluginItems {
		t.Fatalf("MaxItemsFor(other.plugin) = %d, want %d", got, DefaultMaxPluginItems)
	}

	if _, err := s.KindUpsert(ctx, "community.books", "book", []core.KindItem{
		{ID: "s1", Level: "series", Title: "Series 1"},
		{ID: "b1", Level: "book", ParentID: "s1", Title: "Book 1"},
	}); err != nil {
		t.Fatalf("upsert within cap: %v", err)
	}

	count, err := s.PluginItemCount(ctx, "community.books")
	if err != nil || count != 2 {
		t.Fatalf("PluginItemCount = (%d, %v), want (2, nil)", count, err)
	}

	if _, err := s.KindUpsert(ctx, "community.books", "book", []core.KindItem{
		{ID: "b2", Level: "book", ParentID: "s1", Title: "Book 2"},
	}); !errors.Is(err, ErrMaxItemsExceeded) {
		t.Fatalf("over cap err = %v, want ErrMaxItemsExceeded", err)
	}

	// Owner raises cap dynamically above 2,000,000 (e.g. 5,000,000); upsert succeeds immediately.
	liveCap.Store(5_000_000)
	if got := s.MaxItemsFor("community.books"); got != 5_000_000 {
		t.Fatalf("raised MaxItemsFor = %d, want 5000000", got)
	}
	if _, err := s.KindUpsert(ctx, "community.books", "book", []core.KindItem{
		{ID: "b2", Level: "book", ParentID: "s1", Title: "Book 2"},
		{ID: "b3", Level: "book", ParentID: "s1", Title: "Book 3"},
	}); err != nil {
		t.Fatalf("upsert after raising cap: %v", err)
	}

	count, err = s.PluginItemCount(ctx, "community.books")
	if err != nil || count != 4 {
		t.Fatalf("PluginItemCount after upsert = (%d, %v), want (4, nil)", count, err)
	}

	if err := s.KindDelete(ctx, "community.books", "book", []string{"b3"}); err != nil {
		t.Fatalf("KindDelete: %v", err)
	}
	count, err = s.PluginItemCount(ctx, "community.books")
	if err != nil || count != 3 {
		t.Fatalf("PluginItemCount after delete = (%d, %v), want (3, nil)", count, err)
	}
}
