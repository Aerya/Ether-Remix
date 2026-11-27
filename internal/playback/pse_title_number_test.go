package playback

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestPSESeriesYearTitleNumber(t *testing.T) {
	title := &core.Title{Type: core.TypeEpisode, ShowTitle: "Blade Runner 2099", ShowYear: 2026, Season: 1, Episode: 1}
	n := "Blade.Runner.2099.S01E01.MULTi.VF2.1080p.WEB.EAC3.5.1.Atmos.H264-FRQC"
	if y := pseSeriesYear(core.Release{Name: n, Info: releaseparse.Parse(n)}, title); y != 2026 {
		t.Fatalf("année %d", y)
	}
	o := "Other.Show.2099.S01E01.1080p.WEB-X"
	if y := pseSeriesYear(core.Release{Name: o, Info: releaseparse.Parse(o)}, title); y != 2099 {
		t.Fatalf("autre série : année %d", y)
	}
}
