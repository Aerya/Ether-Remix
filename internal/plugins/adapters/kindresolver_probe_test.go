package adapters_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
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

func buildAdapterMKV(durationSec float64, width, height uint64) []byte {
	encID := func(id uint32) []byte {
		switch {
		case id <= 0xff:
			return []byte{byte(id)}
		case id <= 0xffff:
			return []byte{byte(id >> 8), byte(id)}
		case id <= 0xffffff:
			return []byte{byte(id >> 16), byte(id >> 8), byte(id)}
		default:
			return []byte{byte(id >> 24), byte(id >> 16), byte(id >> 8), byte(id)}
		}
	}
	encSize := func(n int) []byte {
		switch {
		case n < 0x7f:
			return []byte{byte(0x80 | n)}
		case n < 0x3fff:
			return []byte{byte(0x40 | (n >> 8)), byte(n)}
		default:
			return []byte{byte(0x20 | (n >> 16)), byte(n >> 8), byte(n)}
		}
	}
	el := func(id uint32, body ...[]byte) []byte {
		joined := bytes.Join(body, nil)
		out := append(encID(id), encSize(len(joined))...)
		return append(out, joined...)
	}
	elU := func(id uint32, v uint64) []byte {
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], v)
		i := 0
		for i < 7 && buf[i] == 0 {
			i++
		}
		return el(id, buf[i:])
	}
	elS := func(id uint32, s string) []byte { return el(id, []byte(s)) }
	elF := func(id uint32, v float64) []byte {
		var buf [8]byte
		binary.BigEndian.PutUint64(buf[:], math.Float64bits(v))
		return el(id, buf[:])
	}
	av1Config := []byte{0x81, 0x04, 0x0c, 0x00}
	colour := el(0x55b0, elU(0x55ba, 1))
	video := el(0xe0, elU(0xb0, width), elU(0xba, height), colour)
	track := el(0xae,
		elU(0xd7, 1), elU(0x83, 1), elS(0x86, "V_AV1"),
		el(0x63a2, av1Config), elU(0x23e383, 41_708_333), video,
	)
	info := el(0x1549a966, elU(0x2ad7b1, 1_000_000), elF(0x4489, durationSec*1000))
	return append(el(0x1a45dfa3, elS(0x4282, "matroska")), el(0x18538067, info, el(0x1654ae6b, track))...)
}

type memMediaProbeCache struct {
	mu      sync.Mutex
	entries map[string]core.MediaInfo
	hits    atomic.Int32
	records atomic.Int32
}

func newMemMediaProbeCache() *memMediaProbeCache {
	return &memMediaProbeCache{entries: map[string]core.MediaInfo{}}
}

func (c *memMediaProbeCache) releaseKey(rel core.Release) string {
	base := strings.ToLower(strings.TrimSpace(rel.InfoHash))
	if base != "" {
		return rel.ScopedContentID("bt:" + base)
	}
	if id := strings.TrimSpace(rel.ID); id != "" {
		return rel.ScopedContentID("id:" + id)
	}
	return ""
}

func (c *memMediaProbeCache) CachedReleaseProbe(rel core.Release) (core.MediaInfo, bool) {
	k := c.releaseKey(rel)
	if k == "" {
		return core.MediaInfo{}, false
	}
	return c.CachedProbeByKeys([]string{k})
}

func (c *memMediaProbeCache) RecordReleaseProbe(ctx context.Context, rel core.Release, _ []byte, info core.MediaInfo) {
	if k := c.releaseKey(rel); k != "" {
		c.RecordProbeByKeys(ctx, []string{k}, info)
	}
}

func (c *memMediaProbeCache) CachedProbeByKeys(keys []string) (core.MediaInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range keys {
		if info, ok := c.entries[k]; ok {
			c.hits.Add(1)
			return info, true
		}
	}
	return core.MediaInfo{}, false
}

func (c *memMediaProbeCache) RecordProbeByKeys(_ context.Context, keys []string, info core.MediaInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records.Add(1)
	for _, k := range keys {
		if k != "" {
			c.entries[k] = info
		}
	}
}

type packDebrid struct {
	files        map[int][]byte
	cached       bool
	acquireCalls atomic.Int32
}

func (d *packDebrid) CheckCached(_ context.Context, hashes []string) (map[string]bool, bool) {
	out := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		out[h] = d.cached
	}
	return out, true
}

func (d *packDebrid) Acquire(_ context.Context, rel core.Release) ([]byte, error) {
	d.acquireCalls.Add(1)
	if !d.cached {
		return nil, core.ErrNotCached
	}
	idx := 0
	if rel.Target.FileIndex != nil {
		idx = *rel.Target.FileIndex
	}
	if b, ok := d.files[idx]; ok {
		return b, nil
	}
	return nil, core.ErrNotFound
}

func (d *packDebrid) ListReleaseFiles(_ context.Context, _ core.Release) ([]core.ReleaseFile, error) {
	return nil, core.ErrNotCached
}

func TestKindResolverDurationVerifyPackIsolationAndProbeMedia(t *testing.T) {
	trailerMKV := buildAdapterMKV(90, 1280, 720)
	episodeMKV := buildAdapterMKV(2400, 1920, 1080)

	var urlReads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		urlReads.Add(1)
		switch r.URL.Path {
		case "/secret-ep.mkv":
			http.ServeContent(w, r, "secret-ep.mkv", time.Time{}, bytes.NewReader(episodeMKV))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	httpClient := &http.Client{Transport: hostRewriteTransport{target: srv.URL}}
	h := newKindTestHarness(t, 0, httpClient)
	ctx := context.Background()

	const packHash = "abcdef0123456789abcdef0123456789abcdef01"
	deb := &packDebrid{
		cached: true,
		files: map[int][]byte{
			0: trailerMKV,
			1: episodeMKV,
		},
	}
	probeCache := newMemMediaProbeCache()
	defCfg := config.Defaults("")
	resSvc := resolver.New(nil, nil, nil, defCfg, resolver.Options{})

	kr := adapters.NewKindResolver(adapters.KindResolverDeps{
		Manager:    h.mgr,
		Libraries:  h.libs,
		Items:      h.items,
		Blobs:      h.blobs,
		Catalog:    h.catalog,
		Resolver:   resSvc,
		Debrid:     deb,
		Engine:     kindFailoverEngine{},
		HTTP:       httpClient,
		ProbeCache: probeCache,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	file0, file1 := 0, 1
	var resolveCalls atomic.Int32
	h.rt.callContext = func(_ context.Context, req plugins.Request) (plugins.Response, error) {
		switch req.Path {
		case "/ether/health":
			return plugins.Response{Status: http.StatusOK, Body: json.RawMessage(`{"status":"ok"}`)}, nil
		case "/ether/v1/kinds/recordings/resolve":
			resolveCalls.Add(1)
			resp := core.KindResolveResponse{
				TTLSec: 600,
				Sources: []core.PlayableSource{
					{
						ID:                   "pack-trailer",
						Pattern:              "release",
						MIME:                 "video/x-matroska",
						DurationSec:          2400,
						DurationToleranceSec: 60,
						Release: &core.ReleaseHint{
							InfoHash:  packHash,
							FileIndex: &file0,
						},
					},
					{
						ID:                   "pack-episode",
						Pattern:              "release",
						MIME:                 "video/x-matroska",
						DurationSec:          2400,
						DurationToleranceSec: 60,
						Release: &core.ReleaseHint{
							InfoHash:  packHash,
							FileIndex: &file1,
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

	const allowedPluginID = "community.example.kindprobe"
	const ungrantedPluginID = "community.example.nohost"
	decl := recordingsKindDecl(true)
	decl["sync"] = map[string]any{"mode": "push"}
	installStudioManifest(t, h.mgr, map[string]any{
		"id":      allowedPluginID,
		"version": "1.0.0",
		"name":    "Kind Probe Test",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Kind probe test",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{"catalog_provider", "kind_resolver"},
			"permissions": map[string]any{
				"network": []string{"media.example"},
				"media":   "probe",
			},
			"strings":     map[string]any{"en": "i18n/en.json"},
			"contributes": map[string]any{"mediaKinds": []map[string]any{decl}},
		},
	}, map[string][]byte{"i18n/en.json": []byte(`{"lib_name":"Recordings"}`)})

	installStudioManifest(t, h.mgr, map[string]any{
		"id":      ungrantedPluginID,
		"version": "1.0.0",
		"name":    "No Network Host Plugin",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "extension",
			"summary":      "No network host permission",
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"capabilities": []string{},
			"permissions":  map[string]any{"media": "probe"},
		},
	}, nil)

	host := h.mgr.HostFor(allowedPluginID)
	if _, err := host.KindUpsert(ctx, "recordings", []core.KindItem{
		{ID: "alb-1", Level: "album", Title: "Pack"},
		{ID: "ep-1", Level: "track", ParentID: "alb-1", Index: 1, Title: "Episode 1", Playable: true},
	}); err != nil {
		t.Fatalf("KindUpsert: %v", err)
	}
	ep1, _, err := h.items.GetByExternal(ctx, allowedPluginID, "recordings", "ep-1")
	if err != nil {
		t.Fatalf("GetByExternal: %v", err)
	}
	viewer := &core.Viewer{ID: 1, IsOwner: true}

	// 1. OpenKind rejects fileIndex=0 (90s trailer) due to duration mismatch,
	// serves fileIndex=1 (2400s episode), and does NOT cache the fallback (#641)
	// so the next OpenKind re-invokes /resolve.
	m1, err := kr.OpenKind(ctx, viewer, ep1.Key, nil)
	if err != nil {
		t.Fatalf("OpenKind #1: %v", err)
	}
	got1, _ := io.ReadAll(io.NewSectionReader(m1.ReaderAt, 0, m1.Size))
	_ = m1.Closer.Close()
	if !bytes.Equal(got1, episodeMKV) {
		t.Fatalf("OpenKind #1 did not serve episode bytes")
	}
	if resolveCalls.Load() != 1 {
		t.Fatalf("resolveCalls = %d, want 1", resolveCalls.Load())
	}

	// Both pack files were probed and cached under DISTINCT keys (no collision!).
	probeCache.mu.Lock()
	entryCount := len(probeCache.entries)
	probeCache.mu.Unlock()
	if entryCount < 2 {
		t.Fatalf("probeCache entries = %d, want >= 2 distinct file-scoped entries for fileIndex 0 and 1", entryCount)
	}

	// Second OpenKind must re-resolve (#641: plugin-answer failure did not persist
	// fallback), and probe checks should hit the durable release_probes cache.
	m2, err := kr.OpenKind(ctx, viewer, ep1.Key, nil)
	if err != nil {
		t.Fatalf("OpenKind #2: %v", err)
	}
	_ = m2.Closer.Close()
	if resolveCalls.Load() != 2 {
		t.Fatalf("resolveCalls after #641 duration mismatch = %d, want 2 (fallback must not persist)", resolveCalls.Load())
	}
	if probeCache.hits.Load() == 0 {
		t.Fatal("expected second OpenKind to reuse cached release_probes entries")
	}

	// 2. ProbeMedia on fileIndex=0 vs fileIndex=1 returns their distinct durations
	// straight from release_probes without re-acquiring from debrid.
	beforeAcquire := deb.acquireCalls.Load()
	p0, err := kr.ProbeMedia(ctx, allowedPluginID, core.PlayableSource{
		Pattern: "release",
		MIME:    "video/x-matroska",
		Release: &core.ReleaseHint{InfoHash: packHash, FileIndex: &file0},
	})
	if err != nil {
		t.Fatalf("ProbeMedia(fileIndex=0): %v", err)
	}
	p1, err := kr.ProbeMedia(ctx, allowedPluginID, core.PlayableSource{
		Pattern: "release",
		MIME:    "video/x-matroska",
		Release: &core.ReleaseHint{InfoHash: packHash, FileIndex: &file1},
	})
	if err != nil {
		t.Fatalf("ProbeMedia(fileIndex=1): %v", err)
	}
	if deb.acquireCalls.Load() != beforeAcquire {
		t.Fatalf("ProbeMedia re-acquired release %d times instead of hitting release_probes cache", deb.acquireCalls.Load()-beforeAcquire)
	}
	if math.Abs(p0.DurationSec-90) > 1 || p0.Width != 1280 || p0.Height != 720 || p0.Container != "mkv" || p0.SizeBytes != int64(len(trailerMKV)) {
		t.Fatalf("p0 = %+v, want 90s 1280x720 mkv size=%d", p0, len(trailerMKV))
	}
	if math.Abs(p1.DurationSec-2400) > 1 || p1.Width != 1920 || p1.Height != 1080 || p1.Container != "mkv" || p1.SizeBytes != int64(len(episodeMKV)) {
		t.Fatalf("p1 = %+v, want 2400s 1920x1080 mkv size=%d", p1, len(episodeMKV))
	}

	// 3. ProbeMedia on a URL caches by hashed key (no raw URL/token in keys),
	// reuses cache on repeat, AND enforces AllowNetworkHost BEFORE cache lookup!
	urlSrc := core.PlayableSource{
		Pattern: "range",
		MIME:    "video/x-matroska",
		URL:     "https://media.example/secret-ep.mkv?token=topsecret",
	}
	pu1, err := kr.ProbeMedia(ctx, allowedPluginID, urlSrc)
	if err != nil {
		t.Fatalf("ProbeMedia(url): %v", err)
	}
	if math.Abs(pu1.DurationSec-2400) > 1 || pu1.Width != 1920 || pu1.Height != 1080 {
		t.Fatalf("pu1 = %+v", pu1)
	}
	readsAfterFirst := urlReads.Load()
	if _, err := kr.ProbeMedia(ctx, allowedPluginID, urlSrc); err != nil {
		t.Fatalf("ProbeMedia(url cached): %v", err)
	}
	if urlReads.Load() != readsAfterFirst {
		t.Fatalf("cached URL probe made extra HTTP reads: %d -> %d", readsAfterFirst, urlReads.Load())
	}
	// Verify no raw secret token leaked into cache keys.
	probeCache.mu.Lock()
	for k := range probeCache.entries {
		if strings.Contains(k, "topsecret") || strings.Contains(k, "secret-ep.mkv") {
			probeCache.mu.Unlock()
			t.Fatalf("raw URL/token leaked into release_probes key: %q", k)
		}
	}
	probeCache.mu.Unlock()
	// Ungranted plugin MUST be refused even though the URL is already in cache!
	if _, err := kr.ProbeMedia(ctx, ungrantedPluginID, urlSrc); !errors.Is(err, plugins.ErrPermissionDenied) {
		t.Fatalf("ungranted plugin ProbeMedia(cached URL) = %v, want ErrPermissionDenied", err)
	}

	// 4. ProbeMedia forces CachedOnly for uncached release sources.
	deb.cached = false
	if _, err := kr.ProbeMedia(ctx, allowedPluginID, core.PlayableSource{
		Pattern: "release",
		MIME:    "video/x-matroska",
		Release: &core.ReleaseHint{InfoHash: "1111111111111111111111111111111111111111"},
	}); err == nil {
		t.Fatal("expected uncached release ProbeMedia to fail without queuing download")
	}
}
