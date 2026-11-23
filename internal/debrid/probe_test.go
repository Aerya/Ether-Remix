package debrid

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestProbePayloadAndCachedFileMetadata(t *testing.T) {
	h := newHarness(t, 1<<20, config.DebridRealDebrid)
	rd := h.provs["realdebrid-0"]

	// 1. Unknown-cache torrent must not trigger any provider API call.
	uncached := torrentRelease("unknown-torrent")
	if b, ok := h.svc.ProbePayload(t.Context(), uncached); ok || len(b) != 0 {
		t.Fatalf("ProbePayload unexpectedly succeeded for unknown-cache torrent")
	}
	if n := rd.callCount("acquire"); n != 0 {
		t.Fatalf("unknown-cache torrent caused %d provider acquire calls", n)
	}

	// 2. On-demand HTTP streams must never be opened for background probes.
	onDemand := core.Release{
		ID:             "ondemand-1",
		Protocol:       core.ProtocolHTTP,
		StreamURL:      "https://addon.example/stream/1",
		StreamOnDemand: true,
	}
	if b, ok := h.svc.ProbePayload(t.Context(), onDemand); ok || len(b) != 0 {
		t.Fatalf("ProbePayload opened an on-demand HTTP stream")
	}

	// 3. Stored payload populates CachedFileMetadata in memory and is reused
	//    by ProbePayload with zero provider calls.
	cached := torrentRelease("cached-torrent")
	p := Payload{
		Key:       payloadKey(cached),
		ReleaseID: cached.ID,
		Provider:  "realdebrid-0",
		Kind:      config.DebridRealDebrid,
		InfoHash:  testHash,
		Path:      "Anime.2024.1080p.BluRay.DUAL.ENG.JPN.DDP5.1-GRP.mkv",
		Size:      4 << 30,
		URL:       "https://cdn.example/file.mkv",
	}
	h.svc.savePayload(t.Context(), p)

	meta, ok := h.svc.CachedFileMetadata(cached)
	if !ok || meta.Path != p.Path || meta.Size != p.Size {
		t.Fatalf("CachedFileMetadata = %+v ok=%v, want path %q size %d", meta, ok, p.Path, p.Size)
	}
	b, ok := h.svc.ProbePayload(t.Context(), cached)
	if !ok || len(b) == 0 {
		t.Fatal("ProbePayload did not reuse stored payload")
	}
	if n := rd.callCount("acquire"); n != 0 {
		t.Fatalf("reused stored payload caused %d provider calls", n)
	}

	// 4. Reloading Service from DB preloads CachedFileMetadata without API hits.
	reloaded := NewService(h.svc.Config(), Options{DB: h.svc.db})
	meta, ok = reloaded.CachedFileMetadata(cached)
	if !ok || meta.Path != p.Path {
		t.Fatalf("reloaded CachedFileMetadata = %+v ok=%v, want path %q", meta, ok, p.Path)
	}

	// 5. Background probe on a torrent NOT yet in the user's Real-Debrid account
	//    must NOT mutate/add a magnet to the account; once the torrent is
	//    already present in the account, ProbePayload acquires it with zero adds.
	knownHash := "1234567890abcdef1234567890abcdef12345678"
	knownRel := core.Release{
		ID:           "known-cached-torrent",
		Name:         "Known.Cached.2024.1080p.WEB-DL-GRP",
		Indexer:      "tz",
		Protocol:     core.ProtocolTorrent,
		InfoHash:     knownHash,
		DebridCached: []string{config.DebridRealDebrid},
		Size:         4 << 30,
	}
	h.cacheEverywhere(knownHash)
	if b, ok := h.svc.ProbePayload(t.Context(), knownRel); ok || len(b) != 0 {
		t.Fatal("ProbePayload unexpectedly mutated RD account by adding an unadded torrent")
	}
	if rd.adds != 0 {
		t.Fatalf("ProbePayload added %d torrents to RD account, want 0", rd.adds)
	}
	rd.mu.Lock()
	rd.torrents[knownHash] = "rd-existing-1"
	rd.mu.Unlock()
	b, ok = h.svc.ProbePayload(t.Context(), knownRel)
	if !ok || len(b) == 0 {
		t.Fatal("ProbePayload did not acquire existing cached account torrent")
	}
	if rd.adds != 0 {
		t.Fatalf("ProbePayload added %d torrents for existing account item, want 0", rd.adds)
	}
}

func TestCachedFileMetadataPerEpisodeAndFileIsolation(t *testing.T) {
	h := newHarness(t, 1<<20, config.DebridRealDebrid)
	const packHash = "285f4b2600c4702b546ef71f9747862ecbcf96a7"

	ep4Target := &core.ReleaseTarget{Season: 1, Episode: 4}
	ep4Rel := core.Release{
		ID: "aio-ep4", GUID: "btih:" + packHash + "/s1e4",
		Protocol: core.ProtocolTorrent, InfoHash: packHash,
		Info:   core.ReleaseInfo{Episodes: []int{4}},
		Target: ep4Target,
	}
	ep5Rel := core.Release{
		ID: "aio-ep5", GUID: "btih:" + packHash + "/s1e5",
		Protocol: core.ProtocolTorrent, InfoHash: packHash,
		Info:   core.ReleaseInfo{Episodes: []int{5}},
		Target: &core.ReleaseTarget{Season: 1, Episode: 5},
	}
	h.svc.savePayload(t.Context(), Payload{
		Key: payloadKey(ep4Rel), ReleaseID: ep4Rel.ID, Provider: "realdebrid-0",
		Kind: config.DebridRealDebrid, InfoHash: packHash,
		Path: "Show.S01E04.2160p.mkv", Size: 7370527522, URL: "https://cdn.example/e04.mkv",
		Target: ep4Target,
	})

	// Lookup for E04 without Target (via GUID /s1e4) hits E04; E05 must miss.
	ep4NoTarget := ep4Rel
	ep4NoTarget.Target = nil
	if meta, ok := h.svc.CachedFileMetadata(ep4NoTarget); !ok || meta.Path != "Show.S01E04.2160p.mkv" {
		t.Fatalf("ep4 CachedFileMetadata = %+v ok=%v", meta, ok)
	}
	if meta, ok := h.svc.CachedFileMetadata(ep5Rel); ok {
		t.Fatalf("ep5 leaked ep4 CachedFileMetadata: %+v", meta)
	}

	// Explicit FileIndex entries of a multi-file hash must not cross-hit.
	idx0, idx1 := 0, 1
	f0 := core.Release{ID: "col-0", Protocol: core.ProtocolTorrent, InfoHash: packHash, Target: &core.ReleaseTarget{FileIndex: &idx0}}
	f1 := core.Release{ID: "col-1", Protocol: core.ProtocolTorrent, InfoHash: packHash, Target: &core.ReleaseTarget{FileIndex: &idx1}}
	h.svc.savePayload(t.Context(), Payload{
		Key: payloadKey(f0), ReleaseID: f0.ID, Provider: "realdebrid-0",
		Kind: config.DebridRealDebrid, InfoHash: packHash,
		Path: "Movie.Part1.mkv", Size: 3 << 30, URL: "https://cdn.example/p1.mkv",
		Target: f0.Target,
	})
	if meta, ok := h.svc.CachedFileMetadata(f1); ok {
		t.Fatalf("file1 leaked file0 CachedFileMetadata: %+v", meta)
	}
}
