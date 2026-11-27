package usenet

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/usenet/nntptest"
)

func tierIDs(f *Fetcher) [][]string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out [][]string
	for _, tier := range f.tiers {
		var ids []string
		for _, p := range tier {
			ids = append(ids, p.cfg.ID)
		}
		out = append(out, ids)
	}
	return out
}

// providersByPriority returns primaries a (priority 0) and b (priority 1)
// plus a backfill block account, all holding the given articles.
func providersByPriority(t *testing.T, latency time.Duration, ids ...string) ([]config.UsenetProvider, map[string]*nntptest.Server) {
	t.Helper()
	servers := map[string]*nntptest.Server{}
	var out []config.UsenetProvider
	for i, id := range []string{"a", "b", "block"} {
		s := nntptest.Start(t, nntptest.Options{Latency: latency})
		for _, msg := range ids {
			addSeg(t, s, msg, []byte("x"))
		}
		servers[id] = s
		cfg := s.Provider(id, 2)
		cfg.Priority, cfg.Backfill = i, id == "block"
		out = append(out, cfg)
	}
	return out, servers
}

func TestBalancedMergesPrimaryTiers(t *testing.T) {
	providers, _ := providersByPriority(t, 0)
	f := newFetcher(t, providers, Options{})
	if got := fmt.Sprint(tierIDs(f)); got != "[[a] [b] [block]]" {
		t.Fatalf("in order tiers %s", got)
	}
	f = newFetcher(t, providers, Options{Balanced: true})
	if got := fmt.Sprint(tierIDs(f)); got != "[[a b] [block]]" {
		t.Fatalf("balanced tiers %s", got)
	}
}

// Balanced mode spreads concurrent work over primaries with different
// priorities, and never onto the backfill account.
func TestBalancedSharesLoadAcrossPriorities(t *testing.T) {
	var ids []string
	for i := 0; i < 20; i++ {
		ids = append(ids, fmt.Sprintf("s%d@x", i))
	}
	providers, srv := providersByPriority(t, 20*time.Millisecond, ids...)
	f := newFetcher(t, providers, Options{Balanced: true})
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.Body(context.Background(), id, nil, core.PriorityLive); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	a, b := srv["a"].Stats().ArticlesSent, srv["b"].Stats().ArticlesSent
	if a+b != 20 || a == 0 || b == 0 {
		t.Fatalf("load split %d/%d", a, b)
	}
	if n := srv["block"].Stats().ArticlesSent; n != 0 {
		t.Fatalf("backfill served %d articles", n)
	}
}

// The load mode changes live and keeps the existing pools.
func TestBalancedToggleKeepsPools(t *testing.T) {
	providers, _ := providersByPriority(t, 0)
	f := newFetcher(t, providers, Options{})
	a := poolFor(t, f, "a")
	f.UpdateConfig(config.Usenet{Providers: providers, LoadMode: config.UsenetLoadBalanced})
	if got := fmt.Sprint(tierIDs(f)); got != "[[a b] [block]]" {
		t.Fatalf("after balanced %s", got)
	}
	if poolFor(t, f, "a") != a {
		t.Fatal("pool rebuilt on a load mode change")
	}
	f.UpdateConfig(config.Usenet{Providers: providers, LoadMode: config.UsenetLoadInOrder})
	if got := fmt.Sprint(tierIDs(f)); got != "[[a] [b] [block]]" {
		t.Fatalf("after in order %s", got)
	}
}

// A provider that keeps missing one file's articles is asked after its tier
// peer for that file, and still asked when the peer misses too.
func TestMissHintDemotesWithinTier(t *testing.T) {
	var ids []string
	for i := 0; i < 20; i++ {
		ids = append(ids, fmt.Sprintf("m%d@x", i))
	}
	providers, srv := providersByPriority(t, 0, ids...)
	srv["b"].Remove(ids...)
	addSeg(t, srv["b"], "only-b@x", []byte("y"))
	f := newFetcher(t, providers, Options{Balanced: true})
	hint := &core.ProviderMissHint{}
	bKey := poolFor(t, f, "b").key
	ctx := core.WithProviderMissHint(context.Background(), hint)
	// Rotation puts b first about half the time until it is demoted.
	n := 0
	for ; n < len(ids) && !hint.Demoted(bKey); n++ {
		if _, err := f.Body(ctx, ids[n], nil, core.PriorityLive); err != nil {
			t.Fatal(err)
		}
	}
	if !hint.Demoted(bKey) {
		t.Fatalf("b not demoted after %d articles", n)
	}
	order, _ := f.route(ctx, routeReq{prio: core.PriorityLive}, nil)
	if order[0].cfg.ID != "a" {
		t.Fatalf("demoted provider was routed first: %s", order[0].cfg.ID)
	}
	if _, err := f.Body(ctx, "only-b@x", nil, core.PriorityLive); err != nil {
		t.Fatalf("demoted provider not asked when its peer missed: %v", err)
	}
	if n := srv["block"].Stats().ArticlesSent; n != 0 {
		t.Fatalf("backfill served %d articles", n)
	}
}

// A hint reorders inside a tier only: in priority order a demoted head
// provider stays ahead of the next tier.
func TestMissHintStaysInTier(t *testing.T) {
	providers, _ := providersByPriority(t, 0)
	f := newFetcher(t, providers, Options{})
	hint := &core.ProviderMissHint{}
	for range core.ProviderMissDemote {
		hint.Miss(poolFor(t, f, "a").key)
	}
	var got []string
	pools, balanced := f.orderSnapshot()
	for _, p := range demoteMissed(hint, pools, balanced) {
		got = append(got, p.cfg.ID)
	}
	if fmt.Sprint(got) != "[a b block]" {
		t.Fatalf("order %v", got)
	}
}
