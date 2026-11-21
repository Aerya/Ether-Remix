package app

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// Le câblage Remix de « Continuer à regarder » doit atteindre les surfaces
// construites par le service (Plex) : sans réglages, la rangée garderait le
// plafond de 50 séries d'Ether.
func TestSurfaceContinueCarriesPSESettings(t *testing.T) {
	x := &surfaceExperience{Service: &experience.Service{}, protocol: "plex", rowGen: func() uint64 { return 0 }}
	c, ok := x.NewContinue(frontend.ContinueOptions{}).(experience.Continue)
	if !ok {
		t.Fatal("NewContinue ne renvoie pas un experience.Continue")
	}
	if c.PSE == nil {
		t.Fatal("réglages PSE absents de la rangée Plex")
	}
	if got := c.PSE(); got.ScanLimit != 200 || got.NewDays != 60 {
		t.Fatalf("valeurs par défaut inattendues : %+v", got)
	}
}
