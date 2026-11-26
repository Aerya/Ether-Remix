package state

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestPlayContextsKeepTheNewestPerViewer(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	s := New(db, Options{Now: func() time.Time { now = now.Add(time.Second); return now }})
	for i := 0; i < MaxPlayContexts+5; i++ {
		if err := s.RecordPlayContext(ctx, 1, "title", fmt.Sprintf("t%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordPlayContext(ctx, 1, "title", "t10"); err != nil { // played again: moves to the front
		t.Fatal(err)
	}
	if err := s.RecordPlayContext(ctx, 2, "favorites", "track"); err != nil {
		t.Fatal(err)
	}
	got, err := s.RecentPlayContexts(ctx, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxPlayContexts || got[0].Ref != "t10" || got[1].Ref != fmt.Sprintf("t%d", MaxPlayContexts+4) {
		t.Fatalf("got %d, first %+v second %+v", len(got), got[0], got[1])
	}
	other, err := s.RecentPlayContexts(ctx, 2, 5)
	if err != nil || len(other) != 1 || other[0].Kind != "favorites" {
		t.Fatalf("other viewer: %+v %v", other, err)
	}
}
