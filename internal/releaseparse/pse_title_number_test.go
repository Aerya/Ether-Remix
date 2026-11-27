package releaseparse

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestPSEAssociationTitleWithNumber(t *testing.T) {
	req := core.SearchRequest{Type: core.TypeEpisode, Title: "Blade Runner 2099", Year: 2026, Season: 1, Episode: 1, SeriesIdentityChecked: true}
	r := core.Release{Name: "Blade.Runner.2099.S01E01.Hades.MULTi.1080p.AMZN.WEB.H264-BYOR"}
	if got := AssociationReason(req, r); got != "" {
		t.Fatalf("raison %q", got)
	}
	r.Name = "Other.Show.2099.S01E01.1080p.WEB-X"
	if AssociationReason(req, r) == "" {
		t.Fatal("autre série acceptée")
	}
}
