package frenchcat

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestLoad(t *testing.T) {
	s := Load()
	if s.Enabled || len(s.Countries) != 1 || s.Countries[0] != "FR" || len(s.Releases) != 2 || !s.Types[core.TypeShow] {
		t.Fatalf("défauts = %+v", s)
	}
	t.Setenv("PSE_FRENCH", "oui")
	t.Setenv("PSE_FRENCH_COUNTRIES", "fr, ca")
	t.Setenv("PSE_FRENCH_RELEASES", "qc,fr")
	t.Setenv("PSE_FRENCH_TYPES", "films")
	s = Load()
	if !s.Enabled || s.Countries[1] != "CA" || s.Releases[0] != "vfq" || s.Releases[1] != "french" || s.Types[core.TypeShow] || !s.NeedsHomeReleases() {
		t.Fatalf("réglages = %+v", s)
	}
	if q, _ := s.SQL("t.show_key", "", core.TypeEpisode, 0); q != "" {
		t.Fatalf("épisodes filtrés alors que les séries ne le sont pas : %s", q)
	}
}

func TestInternationalSettings(t *testing.T) {
	t.Setenv("PSE_FRENCH", "on")
	t.Setenv("PSE_CATALOG_ORIGINAL_LANGUAGES", "ja,de")
	t.Setenv("PSE_CATALOG_COUNTRY_EVIDENCE", "off")
	t.Setenv("PSE_FRENCH_RELEASES", "de,pt-br")
	s := Load()
	if len(s.Countries) != 0 || s.NeedsHomeReleases() || len(s.OriginalLanguages) != 2 || s.Releases[0] != "german" || s.Releases[1] != "portuguese-brazil" {
		t.Fatalf("%+v", s)
	}
	t.Setenv("PSE_CATALOG_ORIGINAL_LANGUAGES", "none")
	if s := Load(); s.OriginalLanguages == nil || len(s.OriginalLanguages) != 0 {
		t.Fatalf("none must disable original-language evidence: %+v", s)
	}
}
