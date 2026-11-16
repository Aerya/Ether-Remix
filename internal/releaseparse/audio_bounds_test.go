package releaseparse

import "testing"

func TestAudioTagsAtTitleBoundary(t *testing.T) {
	for _, text := range []string{"", "A title without metadata", "Un été à Paris", "MULTI", "DUAL AUDIO"} {
		for _, floor := range []int{len(text) - 1, len(text), len(text) + 1, int(^uint(0) >> 1)} {
			t.Run(text, func(t *testing.T) {
				if multiAudioAfter(text, floor) {
					t.Fatal("audio tag after end of title")
				}
				if got := dualAudioKindAfter(text, floor); got != "" {
					t.Fatalf("dual tag after end of title: %q", got)
				}
			})
		}
	}
}

func TestParseTitleWithoutAudioMetadata(t *testing.T) {
	for _, name := range []string{"A title without metadata", "Un été à Paris", "A title 2026", "Une série S01E02"} {
		t.Run(name, func(t *testing.T) {
			info := Parse(name)
			if len(info.Languages) != 0 {
				t.Fatalf("title interpreted as audio metadata: %v", info.Languages)
			}
		})
	}
}

func TestAudioTagsRemainRecognizedAfterBoundary(t *testing.T) {
	if !multiAudioAfter("Title MULTI", 4) || !multiAudioAfter("MULTI", -1) {
		t.Fatal("valid MULTI audio tag lost")
	}
	if got := dualAudioKindAfter("Title DUAL AUDIO", 4); got != LanguageMulti {
		t.Fatalf("DUAL AUDIO: %q", got)
	}
	if got := dualAudioKindAfter("Title DUAL", 4); got != LanguageDual {
		t.Fatalf("DUAL: %q", got)
	}
	if multiAudioAfter("Title MULTI SUB", 4) || dualAudioKindAfter("Title DUAL SUB", 4) != "" {
		t.Fatal("subtitle tag interpreted as audio")
	}
}
