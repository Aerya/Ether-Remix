package releasedb

import (
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestFrenchTags(t *testing.T) {
	for name, want := range map[string]bool{
		"Film.2023.TRUEFRENCH.1080p.WEB.H264-GRP": true,
		"Film.2023.VFF.1080p.WEB.H264-GRP":        true,
		"Film.2023.VFQ.1080p.WEB.H264-GRP":        true,
		"Film.2023.VFi.1080p.WEB.H264-GRP":        true,
		"Film 2023 MULTi.VFI.1080p.HDLight-GRP":   true,
		"Film.2023.VF.1080p.WEB.H264-GRP":         true,
		"Film.2023.VOF.1080p.WEB.H264-GRP":        true,
		"Film.2023.FRENCH.1080p.WEB.H264-GRP":     true,
		"Film.2023.VOSTFR.1080p.WEB.H264-GRP":     false, // sous-titres seulement, pas une VF
		"Film.2023.1080p.WEB.H264-VFXGRP":         false,
		"Vfx.Masters.2023.1080p.WEB.H264-GRP":     false,
		"Film.2023.ENGLISH.1080p.WEB.H264-GRP":    false,
	} {
		got := slices.Contains(releaseLanguages(name, releaseparse.Parse(name).Languages), "french")
		if got != want {
			t.Errorf("%s : français = %v, attendu %v", name, got, want)
		}
	}
}

func TestVFIMarker(t *testing.T) {
	name := "Film 2023 MULTi.VFi.1080p.BluRay-GRP"
	got := releaseLanguages(name, releaseparse.Parse(name).Languages)
	if !slices.Contains(got, "vfi") || !slices.Contains(got, "french") {
		t.Fatalf("languages = %v", got)
	}
	if canonicalLanguage("VFi") != "vfi" {
		t.Fatal("vfi doit rester sélectionnable seul")
	}
}
