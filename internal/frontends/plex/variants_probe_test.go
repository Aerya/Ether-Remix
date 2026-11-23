package plex

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestAudioDisplayTitleOmitsEmptyParensWithoutCodec(t *testing.T) {
	if got := audioDisplayTitle(frontend.StreamInfo{Type: "audio", Language: "en"}); got != "English" {
		t.Fatalf("empty codec/channels = %q, want %q", got, "English")
	}
	if got := audioDisplayTitle(frontend.StreamInfo{Type: "audio", Language: "ja", Channels: 6}); got != "日本語 (5.1)" {
		t.Fatalf("empty codec with 5.1 = %q, want %q", got, "日本語 (5.1)")
	}
}

func TestPlayVersionMetadataUsesProbedVariantAudioTracks(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	jpMovie := fightClub
	jpMovie.Language = "ja"

	probed := core.MediaInfo{
		Container: "mkv",
		Duration:  2 * time.Hour,
		Size:      8 << 30,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "jpn", Default: true},
			{Index: 2, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng"},
		},
	}
	f.srv.setVariants(&fakeVariantResolver{byKey: map[core.RatingKey][]core.ReleaseVariant{
		jpMovie.Key: {
			{
				ID:      "r-probed",
				Label:   "1080p BluRay",
				Auto:    true,
				Release: core.Release{ID: "r-probed", Name: "Anime.2024.1080p.BluRay.MULTi.JPN-GRP", Size: 8 << 30},
				Info:    probed,
			},
			{
				ID:      "r-alt-probed",
				Label:   "1080p WEB-DL",
				Release: core.Release{ID: "r-alt-probed", Name: "Anime.2024.1080p.WEB-DL.MULTi-GRP", Size: 4 << 30},
				Info:    probed,
			},
		},
	}})

	user := ownerUser
	user.AudioLanguages = []string{"en"}
	pr := &Request{User: &user, ClientID: "probe-client-1"}
	md := f.srv.metadata(context.Background(), pr, &jpMovie, renderOpts{detail: true, media: true})
	if len(md.Media) != 2 {
		t.Fatalf("media count = %d, want 2", len(md.Media))
	}
	for idx, media := range md.Media {
		var langs []string
		selectedLang := ""
		for _, st := range media.Part[0].Stream {
			if st.StreamType != 2 {
				continue
			}
			langs = append(langs, st.LanguageCode)
			if st.Selected != nil && *st.Selected {
				selectedLang = st.LanguageCode
			}
		}
		if len(langs) != 2 || langs[0] != "jpn" || langs[1] != "eng" {
			t.Fatalf("media[%d] audio codes = %v, want [jpn eng]", idx, langs)
		}
		if selectedLang != "eng" {
			t.Fatalf("media[%d] selected audio = %q, want eng for English viewer", idx, selectedLang)
		}
	}
}
