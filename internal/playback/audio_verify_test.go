package playback

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func englishRequiredProfile() core.QualityProfile {
	return core.QualityProfile{
		Name:      "English Dub Only",
		MaxHeight: 1080,
		MinHeight: 720,
		ReleaseRules: []core.ReleaseRule{{
			ID:         "eng-only",
			Name:       "English audio",
			Action:     "require",
			Conditions: []core.ReleaseCondition{{Field: "language", Operator: "equals", Value: "en"}},
		}},
	}
}

func TestOpenInitialVerifiesUnknownAudioAndNeverPlaysJapaneseFallback(t *testing.T) {
	jpnOnly := mkv("jpn-untagged", 10_000, time.Minute)
	jpnOnly.AudioLanguages = []string{"jpn"}

	dualEng := mkv("dual-eng", 10_000, time.Minute)
	dualEng.AudioLanguages = []string{"jpn", "eng"}

	h := newHarness(t, []*fakeRelease{jpnOnly, dualEng}, []string{"jpn-untagged", "dual-eng"})
	h.catalog.titles[movieKey].Language = "ja"
	profile := englishRequiredProfile()
	h.m.deps.SelectProfile = func(core.ClientCaps, *core.User, string) core.QualityProfile { return profile }
	h.resolver.candidates = []core.Release{
		{ID: "jpn-untagged", Name: "[SubsPlease] One Piece - 001 (1080p).mkv", Info: releaseparse.Parse("[SubsPlease] One Piece - 001 (1080p).mkv")},
		{ID: "dual-eng", Name: "[Group] One Piece - 001 [Dual Audio].mkv", Info: releaseparse.Parse("[Group] One Piece - 001 [Dual Audio].mkv")},
	}

	ps := h.prepare(owner, movieKey, "c1")
	if ps.Resolution.Release.ID != "dual-eng" {
		t.Fatalf("Prepare selected %s, want dual-eng after rejecting Japanese-only probe", ps.Resolution.Release.ID)
	}
	h.m.Stop(ps.ID)

	// When every candidate probes without English (Japanese-only and Japanese+Spanish),
	// Prepare must reject with "audio is not in the profile's language (english)"
	// and NEVER play a Japanese fallback.
	jpnSpa := mkv("dual-spa", 10_000, time.Minute)
	jpnSpa.AudioLanguages = []string{"jpn", "spa"}
	h2 := newHarness(t, []*fakeRelease{jpnOnly, jpnSpa}, []string{"jpn-untagged", "dual-spa"})
	h2.catalog.titles[movieKey].Language = "ja"
	h2.m.deps.SelectProfile = func(core.ClientCaps, *core.User, string) core.QualityProfile { return profile }
	h2.resolver.candidates = []core.Release{
		{ID: "jpn-untagged", Name: "[SubsPlease] One Piece - 001 (1080p).mkv", Info: releaseparse.Parse("[SubsPlease] One Piece - 001 (1080p).mkv")},
		{ID: "dual-spa", Name: "[Group] One Piece - 001 [Dual Audio].mkv", Info: releaseparse.Parse("[Group] One Piece - 001 [Dual Audio].mkv")},
	}

	_, err := h2.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c2"))
	if !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("Prepare error = %v, want core.ErrNotAvailable", err)
	}
	if !strings.Contains(err.Error(), "audio is not in the profile's language (english)") {
		t.Fatalf("Prepare error %q missing clear language rejection reason", err.Error())
	}
}

func TestOpenInitialBoundsUnknownAudioProbesAtFour(t *testing.T) {
	var fakes []*fakeRelease
	var ids []string
	var cands []core.Release
	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("u%d", i)
		fr := mkv(id, 10_000, time.Minute)
		if i <= 4 {
			fr.AudioLanguages = []string{"jpn"}
		} else {
			fr.AudioLanguages = []string{"jpn", "eng"}
		}
		fakes = append(fakes, fr)
		ids = append(ids, id)
		name := fmt.Sprintf("[Group%d] One Piece - 001 [Dual Audio].mkv", i)
		cands = append(cands, core.Release{ID: id, Name: name, Info: releaseparse.Parse(name)})
	}

	h := newHarness(t, fakes, ids)
	h.m.opts.MaxAttempts = 8
	h.catalog.titles[movieKey].Language = "ja"
	profile := englishRequiredProfile()
	h.m.deps.SelectProfile = func(core.ClientCaps, *core.User, string) core.QualityProfile { return profile }
	h.resolver.candidates = cands

	_, err := h.m.Prepare(context.Background(), owner, movieKey, defaultCaps("c1"))
	if !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("Prepare after 4 failed unknown-audio probes = %v, want ErrNotAvailable", err)
	}
	if got := h.engine.openCount(); got != maxUnknownAudioProbes {
		t.Fatalf("opened %d unknown-language sources, want at most %d", got, maxUnknownAudioProbes)
	}
	if !strings.Contains(err.Error(), "audio is not in the profile's language (english)") {
		t.Fatalf("Prepare error %q missing language rejection reason", err.Error())
	}
}
