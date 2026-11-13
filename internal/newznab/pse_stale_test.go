package newznab

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
	"time"
)

type outageBackend struct {
	fakeBackend
	err   error
	empty bool
}

func (b *outageBackend) Search(ctx context.Context, r core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.empty {
		return nil, nil
	}
	return b.fakeBackend.Search(ctx, r, reserve)
}
func TestStaleSearchOutageAndRecovery(t *testing.T) {
	now := time.Now()
	db := store.OpenTest(t)
	b := &outageBackend{}
	cfg := config.Indexer{ID: "outage", Name: "Outage", Kind: config.IndexerKindAddon, URL: "https://example.invalid", Enabled: true}
	opts := Options{Now: func() time.Time { return now }, SearchCacheTTL: time.Hour, Backend: func(config.Indexer) (Backend, error) { return b, nil }}
	c := New(cfg, db, opts)
	req := core.SearchRequest{Type: core.TypeMovie, Title: "Film"}
	fresh, err := c.Search(context.Background(), req)
	if err != nil || len(fresh) != 1 {
		t.Fatal(fresh, err)
	}
	now = now.Add(2 * time.Hour)
	c = New(cfg, db, opts) // persisted across restart
	b.err = core.ErrSourceUnavailable
	ctx, coverage := core.WithSearchCoverage(context.Background())
	rs, err := c.Search(ctx, req)
	if err != nil || len(rs) != 1 || !coverage.Incomplete() {
		t.Fatalf("stale fallback: %v %v incomplete=%v", rs, err, coverage.Incomplete())
	}
	if _, ok := c.cacheGet(ctx, cacheKey(c, req)); ok {
		t.Fatal("stale answer renewed its lifetime")
	}
	b.err = core.ErrUnauthorized
	if _, err = c.Search(context.Background(), req); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("auth error hidden", err)
	}
	b.err = nil
	b.empty = true
	rs, err = c.Search(context.Background(), req)
	if err != nil || len(rs) != 0 {
		t.Fatal("valid empty resurrected stale results", rs, err)
	}
}
func TestStaleSearchBoundaries(t *testing.T) {
	now := time.Now()
	cfg := config.Indexer{ID: "x", Name: "x", Enabled: true}
	c := New(cfg, nil, Options{Now: func() time.Time { return now }, SearchCacheTTL: time.Hour})
	ctx := context.Background()
	key := "test"
	c.cachePut(ctx, key, []core.Release{{ID: "torrent", Protocol: core.ProtocolTorrent, DebridCached: []string{"alldebrid"}}, {ID: "http", Protocol: core.ProtocolHTTP}})
	now = now.Add(2 * time.Hour)
	rs, ok := c.staleFallback(ctx, key, core.ErrSourceUnavailable)
	if !ok || len(rs) != 1 || len(rs[0].DebridCached) != 0 {
		t.Fatal(rs, ok)
	}
	for _, err := range []error{nil, core.ErrUnauthorized, core.ErrBudgetExhausted, context.Canceled} {
		if _, ok = c.staleFallback(ctx, key, err); ok {
			t.Fatal("unexpected fallback", err)
		}
	}
	bg := core.WithIndexerPurpose(ctx, core.IndexerPurposeAvailability)
	if _, ok = c.staleFallback(bg, key, core.ErrSourceUnavailable); ok {
		t.Fatal("stale availability evidence")
	}
	now = now.Add(24 * time.Hour)
	if _, ok = c.staleFallback(ctx, key, core.ErrSourceUnavailable); ok {
		t.Fatal("unbounded stale result")
	}
}
func TestEffectiveSearchCacheRespectsConfiguredCap(t *testing.T) {
	now := time.Now()
	c := New(config.Indexer{ID: "ttl", Enabled: true}, nil, Options{Now: func() time.Time { return now }, SearchCacheTTL: time.Hour})
	ctx := withCacheAge(context.Background(), now.Add(-400*24*time.Hour))
	c.cachePut(ctx, "old", []core.Release{{ID: "old"}})
	if got := c.memCache["old"].expires.Sub(now); got != time.Hour {
		t.Fatal("configured cap overridden", got)
	}
	c.cachePut(ctx, "empty", nil)
	if got := c.memCache["empty"].expires.Sub(now); got != emptyStableTTL {
		t.Fatal("empty TTL", got)
	}
}
