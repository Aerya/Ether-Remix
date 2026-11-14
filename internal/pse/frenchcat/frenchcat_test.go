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
	if q, _ := s.SQL("t.show_key", "", core.TypeEpisode, 0, true); q != "" {
		t.Fatalf("épisodes filtrés alors que les séries ne le sont pas : %s", q)
	}
}
