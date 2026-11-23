package resolver

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestStremioAddonOrder(t *testing.T) {
	ctx := context.Background()
	title := signal2026()
	caps := appleTV4K()

	stremioRel := func(id, name string, sizeMB int64, indexerID, indexer string, order int) core.Release {
		r := withParsed(rel(id, name, sizeMB))
		r.Protocol = core.ProtocolTorrent
		r.InfoHash = strings.Repeat("a", 39-len(id)) + id // distinct hex-like hash per release
		r.IndexerID = indexerID
		r.Indexer = indexer
		r.SourceOrder = order
		r.PubDate = time.Time{}
		return r
	}
	usenetRel := func(id, name string, sizeMB int64, indexerID, indexer string) core.Release {
		r := withParsed(rel(id, name, sizeMB))
		r.Protocol = core.ProtocolUsenet
		r.IndexerID = indexerID
		r.Indexer = indexer
		r.PubDate = time.Time{}
		return r
	}

	// 1. Default off: Stremio add-on results re-rank by quality profile.
	cfgOff := testCfg()
	cfgOff.Indexers = []config.Indexer{
		{ID: "ix-aio", Name: "AIOStreams", Kind: config.IndexerKindStremio, Protocol: "torrent", Enabled: true, Priority: 0},
	}
	svcOff := New(&fakeIndexer{releases: []core.Release{
		stremioRel("pos1", "The.Signal.2026.1080p.WEB-DL.DDP5.1.H.264-GRP", 8<<10, "ix-aio", "AIOStreams", 1),
		stremioRel("pos2", "The.Signal.2026.2160p.WEB-DL.DV.HDR.DDP5.1.H.265-GRP", 18<<10, "ix-aio", "AIOStreams", 2),
	}}, fakeHealth{}, nil, cfgOff, Options{})
	t.Cleanup(func() { svcOff.Close() })
	resOff, err := svcOff.Resolve(ctx, core.ResolveRequest{Title: title, Client: caps})
	if err != nil {
		t.Fatalf("default-off resolve: %v", err)
	}
	if resOff.Release.ID != "pos2" {
		t.Fatalf("default-off picked %q, want profile-ranked pos2", resOff.Release.ID)
	}

	// 2. Enabled: add-on's order wins even when shuffled by cache hints and
	// lower resolution comes first, while hard rules (CAM, blocked custom
	// format, unacceptable audio language) and health failover still apply.
	cfgOn := testCfg()
	cfgOn.Indexers = []config.Indexer{
		{ID: "ix-aio", Name: "AIOStreams", Kind: config.IndexerKindStremio, Protocol: "torrent", Enabled: true, Priority: 0, UseAddonOrder: true},
	}
	cfgOn.Resolver.CustomFormats = append(cfgOn.Resolver.CustomFormats, core.CustomFormat{
		ID:   "cf-blocked",
		Name: "Blocked Group",
		Specifications: []core.FormatSpecification{
			{
				Name:           "BADGRP",
				Implementation: "ReleaseTitleSpecification",
				Required:       true,
				Fields:         map[string]json.RawMessage{"value": json.RawMessage(`"\\bBADGRP\\b"`)},
			},
		},
	})
	for i := range cfgOn.Quality.Profiles {
		if cfgOn.Quality.Profiles[i].CustomFormatScores == nil {
			cfgOn.Quality.Profiles[i].CustomFormatScores = map[string]int{}
		}
		cfgOn.Quality.Profiles[i].CustomFormatScores["cf-blocked"] = -10000
	}

	// Intentionally pass results out of SourceOrder (simulating debrid cache
	// reordering before ranking) to verify SourceOrder restores the add-on's list.
	addonResults := []core.Release{
		stremioRel("a5-4k-remux", "The.Signal.2026.2160p.BluRay.Remux.HEVC.TrueHD-GRP", 50<<10, "ix-aio", "AIOStreams", 5),
		stremioRel("a4-1080-healthy", "The.Signal.2026.1080p.WEB-DL.DDP5.1.H.264-OKGRP", 7<<10, "ix-aio", "AIOStreams", 4),
		stremioRel("a3-unhealthy", "The.Signal.2026.1080p.WEB-DL.DDP5.1.H.264-DEAD", 8<<10, "ix-aio", "AIOStreams", 3),
		stremioRel("a2-blocked-cf", "The.Signal.2026.2160p.WEB-DL.H.265-BADGRP", 18<<10, "ix-aio", "AIOStreams", 2),
		stremioRel("a1-cam", "The.Signal.2026.1080p.HDCAM.H.264-GRP", 4<<10, "ix-aio", "AIOStreams", 1),
		stremioRel("a6-wrong-lang", "The.Signal.2026.2160p.WEB-DL.ITA.DDP5.1.H.265-GRP", 18<<10, "ix-aio", "AIOStreams", 6),
	}
	svcOn := New(
		&fakeIndexer{releases: addonResults},
		fakeHealth{unhealthy: map[string]bool{"a3-unhealthy": true}},
		nil, cfgOn, Options{},
	)
	t.Cleanup(func() { svcOn.Close() })
	resOn, err := svcOn.Resolve(ctx, core.ResolveRequest{Title: title, Client: caps})
	if err != nil {
		t.Fatalf("addon-order resolve: %v", err)
	}
	// a1 rejected (CAM), a2 rejected (blocked custom format), a3 failed health ->
	// a4 wins even though a5 is a 2160p Remux!
	if resOn.Release.ID != "a4-1080-healthy" {
		t.Fatalf("addon-order picked %q, want a4-1080-healthy", resOn.Release.ID)
	}
	if len(resOn.Alternates) == 0 || resOn.Alternates[0].ID != "a5-4k-remux" {
		t.Fatalf("alternates = %v, want a5-4k-remux next in add-on order", resOn.Alternates)
	}
	traces := svcOn.Traces()
	if len(traces) == 0 {
		t.Fatal("expected decision trace")
	}
	tr := traces[len(traces)-1]
	if !strings.Contains(tr.Routing, "AIOStreams") || !strings.Contains(tr.Routing, "add-on order") {
		t.Fatalf("trace routing = %q, want add-on order explanation naming AIOStreams", tr.Routing)
	}
	var winnerRoute string
	for _, c := range tr.Candidates {
		if c.ReleaseID == "a4-1080-healthy" {
			winnerRoute = c.Route
		}
	}
	if !strings.Contains(winnerRoute, "add-on order #4") {
		t.Fatalf("winner route = %q, want add-on order #4", winnerRoute)
	}

	// 3. Interleaving by source priority:
	// [NZBGeek (0), DrunkenSlug (1), AIOStreams (2, UseAddonOrder), Torrentio (3)]
	// Band 0 (NZBGeek + DrunkenSlug) competes by quality profile; Band 1
	// (AIOStreams) keeps its own order; Band 2 (Torrentio) follows by profile.
	cfgMix := testCfg()
	cfgMix.Indexers = []config.Indexer{
		{ID: "ix-nzb1", Name: "NZBGeek", Protocol: "usenet", Enabled: true, Priority: 0},
		{ID: "ix-nzb2", Name: "DrunkenSlug", Protocol: "usenet", Enabled: true, Priority: 1},
		{ID: "ix-aio", Name: "AIOStreams", Kind: config.IndexerKindStremio, Protocol: "torrent", Enabled: true, Priority: 2, UseAddonOrder: true},
		{ID: "ix-tor", Name: "Torrentio", Kind: config.IndexerKindStremio, Protocol: "torrent", Enabled: true, Priority: 3},
	}
	svcMix := New(&fakeIndexer{}, fakeHealth{}, nil, cfgMix, Options{})
	t.Cleanup(func() { svcMix.Close() })
	profile := SelectProfile(cfgMix.Quality, caps, nil)
	mixReleases := []core.Release{
		usenetRel("nzb1-1080", "The.Signal.2026.1080p.WEB-DL.DDP5.1.H.264-GRP", 8<<10, "ix-nzb1", "NZBGeek"),
		usenetRel("nzb2-2160", "The.Signal.2026.2160p.WEB-DL.DV.HDR.DDP5.1.H.265-GRP", 18<<10, "ix-nzb2", "DrunkenSlug"),
		stremioRel("aio-1-1080", "The.Signal.2026.1080p.WEB-DL.DDP5.1.H.264-AIO1", 7<<10, "ix-aio", "AIOStreams", 1),
		stremioRel("aio-2-2160", "The.Signal.2026.2160p.BluRay.Remux.HEVC.TrueHD-AIO2", 50<<10, "ix-aio", "AIOStreams", 2),
		stremioRel("tor-2160", "The.Signal.2026.2160p.BluRay.Remux.HEVC.TrueHD-TOR", 55<<10, "ix-tor", "Torrentio", 1),
	}
	var mixTrace Trace
	cands, _, err := svcMix.rankCandidates(ctx, core.ResolveRequest{Title: title, Client: caps}, searchRequestFor(title), profile, mixReleases, &mixTrace, "")
	if err != nil {
		t.Fatalf("interleave rankCandidates: %v", err)
	}
	got := make([]string, len(cands))
	for i, c := range cands {
		got[i] = c.rel.ID
	}
	want := []string{"nzb2-2160", "nzb1-1080", "aio-1-1080", "aio-2-2160", "tor-2160"}
	if !slices.Equal(got, want) {
		t.Fatalf("interleaved order = %v, want %v", got, want)
	}
}
