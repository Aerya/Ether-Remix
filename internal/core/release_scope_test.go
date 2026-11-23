package core

import (
	"testing"
	"time"
)

func TestReleaseScopedContentIDPerEpisodePackInvariants(t *testing.T) {
	const packHash = "285f4b2600c4702b546ef71f9747862ecbcf96a7"
	bareKey := "bt:" + packHash

	// Stremio-style per-episode pack listings (no FullSeason): GUID /s1eN + Info.Episodes=[N].
	ep4NoTarget := Release{
		ID:       "aio-ep4",
		GUID:     "btih:" + packHash + "/s1e4",
		Protocol: ProtocolTorrent,
		InfoHash: packHash,
		Info:     ReleaseInfo{Episodes: []int{4}},
	}
	ep5NoTarget := Release{
		ID:       "aio-ep5",
		GUID:     "btih:" + packHash + "/s1e5",
		Protocol: ProtocolTorrent,
		InfoHash: packHash,
		Info:     ReleaseInfo{Episodes: []int{5}},
	}
	ep4FullSeason := Release{
		ID:       "pack-ep4",
		Protocol: ProtocolTorrent,
		InfoHash: packHash,
		Info:     ReleaseInfo{FullSeason: true, Seasons: []int{1}},
		Target:   &ReleaseTarget{Season: 1, Episode: 4},
	}

	k4 := ep4NoTarget.ScopedContentID(bareKey)
	k5 := ep5NoTarget.ScopedContentID(bareKey)
	k4Pack := ep4FullSeason.ScopedContentID(bareKey)

	if k4 == bareKey || k5 == bareKey {
		t.Fatalf("per-episode pack release fell back to bare key: k4=%q k5=%q", k4, k5)
	}
	if k4 == k5 {
		t.Fatalf("E04 and E05 collided on ScopedContentID: %q", k4)
	}
	if k4 != k4Pack {
		t.Fatalf("per-episode listing key %q != FullSeason pack key %q for same episode", k4, k4Pack)
	}

	// Retargeted/vaulted pack: Target episode must win over stale Info.Episodes=[4].
	vaultedForEp5 := Release{
		ID:       "vault-bt:" + packHash,
		GUID:     "vault:bt:" + packHash,
		Protocol: ProtocolTorrent,
		InfoHash: packHash,
		Info:     ReleaseInfo{Seasons: []int{1}, Episodes: []int{4}},
		Target:   &ReleaseTarget{Season: 1, Episode: 5},
	}
	if got := vaultedForEp5.ScopedContentID(bareKey); got != k5 {
		t.Fatalf("vaulted pack targeted at E05 got %q, want %q", got, k5)
	}

	// Untargeted FullSeason pack must never produce shared bare probe keys.
	untargetedPack := Release{
		ID:       "pack-raw",
		Protocol: ProtocolTorrent,
		InfoHash: packHash,
		Info:     ReleaseInfo{FullSeason: true, Seasons: []int{1}},
	}
	if keys := untargetedPack.ProbeContentKeys(); len(keys) != 0 {
		t.Fatalf("untargeted FullSeason pack returned probe keys %v, want nil", keys)
	}

	// Single-file movie torrent stays unscoped (valid movie probes remain reusable).
	movie := Release{
		ID:       "movie-1",
		GUID:     "btih:" + packHash,
		Protocol: ProtocolTorrent,
		InfoHash: packHash,
		Target:   &ReleaseTarget{MovieTitles: []string{"Movie"}, MovieYear: 2024},
	}
	if got := movie.ScopedContentID(bareKey); got != bareKey {
		t.Fatalf("single-file movie key = %q, want %q", got, bareKey)
	}

	// Movie collection btih:<hash>/<fileIdx> matches explicit Target.FileIndex.
	idx := 2
	colFromGUID := Release{
		ID:       "col-guid",
		GUID:     "btih:" + packHash + "/2",
		Protocol: ProtocolTorrent,
		InfoHash: packHash,
	}
	colFromTarget := Release{
		ID:       "col-target",
		GUID:     "btih:" + packHash + "/2",
		Protocol: ProtocolTorrent,
		InfoHash: packHash,
		Target:   &ReleaseTarget{FileIndex: &idx},
	}
	if colFromGUID.ScopedContentID(bareKey) == bareKey {
		t.Fatalf("movie collection GUID /2 stayed unscoped")
	}
	if colFromGUID.ScopedContentID(bareKey) != colFromTarget.ScopedContentID(bareKey) {
		t.Fatalf("movie collection GUID key %q != Target.FileIndex key %q",
			colFromGUID.ScopedContentID(bareKey), colFromTarget.ScopedContentID(bareKey))
	}

	// Untargeted Usenet single-episode release preserves unscoped cross-indexer keys
	// so named and obfuscated indexers still share EffectiveContentID / PreFetchContentID.
	ts := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	usenetNamed := Release{
		ID:         "nzb-a",
		Protocol:   ProtocolUsenet,
		ContentID:  "nzb:aaaa",
		Size:       2 << 30,
		Poster:     "poster@example",
		UsenetDate: ts,
		Info:       ReleaseInfo{Seasons: []int{1}, Episodes: []int{4}},
	}
	usenetObfuscated := Release{
		ID:         "nzb-b",
		Protocol:   ProtocolUsenet,
		ContentID:  "nzb:aaaa",
		Size:       2 << 30,
		Poster:     "poster@example",
		UsenetDate: ts,
	}
	if usenetNamed.EffectiveContentID() != "nzb:aaaa" || usenetNamed.EffectiveContentID() != usenetObfuscated.EffectiveContentID() {
		t.Fatalf("untargeted Usenet EffectiveContentID diverged: named=%q obf=%q",
			usenetNamed.EffectiveContentID(), usenetObfuscated.EffectiveContentID())
	}
	if usenetNamed.PreFetchContentID() != usenetObfuscated.PreFetchContentID() {
		t.Fatalf("untargeted Usenet PreFetchContentID diverged: named=%q obf=%q",
			usenetNamed.PreFetchContentID(), usenetObfuscated.PreFetchContentID())
	}
}
