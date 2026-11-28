package newznab

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

type alkoLikeBackend struct{ *fakeBackend }

func (alkoLikeBackend) Search(context.Context, core.SearchRequest, func(context.Context, string) error) ([]core.Release, error) {
	return []core.Release{{ID: "sfr-alko:x", GUID: "sfr-alko:x", Name: "Film.2020.FRENCH.1080p.mkv", Size: 10, Protocol: core.ProtocolHTTP,
		StreamURL: "https://alldebrid.com/f/privatetoken", StreamDebrid: "alldebrid", IndexerID: "remix-alko"}}, nil
}

func TestLocalAlkoSourceNeverWritesLinksToSearchCache(t *testing.T) {
	db := store.OpenTest(t)
	opts := Options{Now: time.Now, SearchCacheTTL: time.Hour, Backend: func(config.Indexer) (Backend, error) { return alkoLikeBackend{&fakeBackend{}}, nil }}
	req := core.SearchRequest{Type: core.TypeMovie, Title: "Film"}
	for _, id := range []string{"remix-alko", "autre"} {
		c := New(config.Indexer{ID: id, Name: id, Kind: config.IndexerKindAddon, URL: "https://remix.invalid/x", Enabled: true}, db, opts)
		if rs, err := c.Search(context.Background(), req); err != nil || len(rs) != 1 {
			t.Fatal(id, rs, err)
		}
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM newznab_cache WHERE indexer_id=?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if want := map[string]int{"remix-alko": 0, "autre": 1}[id]; n != want {
			t.Fatalf("%s : %d lignes de cache, %d attendues", id, n, want)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM newznab_cache WHERE results LIKE '%privatetoken%' AND indexer_id='remix-alko'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("lien privé en cache : %d %v", n, err)
	}
}
