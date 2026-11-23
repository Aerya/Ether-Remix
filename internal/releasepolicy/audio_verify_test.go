package releasepolicy

import (
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestUnverifiedAudioLanguagesAndProbedVerification(t *testing.T) {
	englishOnly, err := Compile([]core.ReleaseRule{{
		ID: "eng", Name: "English audio", Action: "require",
		Conditions: []core.ReleaseCondition{{Field: "language", Operator: "equals", Value: "en"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"[Group] One Piece - 001 [Dual Audio]",
		"[Group] One Piece - 001 [Multi Audio]",
		"[SubsPlease] One Piece - 001 (1080p) [A1B2C3D4].mkv",
	} {
		rel := core.Release{Name: name, Info: releaseparse.Parse(name)}
		langs, unverified := englishOnly.UnverifiedAudioLanguages(rel, Context{OriginalLanguage: "ja"})
		if !unverified || !slices.Equal(langs, []string{"english"}) {
			t.Fatalf("%q: got unverified=%v langs=%v, want true [english]", name, unverified, langs)
		}
	}

	// Declared Japanese-only is a positive Miss, never unverified.
	jpn := core.Release{Name: "One.Piece.S01E01.JAPANESE.1080p.WEB-DL-GRP", Info: releaseparse.Parse("One.Piece.S01E01.JAPANESE.1080p.WEB-DL-GRP")}
	if _, unverified := englishOnly.UnverifiedAudioLanguages(jpn, Context{OriginalLanguage: "ja"}); unverified {
		t.Fatal("explicit Japanese release must not be treated as unverified")
	}

	// Generic primary tag when region/script is required is unverified until probed.
	latamOnly, err := Compile([]core.ReleaseRule{{
		ID: "latam", Name: "Latin American Spanish", Action: "require",
		Conditions: []core.ReleaseCondition{{Field: "language", Operator: "equals", Value: "es-419"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	genericSpanish := core.Release{Name: "Show.S01E01.SPANISH.1080p.WEB-DL-GRP", Info: releaseparse.Parse("Show.S01E01.SPANISH.1080p.WEB-DL-GRP")}
	if langs, unverified := latamOnly.UnverifiedAudioLanguages(genericSpanish, Context{OriginalLanguage: "ja"}); !unverified || len(langs) != 1 {
		t.Fatalf("generic Spanish under es-419 rule: unverified=%v langs=%v", unverified, langs)
	}
	if missing, ok := latamOnly.RequiredAudioMissing(genericSpanish, Context{}, []core.StreamInfo{
		{Type: "audio", Language: "spa", Title: "Español Latino"},
	}, false); !ok || missing != "" {
		t.Fatalf("probed Latino track rejected: ok=%v missing=%q", ok, missing)
	}
	if missing, ok := latamOnly.RequiredAudioMissing(genericSpanish, Context{}, []core.StreamInfo{
		{Type: "audio", Language: "spa", Title: "Castellano"},
	}, false); ok || missing == "" {
		t.Fatalf("probed Castilian track accepted for es-419: ok=%v missing=%q", ok, missing)
	}

	// Probed Dual Audio: ja+en passes; ja+es fails with "english".
	dual := core.Release{Name: "[Group] One Piece - 001 [Dual Audio]", Info: releaseparse.Parse("[Group] One Piece - 001 [Dual Audio]")}
	if missing, ok := englishOnly.RequiredAudioMissing(dual, Context{OriginalLanguage: "ja"}, []core.StreamInfo{
		{Type: "audio", Language: "jpn"},
		{Type: "audio", Language: "eng"},
	}, false); !ok || missing != "" {
		t.Fatalf("probed ja+en rejected: ok=%v missing=%q", ok, missing)
	}
	if missing, ok := englishOnly.RequiredAudioMissing(dual, Context{OriginalLanguage: "ja"}, []core.StreamInfo{
		{Type: "audio", Language: "jpn"},
	}, false); ok || missing != "english" {
		t.Fatalf("probed ja-only: got ok=%v missing=%q, want false/english", ok, missing)
	}
}
