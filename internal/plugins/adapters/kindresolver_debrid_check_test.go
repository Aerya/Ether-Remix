package adapters

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
)

type stubDebridProvider struct {
	kind   string
	cached map[string]bool
	files  []debrid.File
}

func (p *stubDebridProvider) Kind() string { return p.kind }

func (p *stubDebridProvider) CheckCached(_ context.Context, hashes []string) (map[string]bool, error) {
	out := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		if p.cached[strings.ToLower(h)] {
			out[strings.ToLower(h)] = true
		}
	}
	return out, nil
}

func (p *stubDebridProvider) Find(context.Context, string) (string, error) {
	return "", debrid.ErrUnsupported
}

func (p *stubDebridProvider) Acquire(_ context.Context, req debrid.AcquireRequest) (*debrid.Acquired, error) {
	if !p.cached[strings.ToLower(req.InfoHash)] {
		return nil, core.ErrNotCached
	}
	f := p.files[0]
	if req.Choose != nil {
		var err error
		f, err = req.Choose(p.files)
		if err != nil {
			return nil, err
		}
	}
	return &debrid.Acquired{TorrentID: "tor-" + p.kind, File: f}, nil
}

func (p *stubDebridProvider) Link(_ context.Context, h debrid.Handle) (debrid.DirectLink, error) {
	return debrid.DirectLink{
		URL:     "https://cdn.example/" + p.kind + "/" + h.FileID,
		Size:    h.Size,
		Expires: time.Now().Add(time.Hour),
	}, nil
}

func (p *stubDebridProvider) Remove(context.Context, string) error { return nil }

func (p *stubDebridProvider) Account(context.Context) (debrid.Account, error) {
	return debrid.Account{User: "tester", Premium: true}, nil
}

type staticIndexer struct {
	rels []core.Release
}

func (staticIndexer) Name() string { return "static" }

func (s staticIndexer) Search(context.Context, core.SearchRequest) ([]core.Release, error) {
	return s.rels, nil
}

func (s staticIndexer) FetchNZB(context.Context, core.Release) ([]byte, error) {
	return nil, core.ErrNotFound
}

func TestKindResolverDebridCachedBy(t *testing.T) {
	ctx := context.Background()
	hashBoth := strings.Repeat("aa", 20)
	hashTorBox := strings.Repeat("bb", 20)
	hashMiss := strings.Repeat("cc", 20)

	provs := map[string]*stubDebridProvider{
		"pm-main": {
			kind:   "premiumize",
			cached: map[string]bool{hashBoth: true},
			files:  []debrid.File{{ID: "f1", Path: "Movie.mkv", Size: 1024}},
		},
		"tb-alt": {
			kind:   "torbox",
			cached: map[string]bool{hashBoth: true, hashTorBox: true},
			files:  []debrid.File{{ID: "f2", Path: "Movie.mkv", Size: 1024}},
		},
	}
	svc := debrid.NewService(config.Debrid{
		CachedOnly: true,
		Providers: []config.DebridProvider{
			{ID: "tb-alt", Kind: "torbox", Priority: 20, Enabled: true, APIKey: "secret-tb"},
			{ID: "pm-main", Kind: "premiumize", Name: "Primary PM", Priority: 10, Enabled: true, APIKey: "secret-pm"},
		},
	}, debrid.Options{
		NewProvider: func(pc config.DebridProvider, _ debrid.ProviderOptions) (debrid.Provider, error) {
			return provs[pc.ID], nil
		},
	})

	kr := NewKindResolver(KindResolverDeps{
		Debrid: svc,
		Indexer: staticIndexer{rels: []core.Release{
			{ID: "r1", Name: "Example.2026.1080p.WEB-DL", Protocol: core.ProtocolTorrent, InfoHash: hashBoth, Indexer: "Idx", IndexerID: "idx-1"},
			{ID: "r2", Name: "Example.Hint.1080p.WEB-DL", Protocol: core.ProtocolTorrent, InfoHash: hashTorBox, DebridCached: []string{"torbox"}, Indexer: "Addon", IndexerID: "addon-1"},
			{ID: "r3", Name: "Example.Miss.1080p.WEB-DL", Protocol: core.ProtocolTorrent, InfoHash: hashMiss, Indexer: "Idx", IndexerID: "idx-1"},
		}},
	})

	// 1. debrid_check returns cachedBy in acquisition priority order and omits it when uncached.
	got, err := kr.ResolveSources(ctx, "plug.test", core.SourceQuery{
		Kind:       "debrid_check",
		InfoHashes: []string{strings.ToUpper(hashBoth), hashTorBox, hashMiss},
	})
	if err != nil {
		t.Fatalf("debrid_check: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("debrid_check len = %d, want 3", len(got))
	}
	wantBoth := []core.DebridServiceRef{
		{ID: "pm-main", Name: "Primary PM"},
		{ID: "tb-alt", Name: "TorBox"},
	}
	if !got[0].Cached || len(got[0].CachedBy) != 2 || got[0].CachedBy[0] != wantBoth[0] || got[0].CachedBy[1] != wantBoth[1] {
		t.Fatalf("hashBoth = %+v, want cachedBy %+v", got[0], wantBoth)
	}
	if !got[1].Cached || len(got[1].CachedBy) != 1 || got[1].CachedBy[0] != (core.DebridServiceRef{ID: "tb-alt", Name: "TorBox"}) {
		t.Fatalf("hashTorBox = %+v", got[1])
	}
	if got[2].Cached || len(got[2].CachedBy) != 0 {
		t.Fatalf("hashMiss = %+v, want uncached with empty cachedBy", got[2])
	}
	rawMiss, _ := json.Marshal(got[2])
	if strings.Contains(string(rawMiss), "cachedBy") {
		t.Fatalf("uncached candidate must omit cachedBy on wire: %s", rawMiss)
	}
	rawAll, _ := json.Marshal(got)
	if strings.Contains(string(rawAll), "secret-") {
		t.Fatalf("wire payload leaked API key: %s", rawAll)
	}

	// 2. indexer_search populates cached + cachedBy from Ether's known cache and addon hints.
	searched, err := kr.ResolveSources(ctx, "plug.test", core.SourceQuery{
		Kind:  "indexer_search",
		Query: "Example",
	})
	if err != nil {
		t.Fatalf("indexer_search: %v", err)
	}
	if len(searched) != 3 {
		t.Fatalf("indexer_search len = %d, want 3", len(searched))
	}
	if !searched[0].Cached || len(searched[0].CachedBy) != 2 || searched[0].CachedBy[0] != wantBoth[0] || searched[0].CachedBy[1] != wantBoth[1] {
		t.Fatalf("indexer_search[0] = %+v, want cachedBy %+v", searched[0], wantBoth)
	}
	if !searched[1].Cached || len(searched[1].CachedBy) != 1 || searched[1].CachedBy[0] != (core.DebridServiceRef{ID: "tb-alt", Name: "TorBox"}) {
		t.Fatalf("indexer_search[1] = %+v", searched[1])
	}
	if searched[2].Cached || len(searched[2].CachedBy) != 0 {
		t.Fatalf("indexer_search[2] = %+v, want uncached", searched[2])
	}

	// 3. debrid_acquire reports the service that actually served the torrent.
	acq, err := kr.ResolveSources(ctx, "plug.test", core.SourceQuery{
		Kind:     "debrid_acquire",
		InfoHash: hashBoth,
	})
	if err != nil {
		t.Fatalf("debrid_acquire: %v", err)
	}
	if len(acq) != 1 || !acq[0].Cached || len(acq[0].CachedBy) != 1 || acq[0].CachedBy[0] != (core.DebridServiceRef{ID: "pm-main", Name: "Primary PM"}) {
		t.Fatalf("debrid_acquire = %+v, want served by pm-main", acq)
	}
}
