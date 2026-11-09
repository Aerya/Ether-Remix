package releasedb

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// SUBFRENCH (sous-titres français) compte comme VOSTFR, dans le nom de la
// release comme dans le choix de langue.
func TestSubfrenchCountsAsVostfr(t *testing.T) {
	film := core.Title{Key: 1, Type: core.TypeMovie}
	for _, name := range []string{
		"Film.2020.SUBFRENCH.1080p.WEB.x264-GRP",
		"Film 2020 SubFrench 1080p WEB",
		"Film.2020.VOSTFR.1080p.WEB.x264-GRP",
	} {
		r := FromCore(film, 0, 0, "a", core.Release{Name: name, Info: releaseparse.Parse(name)}, nil)
		for _, want := range []string{"vostfr", "subfrench", "SUBFRENCH"} {
			if !(Filter{Languages: []string{want}}).Match(r) {
				t.Errorf("%s : option %q refusée (sous-titres %v)", name, want, r.SubtitleLanguages)
			}
		}
		if (Filter{Languages: []string{"french"}}).Match(r) {
			t.Errorf("%s : des sous-titres ne font pas une VF", name)
		}
	}
}
