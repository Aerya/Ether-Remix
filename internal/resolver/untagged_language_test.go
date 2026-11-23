package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// An untagged release is presumed to be in the title's original language, so
// a dub-only profile ranks a declared dub above it at any resolution.
func TestUntaggedReleaseTakesTitleOriginalLanguage(t *testing.T) {
	anime := &core.Title{Language: "ja"}
	english := &core.Title{Language: "en"}
	untagged := releaseparse.Parse("[Group] Show - 001 [2160p]")
	dub := releaseparse.Parse("Show.S01E01.1080p.English.Dub.WEB-DL")
	if audioAcceptable(untagged, "", []string{"english"}, anime) {
		t.Fatal("untagged release of a Japanese title accepted by an English-only profile")
	}
	if !audioAcceptable(dub, "", []string{"english"}, anime) {
		t.Fatalf("declared English dub rejected: %+v", dub.Languages)
	}
	if !audioAcceptable(untagged, "", []string{"english"}, english) {
		t.Fatal("untagged release of an English title rejected")
	}
	if !audioAcceptable(untagged, "", []string{"english", "ja"}, anime) {
		t.Fatal("untagged release rejected when the original language is accepted")
	}
}
