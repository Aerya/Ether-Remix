package adapters_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/resolver"
)

type kindFailoverIndexer struct {
	nzbPayloads map[string][]byte
	searchRels  []core.Release
}

func (i *kindFailoverIndexer) Name() string { return "kind-failover-idx" }

func (i *kindFailoverIndexer) Search(_ context.Context, _ core.SearchRequest) ([]core.Release, error) {
	return append([]core.Release(nil), i.searchRels...), nil
}

func (i *kindFailoverIndexer) FetchNZB(_ context.Context, rel core.Release) ([]byte, error) {
	if b, ok := i.nzbPayloads[rel.NZBURL]; ok {
		return b, nil
	}
	return []byte("nzb-default"), nil
}

type kindFailoverDebrid struct {
	payloads map[string][]byte
}

func (d *kindFailoverDebrid) CheckCached(_ context.Context, hashes []string) (map[string]bool, bool) {
	out := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		out[h] = true
	}
	return out, true
}

func (d *kindFailoverDebrid) Acquire(_ context.Context, rel core.Release) ([]byte, error) {
	if b, ok := d.payloads[rel.InfoHash]; ok {
		return b, nil
	}
	return []byte("torbox-default"), nil
}

func (d *kindFailoverDebrid) ListReleaseFiles(_ context.Context, _ core.Release) ([]core.ReleaseFile, error) {
	return nil, core.ErrNotCached
}

type kindFailoverEngine struct {
	core.Engine
}

func (kindFailoverEngine) Open(_ context.Context, id string, data []byte) (core.MediaSource, error) {
	return &kindContextMediaSource{id: id, data: append([]byte(nil), data...)}, nil
}

type kindContextMediaSource struct {
	id         string
	data       []byte
	ctxReads   atomic.Int32
	plainReads atomic.Int32
}

func (s *kindContextMediaSource) ReleaseID() string { return s.id }
func (s *kindContextMediaSource) Name() string      { return s.id + ".mkv" }
func (s *kindContextMediaSource) Size() int64       { return int64(len(s.data)) }
func (s *kindContextMediaSource) Close() error      { return nil }
func (s *kindContextMediaSource) ReadAt(p []byte, off int64) (int, error) {
	s.plainReads.Add(1)
	return bytes.NewReader(s.data).ReadAt(p, off)
}
func (s *kindContextMediaSource) ReadAtContext(ctx context.Context, p []byte, off int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.ctxReads.Add(1)
	return bytes.NewReader(s.data).ReadAt(p, off)
}

func TestKindResolverCandidateFailoverCacheReResolveAndUsenetBackoff(t *testing.T) {
	h := newKindTestHarness(t, 0, nil)
	ctx := context.Background()

	var nowMu sync.Mutex
	curNow := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	nowFn := func() time.Time {
		nowMu.Lock()
		defer nowMu.Unlock()
		return curNow
	}
	advance := func(d time.Duration) {
		nowMu.Lock()
		curNow = curNow.Add(d)
		nowMu.Unlock()
	}

	var usenetServing atomic.Bool
	usenetServing.Store(true)

	const (
		nzbURL     = "https://media.example/releases/usenet-1.nzb"
		torboxHash = "0123456789abcdef0123456789abcdef01234567"
	)
	usenetBytes := []byte("USENET-STREAM-PAYLOAD-BYTES")
	torboxBytes := []byte("TORBOX-STREAM-PAYLOAD-BYTES-ALT")

	idx := &kindFailoverIndexer{
		nzbPayloads: map[string][]byte{nzbURL: usenetBytes},
		searchRels: []core.Release{
			{
				ID:       "search-usenet",
				Name:     "Concert.2025.1080p.WEB-DL.DDP5.1.H.264-USENET",
				Protocol: core.ProtocolUsenet,
				NZBURL:   nzbURL,
				Grabs:    5000,
				Size:     4 << 30,
			},
			{
				ID:       "search-torbox",
				Name:     "Concert.2025.1080p.WEB-DL.DDP5.1.H.264-TORBOX",
				Protocol: core.ProtocolTorrent,
				InfoHash: torboxHash,
				Seeders:  10,
				Size:     4 << 30,
			},
		},
	}
	deb := &kindFailoverDebrid{
		payloads: map[string][]byte{torboxHash: torboxBytes},
	}

	defCfg := config.Defaults("")
	resSvc := resolver.New(idx, nil, nil, defCfg, resolver.Options{
		UsenetServing: usenetServing.Load,
	})
	kr := adapters.NewKindResolver(adapters.KindResolverDeps{
		Manager:       h.mgr,
		Libraries:     h.libs,
		Items:         h.items,
		Blobs:         h.blobs,
		Catalog:       h.catalog,
		Indexer:       idx,
		Resolver:      resSvc,
		Debrid:        deb,
		Engine:        kindFailoverEngine{},
		UsenetServing: usenetServing.Load,
		Now:           nowFn,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	var pluginResolved atomic.Bool
	h.rt.callContext = func(_ context.Context, req plugins.Request) (plugins.Response, error) {
		switch req.Path {
		case "/ether/health":
			return plugins.Response{Status: http.StatusOK, Body: json.RawMessage(`{"status":"ok"}`)}, nil
		case "/ether/v1/kinds/recordings/resolve":
			pluginResolved.Store(true)
			resp := core.KindResolveResponse{
				TTLSec: 600,
				Sources: []core.PlayableSource{
					{
						Pattern: "release",
						MIME:    "video/x-matroska",
						Release: &core.ReleaseHint{
							FileHint: "Concert.2025.1080p.WEB-DL.H264-USENET.mkv",
							NZBURL:   nzbURL,
						},
					},
					{
						Pattern: "release",
						MIME:    "video/x-matroska",
						Release: &core.ReleaseHint{
							FileHint: "Concert.2025.1080p.WEB-DL.H264-TORBOX.mkv",
							InfoHash: torboxHash,
						},
					},
				},
			}
			raw, _ := json.Marshal(resp)
			return plugins.Response{Status: http.StatusOK, Body: raw}, nil
		default:
			return plugins.Response{Status: http.StatusOK, Body: json.RawMessage(`{"items":[]}`)}, nil
		}
	}

	const pluginID = "community.example.kindfailover"
	decl := recordingsKindDecl(true)
	decl["sync"] = map[string]any{"mode": "push"}
	installStudioManifest(t, h.mgr, map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Kind Failover Test",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Kind failover test",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"permissions":  map[string]any{"network": []string{"media.example"}},
			"strings":      map[string]any{"en": "i18n/en.json"},
			"contributes":  map[string]any{"mediaKinds": []map[string]any{decl}},
		},
	}, map[string][]byte{"i18n/en.json": []byte(`{"lib_name":"Recordings"}`)})

	host := h.mgr.HostFor(pluginID)
	if _, err := host.KindUpsert(ctx, "recordings", []core.KindItem{
		{ID: "alb-1", Level: "album", Title: "Album 1"},
		{ID: "trk-1", Level: "track", ParentID: "alb-1", Index: 1, Title: "Track 1", Playable: true},
		{ID: "trk-2", Level: "track", ParentID: "alb-1", Index: 2, Title: "Track 2", Playable: true},
	}); err != nil {
		t.Fatalf("KindUpsert: %v", err)
	}
	trk1, _, err := h.items.GetByExternal(ctx, pluginID, "recordings", "trk-1")
	if err != nil {
		t.Fatalf("GetByExternal(trk-1): %v", err)
	}
	trk2, _, err := h.items.GetByExternal(ctx, pluginID, "recordings", "trk-2")
	if err != nil {
		t.Fatalf("GetByExternal(trk-2): %v", err)
	}
	viewer := &core.Viewer{ID: 1, IsOwner: true}

	// 1. Initial OpenKind when Usenet is healthy opens the Usenet candidate and
	// preserves the unwrapped core.MediaSource (including ContextReaderAt).
	m1, err := kr.OpenKind(ctx, viewer, trk1.Key, nil)
	if err != nil {
		t.Fatalf("OpenKind(trk-1): %v", err)
	}
	defer m1.Closer.Close()
	if m1.Release.Protocol != core.ProtocolUsenet || m1.Release.ID == "" {
		t.Fatalf("m1.Release = %+v, want Usenet release with non-empty ID", m1.Release)
	}
	if _, ok := m1.MediaSource.(core.ContextReaderAt); !ok {
		t.Fatalf("m1.MediaSource = %T, want unwrapped core.ContextReaderAt", m1.MediaSource)
	}
	got1, err := io.ReadAll(io.NewSectionReader(m1.ReaderAt, 0, m1.Size))
	if err != nil || !bytes.Equal(got1, usenetBytes) {
		t.Fatalf("m1 bytes = %q (%v), want %q", got1, err, usenetBytes)
	}
	if !pluginResolved.Load() {
		t.Fatal("expected initial OpenKind to invoke plugin /resolve")
	}

	// 2. OpenKindExcluding fails over to TorBox from the stored candidate list
	// of the SAME kind resolve without calling the plugin's /resolve again.
	pluginResolved.Store(false)
	m2, err := kr.OpenKindExcluding(ctx, viewer, trk1.Key, nil, []string{m1.Release.ID})
	if err != nil {
		t.Fatalf("OpenKindExcluding(stored candidate list): %v", err)
	}
	defer m2.Closer.Close()
	if m2.Release.Protocol != core.ProtocolTorrent || m2.Release.ID == m1.Release.ID {
		t.Fatalf("m2.Release = %+v, want TorBox torrent candidate", m2.Release)
	}
	got2, err := io.ReadAll(io.NewSectionReader(m2.ReaderAt, 0, m2.Size))
	if err != nil || !bytes.Equal(got2, torboxBytes) {
		t.Fatalf("m2 bytes = %q (%v), want %q", got2, err, torboxBytes)
	}
	if pluginResolved.Load() {
		t.Fatal("stored candidate list was not reused: plugin /resolve was invoked during failover")
	}

	// 3. Once the stored candidate list expires, OpenKindExcluding re-resolves via
	// the plugin while still excluding the failed Usenet release.
	advance(11 * time.Minute)
	m3, err := kr.OpenKindExcluding(ctx, viewer, trk1.Key, nil, []string{m1.Release.ID})
	if err != nil {
		t.Fatalf("OpenKindExcluding(re-resolve after expiry): %v", err)
	}
	defer m3.Closer.Close()
	if m3.Release.Protocol != core.ProtocolTorrent {
		t.Fatalf("m3.Release = %+v, want TorBox torrent candidate on re-resolve", m3.Release)
	}
	if !pluginResolved.Load() {
		t.Fatal("expected plugin re-resolve after stored candidate TTL expiry")
	}

	// 4. While Usenet is backing off (481 simultaneous IP addresses), new picks
	// skip the Usenet candidate up front on both OpenKind and ResolveSources.
	usenetServing.Store(false)
	m4, err := kr.OpenKind(ctx, viewer, trk2.Key, nil)
	if err != nil {
		t.Fatalf("OpenKind(trk-2) during Usenet backoff: %v", err)
	}
	defer m4.Closer.Close()
	if m4.Release.Protocol != core.ProtocolTorrent {
		t.Fatalf("OpenKind during Usenet backoff picked %+v, want TorBox torrent candidate", m4.Release)
	}

	searchCands, err := kr.ResolveSources(ctx, pluginID, core.SourceQuery{
		Kind:  "indexer_search",
		Query: "Concert 2025",
	})
	if err != nil || len(searchCands) < 2 {
		t.Fatalf("ResolveSources during Usenet backoff = (%+v, %v)", searchCands, err)
	}
	if searchCands[0].InfoHash != torboxHash || searchCands[1].NZBURL != nzbURL {
		t.Fatalf("ResolveSources order during Usenet backoff = [%+v, %+v], want TorBox candidate before Usenet candidate",
			searchCands[0], searchCands[1])
	}
}

// When candidate 0 fails because the plugin's own answer was wrong (e.g. wrong
// digest or missing explicit file) and candidate 1 opens as fallback, neither
// the fallback resolution nor the broken candidate list may stick: the next
// play must call /resolve again (#641).
func TestKindResolverPluginAnswerFallbackDropsCandidateCacheSoNextPlayReResolves(t *testing.T) {
	h := newKindTestHarness(t, 0, nil)
	ctx := context.Background()

	const (
		hashBad  = "1111111111111111111111111111111111111111"
		hashGood = "2222222222222222222222222222222222222222"
	)
	deb := &kindFailoverDebrid{
		payloads: map[string][]byte{
			hashBad:  []byte("FIRST-CANDIDATE-BYTES"),
			hashGood: []byte("FALLBACK-CANDIDATE-BYTES"),
		},
	}
	kr := adapters.NewKindResolver(adapters.KindResolverDeps{
		Manager:   h.mgr,
		Libraries: h.libs,
		Items:     h.items,
		Blobs:     h.blobs,
		Catalog:   h.catalog,
		Debrid:    deb,
		Engine:    kindFailoverEngine{},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	var resolveCalls atomic.Int32
	h.rt.callContext = func(_ context.Context, req plugins.Request) (plugins.Response, error) {
		switch req.Path {
		case "/ether/health":
			return plugins.Response{Status: http.StatusOK, Body: json.RawMessage(`{"status":"ok"}`)}, nil
		case "/ether/v1/kinds/recordings/resolve":
			n := resolveCalls.Add(1)
			var resp core.KindResolveResponse
			if n == 1 {
				// First resolve: candidate 0 has a wrong CRC32 (ErrPluginAnswer),
				// candidate 1 opens as fallback.
				resp = core.KindResolveResponse{
					TTLSec: 600,
					Sources: []core.PlayableSource{
						{
							ID:      "broken-primary",
							Pattern: "release",
							MIME:    "application/octet-stream",
							CRC32:   "00000000",
							Release: &core.ReleaseHint{InfoHash: hashBad, File: "primary.bin"},
						},
						{
							ID:      "fallback",
							Pattern: "release",
							MIME:    "application/octet-stream",
							Release: &core.ReleaseHint{InfoHash: hashGood, File: "fallback.bin"},
						},
					},
				}
			} else {
				// Second resolve (after fix): candidate 0 now succeeds cleanly.
				resp = core.KindResolveResponse{
					TTLSec: 600,
					Sources: []core.PlayableSource{
						{
							ID:      "fixed-primary",
							Pattern: "release",
							MIME:    "application/octet-stream",
							Release: &core.ReleaseHint{InfoHash: hashBad, File: "primary.bin"},
						},
					},
				}
			}
			raw, _ := json.Marshal(resp)
			return plugins.Response{Status: http.StatusOK, Body: raw}, nil
		default:
			return plugins.Response{Status: http.StatusOK, Body: json.RawMessage(`{"items":[]}`)}, nil
		}
	}

	const pluginID = "community.example.kind641"
	decl := recordingsKindDecl(true)
	decl["sync"] = map[string]any{"mode": "push"}
	installStudioManifest(t, h.mgr, map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Kind 641 Test",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Kind 641 test",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"strings":      map[string]any{"en": "i18n/en.json"},
			"contributes":  map[string]any{"mediaKinds": []map[string]any{decl}},
		},
	}, map[string][]byte{"i18n/en.json": []byte(`{"lib_name":"Recordings"}`)})

	host := h.mgr.HostFor(pluginID)
	if _, err := host.KindUpsert(ctx, "recordings", []core.KindItem{
		{ID: "alb-1", Level: "album", Title: "Album 1"},
		{ID: "trk-1", Level: "track", ParentID: "alb-1", Index: 1, Title: "Track 1", Playable: true},
	}); err != nil {
		t.Fatalf("KindUpsert: %v", err)
	}
	trk1, _, err := h.items.GetByExternal(ctx, pluginID, "recordings", "trk-1")
	if err != nil {
		t.Fatalf("GetByExternal: %v", err)
	}
	viewer := &core.Viewer{ID: 1, IsOwner: true}

	// First play serves the fallback after candidate 0 fails on ErrPluginAnswer.
	m1, err := kr.OpenKind(ctx, viewer, trk1.Key, nil)
	if err != nil {
		t.Fatalf("first OpenKind: %v", err)
	}
	got1, _ := io.ReadAll(io.NewSectionReader(m1.ReaderAt, 0, m1.Size))
	_ = m1.Closer.Close()
	if string(got1) != "FALLBACK-CANDIDATE-BYTES" {
		t.Fatalf("first play served %q, want fallback", got1)
	}
	if _, ok, err := h.items.GetPartResolution(ctx, trk1.Ref, nil); err != nil || ok {
		t.Fatalf("fallback resolution was saved: ok=%v err=%v", ok, err)
	}
	if _, ok, err := h.items.GetPartCandidates(ctx, trk1.Ref, nil); err != nil || ok {
		t.Fatalf("broken candidates remained persisted: ok=%v err=%v", ok, err)
	}

	// Second play must call /resolve again (not replay cached candidates) and
	// now open the fixed primary source.
	m2, err := kr.OpenKind(ctx, viewer, trk1.Key, nil)
	if err != nil {
		t.Fatalf("second OpenKind: %v", err)
	}
	got2, _ := io.ReadAll(io.NewSectionReader(m2.ReaderAt, 0, m2.Size))
	_ = m2.Closer.Close()
	if resolveCalls.Load() != 2 {
		t.Fatalf("resolve calls = %d, want 2 (broken candidate cache was not dropped)", resolveCalls.Load())
	}
	if string(got2) != "FIRST-CANDIDATE-BYTES" {
		t.Fatalf("second play served %q, want fixed primary", got2)
	}
}
