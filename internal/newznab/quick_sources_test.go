package newznab

import (
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
)

func TestQuickSearchAndRescueOnlySelectedIndexer(t *testing.T) {
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a, b := newFakeIndexer(t, ""), newFakeIndexer(t, "")
	ca, cb := testConfig(a.server.URL), testConfig(b.server.URL)
	ca.ID = "a"
	cb.ID = "b"
	m := NewManager(db, []config.Indexer{ca, cb}, Options{NZBDir: t.TempDir()})
	ctx := core.WithQuickSource(t.Context(), "usenet", map[string]bool{"a": true})
	req := core.SearchRequest{Type: core.TypeMovie, Title: "The Signal", Year: 1999}
	_, _ = m.Search(ctx, req)
	_, _ = m.SearchRescue(ctx, req)
	if len(a.hits) == 0 || len(b.hits) != 0 {
		t.Fatalf("a=%v b=%v", a.hits, b.hits)
	}
}
