package debrid

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestPayloadRoundTripAndFileChoice(t *testing.T) {
	p := Payload{Key: "r|s1e2", Provider: "rd", Kind: "realdebrid", InfoHash: testHash, Path: "Show/Show.S01E02.mkv", Size: 42,
		Target: &core.ReleaseTarget{Season: 1, Episode: 2}, Expires: time.Unix(1700000000, 0).UTC()}
	b := p.Encode()
	if !IsPayload(b) || IsPayload([]byte("<?xml version")) {
		t.Fatal("IsPayload")
	}
	got, err := DecodePayload(b)
	if err != nil || got.Path != p.Path || got.Target.Episode != 2 || !got.Expires.Equal(p.Expires) || got.Name() != "Show.S01E02.mkv" {
		t.Fatalf("decode %+v %v", got, err)
	}
	if _, err := DecodePayload([]byte(payloadMagic + `{"size":0}`)); err == nil {
		t.Fatal("incomplete payload accepted")
	}
	if strings.Contains(string(b), "apiKey") {
		t.Fatal("payload carries a key field")
	}

	pack := []File{
		{ID: "a", Path: "Show.S01/Show.S01E01.1080p.mkv", Size: 2000},
		{ID: "b", Path: "Show.S01/Show.S01E02.1080p.mkv", Size: 1900},
		{ID: "c", Path: "Show.S01/Sample/Show.S01E02.sample.mkv", Size: 50},
		{ID: "d", Path: "Show.S01/Show.S01E02.1080p.nfo", Size: 1},
	}
	if f, err := ChooseFile(pack, &core.ReleaseTarget{Season: 1, Episode: 2}); err != nil || f.ID != "b" {
		t.Fatalf("episode %+v %v", f, err)
	}
	if f, err := ChooseFile(pack, nil); err != nil || f.ID != "a" {
		t.Fatalf("largest %+v %v", f, err)
	}
	if _, err := ChooseFile(pack, &core.ReleaseTarget{Season: 1, Episode: 9}); !errors.Is(err, core.ErrWrongContent) {
		t.Fatalf("missing episode: %v", err)
	}
	seasonDirs := []File{{ID: "x", Path: "Show/Season 2/Episode 05 - Name.mkv", Size: 10}, {ID: "y", Path: "Show/Season 2/2x06 Name.mkv", Size: 11}}
	if f, err := ChooseFile(seasonDirs, &core.ReleaseTarget{Season: 2, Episode: 6}); err != nil || f.ID != "y" {
		t.Fatalf("NxNN %+v %v", f, err)
	}
	if f, err := ChooseFile(seasonDirs, &core.ReleaseTarget{Season: 2, Episode: 5}); err != nil || f.ID != "x" {
		t.Fatalf("Season/Episode dir %+v %v", f, err)
	}
	multiSeason := []File{
		{ID: "s1e5", Path: "Show/Season 1/Episode 05.mkv", Size: 20},
		{ID: "s2e5", Path: "Show/Season 2/Episode 05.mkv", Size: 15},
		{ID: "s2e7", Path: "Show/Season 2/07 - Classic Numbering.mkv", Size: 14},
		{ID: "s2e8", Path: "Show/Season 2/208 - Production Code.mkv", Size: 13},
	}
	if f, err := ChooseFile(multiSeason, &core.ReleaseTarget{Season: 2, Episode: 5}); err != nil || f.ID != "s2e5" {
		t.Fatalf("multi-season directory disambiguation: got %+v err=%v", f, err)
	}
	if f, err := ChooseFile(multiSeason, &core.ReleaseTarget{Season: 2, Episode: 7}); err != nil || f.ID != "s2e7" {
		t.Fatalf("classic leading episode number: got %+v err=%v", f, err)
	}
	if f, err := ChooseFile(multiSeason, &core.ReleaseTarget{Season: 2, Episode: 8}); err != nil || f.ID != "s2e8" {
		t.Fatalf("classic 3-digit SEE number: got %+v err=%v", f, err)
	}
	miniseries := []File{
		{ID: "p1", Path: "Band.of.Brothers/Part 01 - Currahee.mkv", Size: 30},
		{ID: "p2", Path: "Band.of.Brothers/Part 02 - Day of Days.mkv", Size: 25},
	}
	if f, err := ChooseFile(miniseries, &core.ReleaseTarget{Season: 1, Episode: 2}); err != nil || f.ID != "p2" {
		t.Fatalf("miniseries Part N: got %+v err=%v", f, err)
	}
	if _, err := ChooseFile([]File{{Path: "x.rar", Size: 9}}, nil); !errors.Is(err, core.ErrReleaseRejected) {
		t.Fatal("rar accepted")
	}
	single := []File{{ID: "s", Path: "abc123.mkv", Size: 9}}
	if f, err := ChooseFile(single, &core.ReleaseTarget{Season: 1, Episode: 1}); err != nil || f.ID != "s" {
		t.Fatalf("single obfuscated episode %+v %v", f, err)
	}
	same := sameFile(Payload{Path: "/Movie/Movie.mkv", Size: 9})
	if f, err := same([]File{{ID: "1", Path: "Movie/MOVIE.mkv", Size: 9}, {ID: "2", Path: "b.mkv", Size: 9}}); err != nil || f.ID != "1" {
		t.Fatalf("same file %+v %v", f, err)
	}
	if _, err := same([]File{{Path: "Movie/Movie.mkv", Size: 8}}); !errors.Is(err, errFileMissing) {
		t.Fatalf("size change: %v", err)
	}
}

func TestServicePriorityFallbackAndPayloadReuse(t *testing.T) {
	h := newHarness(t, 1<<20, "realdebrid", "torbox")
	first, second := h.provs["realdebrid-0"], h.provs["torbox-1"]
	second.cached[testHash] = true // only the lower-priority provider has it
	ctx := context.Background()
	b, err := h.svc.Acquire(ctx, torrentRelease("rel-1"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := DecodePayload(b)
	if p.Provider != "torbox-1" || p.URL == "" || p.Size != 1<<20 || p.ReleaseID != "rel-1" || p.FileID != "1" {
		t.Fatalf("payload %+v", p)
	}
	if !h.svc.KnownCached(testHash) {
		t.Fatal("hash not remembered as cached")
	}
	// The higher-priority provider is now known not to have it, and the
	// stored payload is reused without any provider call.
	before := first.totalCalls() + second.totalCalls()
	if _, err := h.svc.Acquire(ctx, torrentRelease("rel-1")); err != nil {
		t.Fatal(err)
	}
	if after := first.totalCalls() + second.totalCalls(); after != before {
		t.Fatalf("payload reuse made %d provider calls", after-before)
	}
	// Another release with the same hash skips the provider known not to
	// have it and reuses the torrent already on the other account.
	b, err = h.svc.Acquire(ctx, torrentRelease("rel-2"))
	if err != nil {
		t.Fatal(err)
	}
	p2, _ := DecodePayload(b)
	if first.callCount("acquire") != 1 || p2.TorrentID != p.TorrentID {
		t.Fatalf("acquire calls %d, torrent %s vs %s", first.callCount("acquire"), p2.TorrentID, p.TorrentID)
	}
	st := h.svc.Stats()
	if len(st) != 2 || st[1].Cached != 2 || st[1].Reused != 1 || st[0].NotCached != 1 || st[1].Latency.Samples == 0 {
		t.Fatalf("stats %+v", st)
	}
}

func TestServiceNotCachedIsASkipNotAFailure(t *testing.T) {
	h := newHarness(t, 1<<20, "realdebrid", "alldebrid")
	_, err := h.svc.Acquire(context.Background(), torrentRelease("r"))
	if !errors.Is(err, core.ErrNotCached) || !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("got %v", err)
	}
	for _, perm := range []error{core.ErrArticleMissing, core.ErrArticleCorrupt, core.ErrReleaseRejected} {
		if errors.Is(err, perm) {
			t.Fatalf("not-cached classified permanent: %v", err)
		}
	}
	// The answer is remembered: no provider is asked again for a while.
	calls := h.provs["realdebrid-0"].callCount("acquire")
	if _, err = h.svc.Acquire(context.Background(), torrentRelease("r1")); !errors.Is(err, core.ErrNotCached) || h.provs["realdebrid-0"].callCount("acquire") != calls {
		t.Fatalf("uncached memory: %v", err)
	}
	// No playable file is a property of the torrent.
	other := strings.Repeat("12", 20)
	h.cacheEverywhere(other)
	for _, p := range h.provs {
		p.files = []File{{ID: "1", Path: "x/movie.rar", Size: 10}}
	}
	rel := torrentRelease("r2")
	rel.InfoHash = other
	if _, err = h.svc.Acquire(context.Background(), rel); !errors.Is(err, core.ErrReleaseRejected) {
		t.Fatalf("no video: %v", err)
	}
}

func TestServiceAuthFailurePausesProviderAndIsTransient(t *testing.T) {
	h := newHarness(t, 1<<20, "realdebrid", "torbox")
	h.provs["realdebrid-0"].authFail = true
	h.provs["torbox-1"].cached[testHash] = true
	ctx := context.Background()
	if _, err := h.svc.Acquire(ctx, torrentRelease("a")); err != nil {
		t.Fatalf("fallback: %v", err)
	}
	st := h.svc.Stats()
	if st[0].State != "auth_failed" || st[0].BackoffUntil == nil || st[0].LastError == "" {
		t.Fatalf("breaker %+v", st[0])
	}
	calls := h.provs["realdebrid-0"].callCount("acquire")
	h.provs["torbox-1"].cached[testHash] = false
	_, err := h.svc.Acquire(ctx, torrentRelease("b"))
	if h.provs["realdebrid-0"].callCount("acquire") != calls {
		t.Fatal("paused provider was called")
	}
	if err == nil || errors.Is(err, core.ErrReleaseRejected) {
		t.Fatalf("got %v", err)
	}
	// With no provider enabled the error is configuration, not availability.
	h.svc.UpdateConfig(config.Debrid{})
	if _, err := h.svc.Acquire(ctx, torrentRelease("c")); !errors.Is(err, ErrNoProvider) || errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("no provider: %v", err)
	}
}

func TestServiceRefresh(t *testing.T) {
	h := newHarness(t, 1<<20, "realdebrid", "torbox")
	h.cacheEverywhere(testHash)
	ctx := context.Background()
	b, err := h.svc.Acquire(ctx, torrentRelease("r"))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := DecodePayload(b)
	rd := h.provs["realdebrid-0"]

	// A fresh link is returned as is.
	if out, err := h.svc.Refresh(ctx, p, false); err != nil || out.URL != p.URL {
		t.Fatalf("fresh %v", err)
	}
	// Forced: same provider, new link.
	out, err := h.svc.Refresh(ctx, p, true)
	if err != nil || out.URL == p.URL || out.Provider != p.Provider {
		t.Fatalf("forced %+v %v", out, err)
	}
	// The torrent was removed from the account: re-added by magnet.
	_ = rd.Remove(ctx, p.TorrentID)
	out, err = h.svc.Refresh(ctx, out, true)
	if err != nil || out.TorrentID == p.TorrentID || out.Provider != "realdebrid-0" {
		t.Fatalf("re-add %+v %v", out, err)
	}
	// No longer cached on the first provider: the second takes over.
	rd.mu.Lock()
	rd.cached[testHash], rd.gone = false, true
	rd.mu.Unlock()
	out, err = h.svc.Refresh(ctx, out, true)
	if err != nil || out.Provider != "torbox-1" {
		t.Fatalf("move %+v %v", out, err)
	}
	// Gone everywhere: transient, never "missing" (no bad mark).
	for _, fp := range h.provs {
		fp.mu.Lock()
		fp.cached[testHash], fp.gone = false, true
		fp.mu.Unlock()
	}
	_, err = h.svc.Refresh(ctx, out, true)
	if !errors.Is(err, core.ErrSourceUnavailable) || errors.Is(err, core.ErrArticleMissing) {
		t.Fatalf("gone everywhere: %v", err)
	}
	// The file vanished from the torrent: missing (failover + bad mark).
	for _, fp := range h.provs {
		fp.mu.Lock()
		fp.cached[testHash], fp.gone = true, true
		fp.files = []File{{ID: "9", Path: "Other/other.mkv", Size: 77}}
		fp.mu.Unlock()
	}
	if _, err = h.svc.Refresh(ctx, out, true); !errors.Is(err, core.ErrArticleMissing) {
		t.Fatalf("file missing: %v", err)
	}
}

func TestServiceCheckCachedAndTest(t *testing.T) {
	h := newHarness(t, 1<<20, "torbox", "premiumize")
	for _, p := range h.provs {
		p.bulk = true
	}
	other := strings.Repeat("ef", 20)
	h.provs["premiumize-1"].cached[testHash] = true
	got, definitive := h.svc.CheckCached(context.Background(), []string{testHash, other})
	if !definitive || !got[testHash] || got[other] {
		t.Fatalf("check %v %v", got, definitive)
	}
	if !h.svc.KnownCached(testHash) || h.svc.KnownCached(other) {
		t.Fatal("known cached")
	}
	// Answers are remembered: no second round trip.
	calls := h.provs["torbox-0"].callCount("check")
	h.svc.CheckCached(context.Background(), []string{testHash, other})
	if h.provs["torbox-0"].callCount("check") != calls {
		t.Fatal("bulk check repeated")
	}
	if err := h.svc.Test(context.Background(), config.DebridProvider{ID: "torbox-0", Kind: "torbox", APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	h.provs["torbox-0"].authFail = true
	if err := h.svc.Test(context.Background(), config.DebridProvider{ID: "torbox-0", Kind: "torbox", APIKey: "k"}); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("test auth: %v", err)
	}
}

func TestUpdateConfigKeepsProviderState(t *testing.T) {
	h := newHarness(t, 1<<20, "realdebrid")
	h.cacheEverywhere(testHash)
	if _, err := h.svc.Acquire(context.Background(), torrentRelease("r")); err != nil {
		t.Fatal(err)
	}
	cfg := h.svc.Config()
	cfg.Providers[0].Name = "Renamed"
	h.svc.UpdateConfig(cfg)
	st := h.svc.Stats()
	if st[0].Name != "Renamed" || st[0].Cached != 1 {
		t.Fatalf("stats lost on rename: %+v", st[0])
	}
	cfg.Providers[0].Enabled = false
	h.svc.UpdateConfig(cfg)
	if h.svc.Enabled() || h.svc.Stats()[0].State != "disabled" {
		t.Fatal("disable")
	}
}

func TestServiceCheckCachedByAndKnownCachedBy(t *testing.T) {
	h := newHarness(t, 1<<20, "torbox", "premiumize", "realdebrid")
	for _, p := range h.provs {
		p.bulk = true
	}
	// Give the second provider a custom account name and reverse priorities so
	// priority order differs from config slice order.
	cfg := h.svc.Config()
	cfg.Providers[0].Priority = 20 // torbox-0 -> default display name "TorBox"
	cfg.Providers[0].Name = ""
	cfg.Providers[1].Priority = 10 // premiumize-1 -> custom display name
	cfg.Providers[1].Name = "Family PM"
	cfg.Providers[2].Priority = 30 // realdebrid-2 -> bulk unsupported
	cfg.Providers[2].Name = ""
	h.svc.UpdateConfig(cfg)
	h.provs["realdebrid-2"].bulk = false

	both := testHash
	onlyTorBox := strings.Repeat("12", 20)
	uncached := strings.Repeat("ef", 20)
	h.provs["premiumize-1"].cached[both] = true
	h.provs["torbox-0"].cached[both] = true
	h.provs["torbox-0"].cached[onlyTorBox] = true

	by, definitive := h.svc.CheckCachedBy(context.Background(), []string{both, onlyTorBox, uncached})
	if definitive {
		t.Fatal("expected definitive=false when realdebrid does not support bulk checks")
	}
	// Acquisition order is priority 10 (Family PM) then priority 20 (TorBox).
	wantBoth := []core.DebridServiceRef{
		{ID: "premiumize-1", Name: "Family PM"},
		{ID: "torbox-0", Name: "TorBox"},
	}
	if len(by[both]) != len(wantBoth) || by[both][0] != wantBoth[0] || by[both][1] != wantBoth[1] {
		t.Fatalf("both cachedBy = %+v, want %+v", by[both], wantBoth)
	}
	if len(by[onlyTorBox]) != 1 || by[onlyTorBox][0] != (core.DebridServiceRef{ID: "torbox-0", Name: "TorBox"}) {
		t.Fatalf("onlyTorBox cachedBy = %+v", by[onlyTorBox])
	}
	if _, present := by[uncached]; present {
		t.Fatalf("uncached hash should be omitted, got %+v", by[uncached])
	}

	// Second call served from memory cache keeps the same services and priority order.
	calls := h.provs["torbox-0"].callCount("check")
	by2, _ := h.svc.CheckCachedBy(context.Background(), []string{both, uncached})
	if h.provs["torbox-0"].callCount("check") != calls {
		t.Fatal("expected warm cache hit without second provider call")
	}
	if len(by2[both]) != 2 || by2[both][0] != wantBoth[0] || by2[both][1] != wantBoth[1] {
		t.Fatalf("warm cachedBy = %+v, want %+v", by2[both], wantBoth)
	}

	// KnownCachedBy reflects verified cache entries and lets verified negative verdicts
	// override stale addon hints while falling back to hints for unchecked providers.
	gotKnown := h.svc.KnownCachedBy(onlyTorBox, []string{"premiumize", "realdebrid"})
	wantKnown := []core.DebridServiceRef{
		{ID: "torbox-0", Name: "TorBox"},
		{ID: "realdebrid-2", Name: "Real-Debrid"},
	}
	if len(gotKnown) != len(wantKnown) || gotKnown[0] != wantKnown[0] || gotKnown[1] != wantKnown[1] {
		t.Fatalf("KnownCachedBy = %+v, want %+v", gotKnown, wantKnown)
	}

	// ServiceRef resolves configured display name and falls back to kind label if removed.
	ref, ok := h.svc.ServiceRef("premiumize-1", "premiumize")
	if !ok || ref != (core.DebridServiceRef{ID: "premiumize-1", Name: "Family PM"}) {
		t.Fatalf("ServiceRef configured = %+v %v", ref, ok)
	}
	ref, ok = h.svc.ServiceRef("gone-id", "torbox")
	if !ok || ref != (core.DebridServiceRef{ID: "gone-id", Name: "TorBox"}) {
		t.Fatalf("ServiceRef fallback = %+v %v", ref, ok)
	}
}
