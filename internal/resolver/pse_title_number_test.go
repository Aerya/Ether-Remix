package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestPSETitleWithNumber(t *testing.T) {
	for _, c := range []struct {
		name, want string
		year       int
	}{
		{"Blade.Runner.2099.S01E01.MULTi.1080p.WEB.H264-BYOR", "Blade Runner 2099", 0},
		{"Blade.Runner.2049.2017.MULTi.1080p.BluRay.x264-X", "Blade Runner 2049", 2017},
		{"Blade.Runner.1982.MULTi.1080p.BluRay.x264-X", "Blade Runner", 1982},
		{"Other.Show.2099.S01E01.1080p.WEB-X", "Blade Runner 2099", 2099},
	} {
		rel := core.Release{Name: c.name, Info: releaseparse.Parse(c.name)}
		got := pseTitleWithNumber(rel, core.SearchRequest{Title: c.want})
		if got.Info.Year != c.year {
			t.Fatalf("%s: année %d, attendu %d (titre %q)", c.name, got.Info.Year, c.year, got.Info.Title)
		}
		if c.year == 0 && !releaseparse.TitleMatches(got.Info, c.want) {
			t.Fatalf("%s: titre %q", c.name, got.Info.Title)
		}
	}
}
