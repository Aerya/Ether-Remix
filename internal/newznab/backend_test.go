package newznab

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type fakeBackend struct {
	searches, fetches, resolves int
	cfg                         config.Indexer
}

func (f *fakeBackend) Search(ctx context.Context, req core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error) {
	if err := reserve(ctx, "hit"); err != nil {
		return nil, err
	}
	f.searches++
	return []core.Release{{GUID: "g1", Name: "Film.2020.1080p.WEB-DL", NZBURL: "https://site.invalid/nzb/1"}}, nil
}

func (f *fakeBackend) FetchNZB(ctx context.Context, r core.Release, reserve func(context.Context, string) error) ([]byte, error) {
	if err := reserve(ctx, "grab"); err != nil {
		return nil, err
	}
	f.fetches++
	return []byte("<nzb></nzb>"), nil
}

func (f *fakeBackend) ResolveTorrent(ctx context.Context, r core.Release, reserve func(context.Context, string) error) (core.Release, error) {
	if err := reserve(ctx, "grab"); err != nil {
		return r, err
	}
	f.resolves++
	r.InfoHash = "0123456789abcdef0123456789abcdef01234567"
	return r, nil
}

func TestBuiltinBackendSharesBudgetsCacheAndRouting(t *testing.T) {
	ctx := context.Background()
	fb := &fakeBackend{}
	dir := t.TempDir()
	cfg := config.Indexer{ID: "site", Name: "Site", URL: "https://site.invalid", Kind: config.IndexerKindCardigann, Definition: "fake", Enabled: true, DailyLimit: 1, DailyGrabs: 1}
	m := NewManager(nil, []config.Indexer{cfg}, Options{NZBDir: dir, Backend: func(c config.Indexer) (Backend, error) {
		fb.cfg = c
		return fb, nil
	}})
	req := core.SearchRequest{Type: core.TypeMovie, Title: "Film", Year: 2020}
	rels, err := m.Search(ctx, req)
	if err != nil || len(rels) != 1 {
		t.Fatalf("search: %v %v", rels, err)
	}
	r := rels[0]
	if r.Indexer != "Site" || r.ID == "" || r.Info.Height != 1080 || fb.cfg.Definition != "fake" {
		t.Fatalf("stamping = %+v", r)
	}
	if _, err := m.Search(ctx, req); err != nil || fb.searches != 1 {
		t.Fatalf("cache not used: %d %v", fb.searches, err)
	}
	if _, err := m.Search(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "Other"}); !errors.Is(err, core.ErrBudgetExhausted) {
		t.Fatalf("hit budget not enforced: %v", err)
	}
	body, err := m.FetchNZB(ctx, r)
	if err != nil || string(body) != "<nzb></nzb>" {
		t.Fatalf("fetch: %q %v", body, err)
	}
	if _, err := os.Stat(m.Clients()[0].nzbDiskPath(r.ID)); err != nil {
		t.Fatalf("NZB not kept on disk: %v", err)
	}
	// The stored NZB is reused without spending the (exhausted) grab budget.
	if _, err := m.FetchNZB(ctx, r); err != nil || fb.fetches != 1 {
		t.Fatalf("refetch: %d %v", fb.fetches, err)
	}
	torrent := core.Release{Indexer: "Site", Name: "T", Protocol: core.ProtocolTorrent, NZBURL: "https://site.invalid/t/1"}
	if _, err := m.ResolveTorrent(ctx, torrent); !errors.Is(err, core.ErrBudgetExhausted) {
		t.Fatalf("grab budget not enforced on torrents: %v", err)
	}
}

func TestBuiltinWithoutBackendFails(t *testing.T) {
	cfg := config.Indexer{ID: "site", Name: "Site", Kind: config.IndexerKindCardigann, Enabled: true}
	c := New(cfg, nil, Options{})
	if _, err := c.Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "x"}); err == nil {
		t.Fatal("built-in indexer searched without a backend")
	}
}

func TestBuiltinTVSearchDoesNotReuseOldYearKeywordAnswers(t *testing.T) {
	ctx := context.Background()
	fb := &fakeBackend{}
	cfg := config.Indexer{ID: "site", Name: "Site", Kind: config.IndexerKindCardigann, Definition: "fake", Enabled: true}
	c := New(cfg, nil, Options{Backend: func(config.Indexer) (Backend, error) { return fb, nil }})
	req := core.SearchRequest{Type: core.TypeEpisode, Title: "Show Name", Year: 1996, Season: 2, Episode: 3}
	// Non-built-in keys retain the legacy format. Seed a persisted answer
	// made by the earlier TV query, then exercise the normal client path.
	legacy := New(config.Indexer{ID: cfg.ID}, nil, Options{})
	c.cachePut(ctx, cacheKey(legacy, req), []core.Release{{GUID: "old-answer"}})
	got, err := c.Search(ctx, req)
	if err != nil || fb.searches != 1 || len(got) != 1 || got[0].GUID == "old-answer" {
		t.Fatalf("reused stale TV query: searches=%d got=%+v err=%v", fb.searches, got, err)
	}
	if _, err := c.Search(ctx, req); err != nil || fb.searches != 1 {
		t.Fatal("corrected TV answer was not cached")
	}
}

type alkoBackend struct{ fakeBackend }

func (alkoBackend) Search(ctx context.Context, req core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error) {
	if err := reserve(ctx, "hit"); err != nil {
		return nil, err
	}
	return []core.Release{
		{ID: "sfr-alko:9", GUID: "sfr-alko:9", Name: "Film.2020.1080p.WEB-DL", Protocol: core.ProtocolHTTP, Indexer: "StreamFusion Reborn (Alkodiques)",
			StreamURL: "https://alldebrid.com/f/abc9", StreamDebrid: "alldebrid", StreamOnDemand: true},
		{ID: "t1", GUID: "t1", Name: "Film.2020.1080p.BluRay", Protocol: core.ProtocolTorrent, Indexer: "Autre", InfoHash: "0123456789abcdef0123456789abcdef01234567"},
	}, nil
}

func TestBackendStampKeepsVerifiedFileLabel(t *testing.T) {
	cfg := config.Indexer{ID: "sfr", Name: "StreamFusion Reborn (base)", URL: "https://site.invalid", Kind: config.IndexerKindCardigann, Definition: "fake", Enabled: true}
	m := NewManager(nil, []config.Indexer{cfg}, Options{NZBDir: t.TempDir(), Backend: func(config.Indexer) (Backend, error) { return &alkoBackend{}, nil }})
	rels, err := m.Search(context.Background(), core.SearchRequest{Type: core.TypeMovie, Title: "Film", Year: 2020})
	if err != nil || len(rels) != 2 {
		t.Fatalf("search: %v %v", rels, err)
	}
	for _, r := range rels {
		wantLabel := "StreamFusion Reborn (base)"
		if core.IsVerifiedFile(r) {
			wantLabel = "StreamFusion Reborn (Alkodiques)"
		}
		if r.Indexer != wantLabel {
			t.Errorf("%s: libellé %q, attendu %q", r.ID, r.Indexer, wantLabel)
		}
		if r.IndexerID == "" {
			t.Errorf("%s: IndexerID perdu", r.ID)
		}
	}
}
