package resolver

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func testEpisodeTitle(tmdb int64, season, ep int) *core.Title {
	return &core.Title{
		Key:       core.EpisodeKey(tmdb, season, ep),
		ShowKey:   core.ShowKey(tmdb),
		Type:      core.TypeEpisode,
		Title:     "Episode",
		ShowTitle: "Severance",
		Year:      2022,
		Season:    season,
		Episode:   ep,
		IDs:       core.IDs{TMDB: tmdb},
	}
}

func testViewerChoiceReleases() []core.Release {
	return []core.Release{
		{
			ID: "rel-4k-dv-remux", Indexer: "idx", Protocol: core.ProtocolUsenet, Size: 58 << 30,
			Name: "The.Signal.1999.2160p.UHD.BluRay.REMUX.DV.HEVC.TrueHD.7.1.Atmos-GRP1",
			Info: core.ReleaseInfo{
				Title: "The Signal", Year: 1999, Height: 2160, Source: core.SourceRemux,
				VideoCodec: "hevc", DolbyVision: true, HDR10: true, AudioCodec: "truehd",
				Atmos: true, Channels: "7.1", Group: "GRP1",
			},
		},
		{
			ID: "rel-1080p-remux", Indexer: "idx", Protocol: core.ProtocolUsenet, Size: 28 << 30,
			Name: "The.Signal.1999.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP2",
			Info: core.ReleaseInfo{
				Title: "The Signal", Year: 1999, Height: 1080, Source: core.SourceRemux,
				VideoCodec: "h264", AudioCodec: "dtshd", Channels: "5.1", Group: "GRP2",
			},
		},
		{
			ID: "rel-1080p-webdl", Indexer: "idx", Protocol: core.ProtocolUsenet, Size: 7 << 30,
			Name: "The.Signal.1999.1080p.WEB-DL.DDP5.1.Atmos.H.264-GRP3",
			Info: core.ReleaseInfo{
				Title: "The Signal", Year: 1999, Height: 1080, Source: core.SourceWEBDL,
				VideoCodec: "h264", AudioCodec: "eac3", Atmos: true, Channels: "5.1", Group: "GRP3",
			},
		},
	}
}

type episodeChoiceIndexer struct {
	byEpisode map[int][]core.Release
}

func (f *episodeChoiceIndexer) Name() string { return "ep-fake" }
func (f *episodeChoiceIndexer) Search(_ context.Context, req core.SearchRequest) ([]core.Release, error) {
	return append([]core.Release(nil), f.byEpisode[req.Episode]...), nil
}
func (f *episodeChoiceIndexer) FetchNZB(_ context.Context, r core.Release) ([]byte, error) {
	return []byte("nzb:" + r.ID), nil
}

func TestViewerChoicePrecedenceSafetyAndClear(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	cfg := testCfg()
	cfg.Resolver.SameQualityAlternatives = 2
	cfg.Resolver.OfferHigherVersions = true
	releases := testViewerChoiceReleases()
	idx := &fakeIndexer{releases: releases}
	svc := New(idx, fakeHealth{}, db, cfg, Options{})
	defer svc.Close()

	viewer1 := &core.User{ID: 1, Username: "alice"}
	viewer2 := &core.User{ID: 2, Username: "bob"}
	title := matrixTitle()

	// Owner pins rel-1080p-webdl.
	if _, err := svc.Pin(ctx, title.Key, title.Title, releases[2], ""); err != nil {
		t.Fatalf("pin: %v", err)
	}
	req1 := core.ResolveRequest{Title: title, User: viewer1, Client: appleTVCaps()}
	res, err := svc.Resolve(ctx, req1)
	if err != nil || res.Release.ID != "rel-1080p-webdl" {
		t.Fatalf("owner pin resolve = %+v, err=%v; want rel-1080p-webdl", res, err)
	}

	// Viewer 1 remembers rel-1080p-remux -> overrides owner pin for Viewer 1 only.
	if err := svc.RecordViewerChoice(ctx, viewer1.ID, title, "slot:alt1", releases[1], nil); err != nil {
		t.Fatalf("record viewer choice: %v", err)
	}
	res1, err := svc.Resolve(ctx, req1)
	if err != nil || res1.Release.ID != "rel-1080p-remux" {
		t.Fatalf("viewer 1 resolve = %+v, err=%v; want rel-1080p-remux", res1, err)
	}
	// Viewer 2 still sees the owner pin.
	req2 := core.ResolveRequest{Title: title, User: viewer2, Client: appleTVCaps()}
	res2, err := svc.Resolve(ctx, req2)
	if err != nil || res2.Release.ID != "rel-1080p-webdl" {
		t.Fatalf("viewer 2 resolve = %+v, err=%v; want rel-1080p-webdl", res2, err)
	}

	// VersionSlots projects Remembered on Auto and Pinned on the owner pin's slot,
	// plus Availability classification (local cache -> instant).
	svc.SetLocalCacheLookup(func(id string) bool { return id == "rel-1080p-remux" })
	slots, err := svc.VersionSlots(ctx, req1, core.MaxVariants, true)
	if err != nil || len(slots) == 0 {
		t.Fatalf("VersionSlots: %v (len=%d)", err, len(slots))
	}
	if !slots[0].Auto || !slots[0].Remembered || slots[0].Release.ID != "rel-1080p-remux" {
		t.Fatalf("Auto slot = %+v; want Remembered=true, Release=rel-1080p-remux", slots[0])
	}
	if slots[0].Availability != core.VersionInstant {
		t.Fatalf("Auto slot availability = %q, want instant", slots[0].Availability)
	}
	foundPinned := false
	for _, s := range slots {
		if s.Release.ID == "rel-1080p-webdl" && s.Pinned {
			foundPinned = true
		}
	}
	if !foundPinned {
		t.Fatalf("owner-pinned release did not carry Pinned=true in slots: %+v", slots)
	}

	// Safety gate: if Viewer 1's remembered release is excluded/bad, fall back silently to owner pin!
	reqExcluded := req1
	reqExcluded.Exclude = []string{"rel-1080p-remux"}
	resFallback, err := svc.Resolve(ctx, reqExcluded)
	if err != nil || resFallback.Release.ID != "rel-1080p-webdl" {
		t.Fatalf("excluded remembered choice = %+v, err=%v; want silent fallback to owner pin rel-1080p-webdl", resFallback, err)
	}

	// Persistence across service restart: reopen resolver against the same DB.
	svc2 := New(idx, fakeHealth{}, db, cfg, Options{})
	defer svc2.Close()
	summary, err := svc2.ViewerChoiceSummary(ctx, viewer1, title)
	if err != nil || summary == nil || summary.Scope != "title" || summary.Label == "" {
		t.Fatalf("restarted ViewerChoiceSummary = %+v, err=%v", summary, err)
	}

	// Clear choice restores owner pin.
	cleared, err := svc2.ClearViewerChoice(ctx, viewer1.ID, title, "all")
	if err != nil || !cleared {
		t.Fatalf("ClearViewerChoice = %v, %v; want true, nil", cleared, err)
	}
	resAfterClear, err := svc2.Resolve(ctx, req1)
	if err != nil || resAfterClear.Release.ID != "rel-1080p-webdl" {
		t.Fatalf("resolve after clear = %+v, err=%v; want owner pin rel-1080p-webdl", resAfterClear, err)
	}
}

func TestViewerChoiceEpisodeShowProfileAndSilentFallback(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	cfg := testCfg()
	cfg.Resolver.OfferHigherVersions = true

	ep1 := testEpisodeTitle(95396, 1, 1)
	ep2 := testEpisodeTitle(95396, 1, 2)
	ep3 := testEpisodeTitle(95396, 1, 3)

	ep2Releases := []core.Release{
		{
			ID: "ep2-4k-dv-webdl", Indexer: "idx", Protocol: core.ProtocolUsenet, Size: 9 << 30,
			Name: "Severance.S01E02.2160p.ATVP.WEB-DL.DDP5.1.Atmos.DV.HEVC-GRP",
			Info: core.ReleaseInfo{
				Title: "Severance", Seasons: []int{1}, Episodes: []int{2}, Height: 2160,
				Source: core.SourceWEBDL, VideoCodec: "hevc", DolbyVision: true, AudioCodec: "eac3", Atmos: true,
			},
		},
		{
			ID: "ep2-1080p-remux", Indexer: "idx", Protocol: core.ProtocolUsenet, Size: 14 << 30,
			Name: "Severance.S01E02.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP",
			Info: core.ReleaseInfo{
				Title: "Severance", Seasons: []int{1}, Episodes: []int{2}, Height: 1080,
				Source: core.SourceRemux, VideoCodec: "h264", AudioCodec: "dtshd",
			},
		},
	}
	ep3Releases := []core.Release{
		{
			ID: "ep3-720p-webdl", Indexer: "idx", Protocol: core.ProtocolUsenet, Size: 2 << 30,
			Name: "Severance.S01E03.720p.WEB-DL.H.264-GRP",
			Info: core.ReleaseInfo{
				Title: "Severance", Seasons: []int{1}, Episodes: []int{3}, Height: 720,
				Source: core.SourceWEBDL, VideoCodec: "h264", AudioCodec: "aac",
			},
		},
	}

	idx := &episodeChoiceIndexer{byEpisode: map[int][]core.Release{2: ep2Releases, 3: ep3Releases}}
	svc := New(idx, fakeHealth{}, db, cfg, Options{})
	defer svc.Close()

	viewer := &core.User{ID: 7, Username: "mark"}
	ep1Picked := core.Release{
		ID: "ep1-1080p-remux", Indexer: "idx", Protocol: core.ProtocolUsenet, Size: 13 << 30,
		Name: "Severance.S01E01.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP",
		Info: core.ReleaseInfo{
			Title: "Severance", Seasons: []int{1}, Episodes: []int{1}, Height: 1080,
			Source: core.SourceRemux, VideoCodec: "h264", AudioCodec: "dtshd",
		},
	}

	// Recording ep1 records both title choice (ep1) and show profile ("1080p Remux").
	if err := svc.RecordViewerChoice(ctx, viewer.ID, ep1, "slot:alt1", ep1Picked, nil); err != nil {
		t.Fatalf("record ep1: %v", err)
	}
	// Episode 2 should automatically resolve to its matching 1080p Remux slot instead of 4K DV WEB-DL!
	resEp2, err := svc.Resolve(ctx, core.ResolveRequest{Title: ep2, User: viewer, Client: appleTVCaps()})
	if err != nil || resEp2.Release.ID != "ep2-1080p-remux" {
		t.Fatalf("ep2 resolve = %+v, err=%v; want ep2-1080p-remux via show profile", resEp2, err)
	}
	summaryEp2, err := svc.ViewerChoiceSummary(ctx, viewer, ep2)
	if err != nil || summaryEp2 == nil || summaryEp2.Scope != "show" || summaryEp2.Label != "1080p Remux" {
		t.Fatalf("ep2 summary = %+v, err=%v; want scope=show label='1080p Remux'", summaryEp2, err)
	}
	// Episode 3 has only 720p WEB-DL (no match for 1080p Remux) -> silent fallback!
	resEp3, err := svc.Resolve(ctx, core.ResolveRequest{Title: ep3, User: viewer, Client: appleTVCaps()})
	if err != nil || resEp3.Release.ID != "ep3-720p-webdl" {
		t.Fatalf("ep3 resolve = %+v, err=%v; want silent fallback to ep3-720p-webdl", resEp3, err)
	}

	// Clearing "show" on the show key removes the show preference.
	cleared, err := svc.ClearViewerChoice(ctx, viewer.ID, &core.Title{Key: core.ShowKey(95396), Type: core.TypeShow}, "show")
	if err != nil || !cleared {
		t.Fatalf("clear show choice: %v, %v", cleared, err)
	}
	if s, err := svc.ViewerChoiceSummary(ctx, viewer, ep2); err != nil || s != nil {
		t.Fatalf("expected cleared show choice on ep2, got %+v, err=%v", s, err)
	}
}
