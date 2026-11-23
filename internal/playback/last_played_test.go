package playback

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// rememberedDenyResolver is a resolver whose current identity rules no
// longer accept a remembered release for the title.
type rememberedDenyResolver struct{ *fakeResolver }

func (rememberedDenyResolver) RememberedReleaseAllowed(context.Context, *core.Title, core.Release) bool {
	return false
}

const lastPlayedName = "The.Signal.1999.1080p.WEB-DL.DDP5.1.H.264-GRP"

func lastPlayedRelease(name string) core.Release {
	return core.Release{ID: "r1", Name: name, Size: 2 << 30, Info: releaseparse.Parse(name)}
}

// plantPlayed records r1 as the release that last served the owner's movie,
// resolved under another profile selection key than the current one.
func plantPlayed(h *harness, rel core.Release, nzb []byte, originalLanguage string) {
	h.m.resume.noteLive(resumeRecord{
		userID: owner.ID, key: movieKey, release: rel, profile: "elsewhere|0000000000000000",
		originalLanguage: originalLanguage, updatedAt: h.clock.Now(),
	}, nzb)
}

func originalAudioRule(action string) core.ReleaseRule {
	return core.ReleaseRule{ID: "orig", Name: "Original audio", Action: action, Conditions: []core.ReleaseCondition{{Field: "audio_matches_original", Operator: "equals"}}}
}

// A detail warm-up whose search found nothing may use the release the viewer
// last played only while the current profile, source preference and title
// identity would still choose it.
func TestLastPlayedResolutionGuards(t *testing.T) {
	cases := []struct {
		name    string
		release string
		nzb     []byte
		lang    string // catalogue original language stored with the play
		rules   []core.ReleaseRule
		setup   func(h *harness)
		caps    func(*core.ClientCaps)
		want    bool
	}{
		{name: "accepted", want: true},
		{name: "no payload", nzb: []byte{}},
		{name: "title identity rejects", setup: func(h *harness) {
			h.m.deps.Resolver = rememberedDenyResolver{h.resolver}
		}},
		{name: "source preference excludes", setup: func(h *harness) {
			cfg := h.m.config()
			cfg.Resolver.SourcePreference = "debrid"
			h.m.SetConfig(cfg)
		}},
		{name: "source preference includes", want: true, setup: func(h *harness) {
			cfg := h.m.config()
			cfg.Resolver.SourcePreference = "usenet"
			h.m.SetConfig(cfg)
		}},
		{name: "open failure recorded", setup: func(h *harness) {
			h.m.mu.Lock()
			h.m.openFailures = map[core.RatingKey]map[string]bool{movieKey: {"r1": true}}
			h.m.mu.Unlock()
		}},
		{name: "blocked", setup: func(h *harness) { h.resolver.MarkBad(context.Background(), "r1", "owner block") }},
		// A prefer-only original-audio rule never rejects the release, but a
		// play chosen under another catalogue language must rank again.
		{name: "original language changed", lang: "fr", rules: []core.ReleaseRule{originalAudioRule("prefer")}},
		{name: "original language unchanged", lang: "en", rules: []core.ReleaseRule{originalAudioRule("prefer")}, want: true},
		{name: "release rule rejects", rules: []core.ReleaseRule{{ID: "no-web", Name: "No WEB-DL", Action: "avoid", Conditions: []core.ReleaseCondition{{Field: "name", Operator: "contains", Value: "WEB-DL"}}}}},
		{name: "release rule allows", rules: []core.ReleaseRule{{ID: "no-cam", Name: "No CAM", Action: "avoid", Conditions: []core.ReleaseCondition{{Field: "name", Operator: "contains", Value: "CAM"}}}}, want: true},
		// The profile and the device's caps stop at 1080: no 2160p release.
		{name: "above the profile's height", release: "The.Signal.1999.2160p.WEB-DL.DDP5.1.H.265-GRP"},
		// Within the profile, but above what this device's caps allow.
		{name: "above the device's height", caps: func(c *core.ClientCaps) { c.MaxHeight = 720 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 136*time.Minute)}, nil)
			name := tc.release
			if name == "" {
				name = lastPlayedName
			}
			rel := lastPlayedRelease(name)
			nzb := tc.nzb
			if nzb == nil {
				nzb = []byte("nzb-r1")
			}
			plantPlayed(h, rel, nzb, tc.lang)
			if tc.setup != nil {
				tc.setup(h)
			}
			caps := defaultCaps("detail")
			profile := h.m.selectProfile(caps, owner, movieKey)
			profile.ReleaseRules = tc.rules
			if tc.caps != nil {
				tc.caps(&caps)
			}
			title := *h.catalog.titles[movieKey]
			title.Language = "en"
			got := h.m.lastPlayedResolution(context.Background(), owner.ID, &title, profile, caps)
			if (got != nil) != tc.want {
				t.Fatalf("lastPlayedResolution = %+v, want accepted=%v", got, tc.want)
			}
			if got != nil && (got.Release.ID != "r1" || string(got.NZB) != "nzb-r1" || got.Title != movieKey) {
				t.Fatalf("lastPlayedResolution = %+v, want r1 with its stored payload", got)
			}
		})
	}
}

// settleWarm waits until the warm-up for id has published a source or ended
// without one; an entry that ended without a source is already removed.
func settleWarm(t *testing.T, h *harness, id identity) {
	t.Helper()
	h.m.mu.Lock()
	e := h.m.warm[id]
	h.m.mu.Unlock()
	if e == nil {
		return
	}
	select {
	case <-e.done:
	case <-time.After(5 * time.Second):
		t.Fatal("warm-up did not finish")
	}
}

func warmReleaseID(h *harness, id identity) string {
	h.m.mu.Lock()
	defer h.m.mu.Unlock()
	if e := h.m.warm[id]; e != nil && e.res != nil {
		return e.res.Release.ID
	}
	return ""
}

func setSearch(h *harness, rels ...core.Release) {
	h.resolver.mu.Lock()
	h.resolver.candidates = rels
	h.resolver.mu.Unlock()
}

// channelCaps are the caps of the Live TV channel warm-up (app/livetv.go).
func channelCaps() core.ClientCaps {
	return core.ClientCaps{Product: "Ether Live TV", ClientID: "ether-channel-prewarm", MaxHeight: 1080, MaxBitrateKbps: 6000, BroadcastBitrateKbps: 6000,
		VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3", "eac3"}, Containers: []string{"mp4", "mkv"}}
}

// The detail warm-up falls back to the last played release only when its
// Resolve found no source, and only for a detail page: a search that answers
// keeps its pick, and other warm-up reasons and a Live TV channel warm-up end
// without a source. The fallback lists the file's tracks without
// prefetching its bytes.
func TestDetailWarmUsesLastPlayedOnlyWhenResolveFails(t *testing.T) {
	cases := []struct {
		name       string
		reason     string
		channel    bool
		results    []string // releases the search answers with
		want       string   // warm release, "" for none
		prefetched string   // release whose bytes were prefetched, "" for none
	}{
		{"detail, search fails", core.PrewarmDetail, false, nil, "r1", ""},
		{"detail, search answers", core.PrewarmDetail, false, []string{"r2"}, "r2", "r2"},
		{"trailer, search fails", core.PrewarmTrailer, false, nil, "", ""},
		{"post-play, search fails", core.PrewarmPostPlay, false, nil, "", ""},
		{"live TV channel, search fails", core.PrewarmDetail, true, nil, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 136*time.Minute), mkv("r2", 2<<30, 136*time.Minute)}, tc.results)
			plantPlayed(h, lastPlayedRelease(lastPlayedName), []byte("nzb-r1"), "")
			caps := defaultCaps("detail")
			opts := core.PrewarmOptions{Reason: tc.reason}
			if tc.channel {
				caps = channelCaps()
				opts = core.PrewarmOptions{Reason: tc.reason, DuringPlayback: true, PrefetchBytes: 32 << 20}
			}
			id := identity{userID: owner.ID, clientID: caps.ClientID, key: movieKey, surface: h.m.playbackSurface(string(caps.Protocol))}
			if !h.m.Prewarm(context.Background(), owner, movieKey, caps, opts) {
				t.Fatal("warm-up not admitted")
			}
			settleWarm(t, h, id)
			if got := h.resolver.callCount(); got != 1 {
				t.Fatalf("resolver calls %d, want 1 (the remembered pick is for another profile)", got)
			}
			got := warmReleaseID(h, id)
			h.m.mu.Lock()
			_, missed := h.m.warmMisses[id]
			h.m.mu.Unlock()
			if got != tc.want {
				t.Fatalf("warm release %q, want %q", got, tc.want)
			}
			if missed != (tc.want == "") {
				t.Fatalf("warm miss recorded=%v, want %v", missed, tc.want == "")
			}
			h.m.Close() // waits for the warm-up's prefetch to end
			h.engine.mu.Lock()
			prefetch := ""
			for _, c := range h.engine.prefetch {
				prefetch += c.ReleaseID
			}
			h.engine.mu.Unlock()
			if prefetch != tc.prefetched {
				t.Fatalf("prefetched %q, want %q", prefetch, tc.prefetched)
			}
		})
	}
}

// A detail page that lists the tracks of a release played under another
// profile does not make it the source of Play: once search answers, Play
// on that device opens and remembers the release its own profile selects.
func TestPlayAfterLastPlayedDetailSelectsUnderItsProfile(t *testing.T) {
	h := newHarness(t, []*fakeRelease{
		mkv("r1", 4<<30, 136*time.Minute),
		{ID: "r2", Name: "r2.mkv", Size: 40 << 30, Duration: 136 * time.Minute, Height: 2160, Codec: "hevc"},
	}, nil)
	const uhd = "The.Signal.1999.2160p.UHD.BluRay.REMUX.HDR.HEVC.TrueHD-GRP"
	r1 := core.Release{ID: "r1", Name: lastPlayedName, Size: 4 << 30, Info: releaseparse.Parse(lastPlayedName)}
	r2 := core.Release{ID: "r2", Name: uhd, Size: 40 << 30, Info: releaseparse.Parse(uhd)}
	ctx := context.Background()

	setSearch(h, r1)
	ps := h.prepare(owner, movieKey, "phone") // 1080p profile
	h.m.Stop(ps.ID)
	h.m.waitResumeSaves()

	setSearch(h)
	tv := defaultCaps("tv") // 4K profile
	tv.MaxHeight, tv.VideoCodecs, tv.HDR10 = 2160, []string{"h264", "hevc"}, true
	id := identity{userID: owner.ID, clientID: tv.ClientID, key: movieKey, surface: h.m.playbackSurface(string(tv.Protocol))}
	if !h.m.Prewarm(ctx, owner, movieKey, tv, core.PrewarmOptions{Reason: core.PrewarmDetail}) {
		t.Fatal("warm-up not admitted")
	}
	settleWarm(t, h, id)
	if got := warmReleaseID(h, id); got != "r1" {
		t.Fatalf("detail warm release %q, want the last played r1", got)
	}

	setSearch(h, r2)
	before := h.resolver.callCount()
	ps, err := h.m.Prepare(ctx, owner, movieKey, tv)
	if err != nil {
		t.Fatal(err)
	}
	h.m.Stop(ps.ID)
	h.m.waitResumeSaves()
	if got := ps.Resolution.Release.ID; got != "r2" || h.resolver.callCount() == before {
		t.Fatalf("Play opened %s after %d resolves, want r2 from a fresh resolve", got, h.resolver.callCount()-before)
	}
	if got := warmReleaseID(h, id); got != "" {
		t.Fatalf("detail warm entry %q kept after Play", got)
	}
	rec, err := h.m.resume.get(ctx, owner.ID, movieKey)
	if err != nil || rec == nil || rec.releaseID != "r2" || rec.profile != profileKey(h.m.selectProfile(tv, owner, movieKey)) {
		t.Fatalf("stored record %+v (err %v), want r2 under the 4K profile", rec, err)
	}
}
