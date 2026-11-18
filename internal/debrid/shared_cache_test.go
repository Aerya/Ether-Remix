package debrid

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// failingProvider answers Acquire with a fixed error while it is set.
type failingProvider struct {
	*fakeProvider
	err error
}

func (p *failingProvider) Acquire(ctx context.Context, req AcquireRequest) (*Acquired, error) {
	if p.err != nil {
		p.count("acquire")
		return nil, p.err
	}
	return p.fakeProvider.Acquire(ctx, req)
}

// localShared is a fake local BitTorrent client (a LocalDownloader).
type localShared struct{ *failingProvider }

func (localShared) LocalDownloads() bool { return false }

// sharedCacheService builds a service with one fake account per kind, named
// "<kind>-<index>". Every account has the test hash cached.
func sharedCacheService(t *testing.T, kinds ...string) (*Service, map[string]*failingProvider) {
	t.Helper()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	media := newMediaServer(t, 1<<20)
	provs := map[string]*failingProvider{}
	locals := map[string]Provider{}
	var cfg config.Debrid
	cfg.CachedOnly, cfg.RemoveUncached = true, true
	for i, k := range kinds {
		id := fmt.Sprintf("%s-%d", k, i)
		fp := newFakeProvider(k, media, movieFiles(1<<20))
		fp.cached[testHash] = true
		provs[id] = &failingProvider{fakeProvider: fp}
		if k == "remix-torrent" {
			locals[id] = localShared{provs[id]}
		}
		cfg.Providers = append(cfg.Providers, config.DebridProvider{ID: id, Name: id, Kind: k, APIKey: "k", Priority: i, Enabled: true})
	}
	s := NewService(cfg, Options{DB: db, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), NewProvider: func(c config.DebridProvider, _ ProviderOptions) (Provider, error) {
		if p := locals[c.ID]; p != nil {
			return p, nil
		}
		if p := provs[c.ID]; p != nil {
			return p, nil
		}
		return nil, fmt.Errorf("unknown %s", c.ID)
	}})
	return s, provs
}

// A debrid service's cache is shared by its accounts: "not cached" on the
// first AllDebrid account is not asked again of the second.
func TestNotCachedNotAskedAgainOfSameService(t *testing.T) {
	s, provs := sharedCacheService(t, "alldebrid", "alldebrid")
	delete(provs["alldebrid-0"].cached, testHash)
	_, err := s.Acquire(context.Background(), torrentRelease("a"))
	if !errors.Is(err, core.ErrNotCached) {
		t.Fatalf("want ErrNotCached, got %v", err)
	}
	if n := provs["alldebrid-0"].callCount("acquire"); n != 1 {
		t.Fatalf("first account asked %d times, want 1", n)
	}
	if n := provs["alldebrid-1"].callCount("acquire"); n != 0 {
		t.Fatalf("second account of the same service asked %d times, want 0", n)
	}
}

// An account error on the first account still moves on to the second
// account of the same service.
func TestAccountErrorStillTriesSameServiceAccount(t *testing.T) {
	for name, fail := range map[string]error{
		"auth":  fmt.Errorf("fake: %w", ErrAuth),
		"limit": fmt.Errorf("fake: %w", core.ErrDownloadLimit),
	} {
		s, provs := sharedCacheService(t, "alldebrid", "alldebrid")
		provs["alldebrid-0"].err = fail
		if _, err := s.Acquire(context.Background(), torrentRelease("a")); err != nil {
			t.Fatalf("%s: second account not used: %v", name, err)
		}
		if n := provs["alldebrid-1"].callCount("acquire"); n != 1 {
			t.Fatalf("%s: second account asked %d times, want 1", name, n)
		}
	}
}

// Another service has its own cache: it is still asked.
func TestNotCachedStillAsksOtherService(t *testing.T) {
	s, provs := sharedCacheService(t, "alldebrid", "torbox")
	delete(provs["alldebrid-0"].cached, testHash)
	if _, err := s.Acquire(context.Background(), torrentRelease("a")); err != nil {
		t.Fatalf("other service not used: %v", err)
	}
	if n := provs["torbox-1"].callCount("acquire"); n != 1 {
		t.Fatalf("other service asked %d times, want 1", n)
	}
}

// A refusal is the account's answer, not the cache's, even though it wraps
// ErrNotCached: the second account of the same service is still asked. This
// covers AllDebrid's MAGNET_TOO_MANY, which is a refusal and a download limit.
func TestRefusalStillTriesSameServiceAccount(t *testing.T) {
	for name, fail := range map[string]error{
		"refused":         fmt.Errorf("fake: %w", ErrAddRefused),
		"blocked":         fmt.Errorf("fake: %w", ErrBlocked),
		"magnet too many": adError(200, "MAGNET_TOO_MANY", "Magnets limit reached"),
	} {
		if name == "magnet too many" && !errors.Is(fail, ErrAddRefused) {
			t.Fatalf("MAGNET_TOO_MANY is no longer a refusal: %v", fail)
		}
		s, provs := sharedCacheService(t, "alldebrid", "alldebrid")
		provs["alldebrid-0"].err = fail
		if _, err := s.Acquire(context.Background(), torrentRelease("a")); err != nil {
			t.Fatalf("%s: second account not used: %v", name, err)
		}
		if n := provs["alldebrid-1"].callCount("acquire"); n != 1 {
			t.Fatalf("%s: second account asked %d times, want 1", name, n)
		}
	}
}

// A plugin or a local BitTorrent client has no cache shared with another
// account of the same kind: "not cached" is asked again of the second one.
func TestNotCachedStillAsksDuplicatePluginAndLocal(t *testing.T) {
	for _, kind := range []string{"plugin:x", "remix-torrent"} {
		s, provs := sharedCacheService(t, kind, kind)
		id0, id1 := kind+"-0", kind+"-1"
		delete(provs[id0].cached, testHash)
		if _, err := s.Acquire(context.Background(), torrentRelease("a")); err != nil {
			t.Fatalf("%s: second account not used: %v", kind, err)
		}
		if n := provs[id1].callCount("acquire"); n != 1 {
			t.Fatalf("%s: second account asked %d times, want 1", kind, n)
		}
	}
}
