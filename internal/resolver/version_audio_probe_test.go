package resolver

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestEstimateVariantInfoConservativeMultiAndDualAudio(t *testing.T) {
	jpTitle := &core.Title{Key: core.MovieKey(77), Type: core.TypeMovie, Title: "Spirited Away", Language: "ja", Runtime: 125 * time.Minute}
	audio := func(info core.MediaInfo) (langs []string, def string) {
		for _, st := range info.Streams {
			if st.Type != "audio" {
				continue
			}
			name := releaseparse.LanguageBase(releaseparse.LanguageName(st.Language))
			if st.Language != "" && name == "" {
				name = st.Language
			}
			langs = append(langs, name)
			if st.Default {
				def = name
			}
		}
		return langs, def
	}

	// MULTi + JPN on a Japanese title must never collapse to Japanese-only:
	// it preserves the second track slot (unlabelled when sourceLang is absent,
	// or sourceLang when the indexer declares English).
	multiJPN := withParsed(rel("m1", "Spirited.Away.2001.1080p.BluRay.MULTi.JPN.DDP5.1.H.264-GRP", 8000))
	if langs, _ := audio(estimateVariantInfo(jpTitle, multiJPN, []string{"en"})); !slices.Equal(langs, []string{"japanese", ""}) {
		t.Fatalf("MULTi.JPN without sourceLang = %v, want [japanese \"\"]", langs)
	}
	multiJPN.SourceLanguage = "en"
	if langs, def := audio(estimateVariantInfo(jpTitle, multiJPN, []string{"en"})); !slices.Equal(langs, []string{"japanese", "english"}) || def != "english" {
		t.Fatalf("MULTi.JPN with en sourceLang = %v default %q, want [japanese english] default english", langs, def)
	}

	// Dual-Audio / MULTi without an explicit audio codec token must still list
	// parsed audio languages instead of dropping all audio streams.
	noCodec := withParsed(rel("m2", "Spirited.Away.2001.1080p.BluRay.x265.Dual-Audio.ENG.JPN-GRP", 8000))
	if noCodec.Info.AudioCodec != "" {
		t.Fatalf("expected no audio codec parsed, got %q", noCodec.Info.AudioCodec)
	}
	langs, def := audio(estimateVariantInfo(jpTitle, noCodec, []string{"en"}))
	if !slices.Contains(langs, "english") || !slices.Contains(langs, "japanese") || def != "english" {
		t.Fatalf("Dual-Audio without codec = %v default %q, want english+japanese default english", langs, def)
	}
}

func TestRankingUsesProbedTracksForAudioLanguagePreference(t *testing.T) {
	ctx := context.Background()
	jpTitle := &core.Title{
		Key:      core.MovieKey(88),
		Type:     core.TypeMovie,
		Title:    "Spirited Away",
		Year:     2001,
		Language: "ja",
		Runtime:  125 * time.Minute,
	}

	// High-quality release tagged only JPN in the release name, plus a smaller
	// English-tagged 720p release. Without a probe, a profile requiring English
	// rejects/demotes the JPN-tagged 1080p copy; once probed with both Japanese
	// and English tracks, the 1080p release must not be demoted or rejected.
	highJPN := rel("jpn-1080", "Spirited.Away.2001.1080p.BluRay.JPN.DDP5.1.H.264-GRP", 12000)
	highJPN.ContentID = "nzb:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	lowENG := rel("eng-720", "Spirited.Away.2001.720p.WEB-DL.ENG.DDP5.1.H.264-GRP", 4000)

	cfg := testCfg()
	for i := range cfg.Quality.Profiles {
		cfg.Quality.Profiles[i].Language = "English"
		cfg.Quality.Profiles[i].AudioLanguageMode = core.AudioLanguageModeRequired
		cfg.Quality.Profiles[i].PreferredLanguages = []string{"en"}
	}

	idx := &fakeIndexer{releases: []core.Release{highJPN, lowENG}}
	user := &core.User{ID: 1, AudioLanguages: []string{"en"}}

	// Without probed facts: JPN-only 1080p is rejected by the English profile, so 720p ENG wins.
	svcUnprobed := New(idx, fakeHealth{}, nil, cfg, Options{})
	resUnprobed, err := svcUnprobed.Resolve(ctx, core.ResolveRequest{Title: jpTitle, User: user, Client: appleTVCaps()})
	if err != nil {
		t.Fatalf("unprobed resolve: %v", err)
	}
	if resUnprobed.Release.ID != lowENG.ID {
		t.Fatalf("unprobed winner = %q, want %q", resUnprobed.Release.ID, lowENG.ID)
	}

	// With probed facts showing real Japanese + English tracks: 1080p is accepted and wins.
	probed := core.MediaInfo{
		Container: "mkv",
		Duration:  125 * time.Minute,
		Size:      highJPN.Size,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "jpn", Default: true},
			{Index: 2, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng"},
		},
	}
	svcProbed := New(idx, fakeHealth{}, nil, cfg, Options{
		ProbedMedia: func(r core.Release) (core.MediaInfo, bool) {
			if r.ContentID == highJPN.ContentID {
				return probed, true
			}
			return core.MediaInfo{}, false
		},
	})
	resProbed, err := svcProbed.Resolve(ctx, core.ResolveRequest{Title: jpTitle, User: user, Client: appleTVCaps()})
	if err != nil {
		t.Fatalf("probed resolve: %v", err)
	}
	if resProbed.Release.ID != highJPN.ID {
		t.Fatalf("probed winner = %q, want %q (probe proved English is present)", resProbed.Release.ID, highJPN.ID)
	}

	// Variants must also expose the real probed tracks on the winning version.
	variants, err := svcProbed.Variants(ctx, core.ResolveRequest{Title: jpTitle, User: user, Client: appleTVCaps()}, core.MaxVariants)
	if err != nil || len(variants) == 0 {
		t.Fatalf("variants = %+v err=%v", variants, err)
	}
	if variants[0].Release.ID != highJPN.ID || variants[0].Info.Estimated {
		t.Fatalf("top variant = %+v, want probed %s", variants[0], highJPN.ID)
	}
}
