package newznab

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestRetainedCandidatesAvoidFanoutAndRescueBypassesThem(t *testing.T) {
	m := NewManager(nil, nil, Options{})
	calls := 0
	m.SetRetainedSearch(func(context.Context, core.SearchRequest) ([]core.Release, bool) {
		calls++
		return []core.Release{{ID: "retained"}}, true
	})
	observed := 0
	m.SetSearchObserver(func(context.Context, core.SearchRequest, []core.Release) { observed++ })
	arrived := 0
	rs, err := m.SearchWith(t.Context(), core.SearchRequest{}, func(rs []core.Release) { arrived += len(rs) })
	if err != nil || len(rs) != 1 || arrived != 1 || calls != 1 || observed != 0 {
		t.Fatal(rs, err, arrived, calls)
	}
	rs, err = m.SearchRescue(t.Context(), core.SearchRequest{})
	if err != nil || len(rs) != 0 || calls != 1 {
		t.Fatal("rescue reused rejected observation", rs, err, calls)
	}
}

type libraryBackend struct{}

func (libraryBackend) Search(context.Context, core.SearchRequest, func(context.Context, string) error) ([]core.Release, error) {
	return []core.Release{{ID: "sample", Name: "Sample.2026.1080p", Protocol: core.ProtocolHTTP, StreamURL: "https://example.test/file"}}, nil
}
func (libraryBackend) FetchNZB(context.Context, core.Release, func(context.Context, string) error) ([]byte, error) {
	return nil, nil
}
func TestSearchObserverRetainsPrimaryAndRescueReferences(t *testing.T) {
	m := NewManager(nil, []config.Indexer{{ID: "source", Name: "Source", Kind: config.IndexerKindAddon, URL: "https://example.test", Enabled: true}}, Options{Backend: func(config.Indexer) (Backend, error) { return libraryBackend{}, nil }})
	var observed []core.Release
	m.SetSearchObserver(func(_ context.Context, _ core.SearchRequest, rs []core.Release) { observed = append(observed, rs...) })
	for _, search := range []func(context.Context, core.SearchRequest) ([]core.Release, error){m.Search, m.SearchRescue} {
		before := len(observed)
		rs, err := search(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "Sample", Year: 2026, Season: 1, Episode: 1})
		if err != nil || len(rs) != 1 || len(observed) != before+1 || observed[before].StreamURL == "" || observed[before].IndexerID != "source" {
			t.Fatal(err, rs, observed)
		}
	}
}

func (libraryBackend) ResolveTorrent(_ context.Context, r core.Release, _ func(context.Context, string) error) (core.Release, error) {
	return r, nil
}

func TestRetainedMovieRescueRefreshesAddon(t *testing.T) {
	m := NewManager(nil, []config.Indexer{{ID: "source", Name: "Source", Kind: config.IndexerKindAddon, URL: "https://example.test", Enabled: true}}, Options{Backend: func(config.Indexer) (Backend, error) { return libraryBackend{}, nil }})
	m.SetRetainedSearch(func(context.Context, core.SearchRequest) ([]core.Release, bool) {
		return []core.Release{{ID: "old"}}, true
	})
	m.SetRetainedRefresh(func() bool { return true })
	rs, err := m.SearchRescue(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "Sample"})
	if err != nil || len(rs) != 1 || rs[0].ID != "sample" {
		t.Fatal("rescue did not refresh addon", err, rs)
	}
}
