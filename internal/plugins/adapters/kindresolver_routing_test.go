package adapters_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/resolver"
)

func TestKindResolverTierRoutingStartupStatsAndStallDemotion(t *testing.T) {
	ctx := context.Background()
	h := newKindTestHarness(t, 0, nil)

	const (
		nzbURL     = "https://media.example/releases/usenet-tier.nzb"
		torboxHash = "fedcba9876543210fedcba9876543210fedcba98"
	)
	usenetBytes := []byte("USENET-TIER-PAYLOAD-BYTES")
	torboxBytes := []byte("TORBOX-TIER-PAYLOAD-BYTES")

	idx := &kindFailoverIndexer{
		nzbPayloads: map[string][]byte{nzbURL: usenetBytes},
	}
	deb := &kindFailoverDebrid{
		payloads: map[string][]byte{torboxHash: torboxBytes},
	}

	defCfg := config.Defaults("")
	defCfg.Resolver.SourcePreference = resolver.PreferDebrid
	resSvc := resolver.New(idx, nil, nil, defCfg, resolver.Options{})
	t.Cleanup(func() { _ = resSvc.Close() })

	kr := adapters.NewKindResolver(adapters.KindResolverDeps{
		Manager:   h.mgr,
		Libraries: h.libs,
		Items:     h.items,
		Blobs:     h.blobs,
		Catalog:   h.catalog,
		Indexer:   idx,
		Resolver:  resSvc,
		Debrid:    deb,
		Engine:    kindFailoverEngine{},
		Now:       time.Now,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	tier1 := 1
	h.rt.callContext = func(_ context.Context, req plugins.Request) (plugins.Response, error) {
		switch req.Path {
		case "/ether/health":
			return plugins.Response{Status: http.StatusOK, Body: json.RawMessage(`{"status":"ok"}`)}, nil
		case "/ether/v1/kinds/recordings/resolve":
			resp := core.KindResolveResponse{
				TTLSec: 600,
				Sources: []core.PlayableSource{
					{
						ID:          "usenet-1",
						Pattern:     "release",
						MIME:        "video/x-matroska",
						Tier:        &tier1,
						Size:        4 << 30,
						DurationSec: 3600,
						Release: &core.ReleaseHint{
							// Deliberately distinct releaseparse source tag (WEB-DL vs BluRay)
							// to prove explicit plugin Tier overrides movie/TV sourceClass.
							FileHint: "Concert.2025.1080p.WEB-DL.H264-USENET.mkv",
							NZBURL:   nzbURL,
						},
					},
					{
						ID:          "debrid-1",
						Pattern:     "release",
						MIME:        "video/x-matroska",
						Tier:        &tier1,
						Size:        4 << 30,
						DurationSec: 3600,
						Release: &core.ReleaseHint{
							FileHint: "Concert.2025.1080p.BluRay.x264-TORBOX.mkv",
							InfoHash: torboxHash,
							Candidate: &core.SourceCandidate{
								InfoHash: torboxHash,
								Cached:   true,
							},
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

	const pluginID = "community.example.kindrouting"
	decl := recordingsKindDecl(true)
	decl["sync"] = map[string]any{"mode": "push"}
	installStudioManifest(t, h.mgr, map[string]any{
		"id":      pluginID,
		"version": "1.0.0",
		"name":    "Kind Routing Test",
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "content",
			"summary":      "Kind routing test",
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

	// 1. PreferDebrid routes debrid-1 ahead of usenet-1 within tier 1, and
	// feeds the open duration into resolver.Service's shared startup stats.
	m1, err := kr.OpenKind(ctx, viewer, trk1.Key, nil)
	if err != nil {
		t.Fatalf("OpenKind(trk-1): %v", err)
	}
	_ = m1.Closer.Close()
	if m1.Release.Protocol != core.ProtocolTorrent {
		t.Fatalf("m1.Release.Protocol = %q, want %q (tier routing should prefer debrid)", m1.Release.Protocol, core.ProtocolTorrent)
	}
	var torrentSamples int64
	for _, st := range resSvc.SourceStartups() {
		if st.Protocol == core.ProtocolTorrent {
			torrentSamples = st.Samples
		}
	}
	if torrentSamples != 1 {
		t.Fatalf("resolver torrent startup samples = %d, want 1", torrentSamples)
	}

	// 2. Observing a playback stall on the exact Release returned by OpenKind
	// demotes that stalled debrid release behind same-tier usenet-1 on trk-2
	// (proving deliveryKey matches playableSourceRelease end-to-end).
	resSvc.ObserveSourceStall(m1.Release, 100_000)

	m2, err := kr.OpenKind(ctx, viewer, trk2.Key, nil)
	if err != nil {
		t.Fatalf("OpenKind(trk-2): %v", err)
	}
	_ = m2.Closer.Close()
	if m2.Release.Protocol != core.ProtocolUsenet {
		t.Fatalf("m2.Release.Protocol = %q, want %q (stalled debrid release must be demoted)", m2.Release.Protocol, core.ProtocolUsenet)
	}
}
