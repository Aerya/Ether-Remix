package resolver

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// A combined release naming two segments ("S01E01-E02", "S01E03E04") is a
// copy of each episode it covers: its concatenated episode titles satisfy
// the title check for either one, naming the sibling episode is not naming
// a different release, and its size is judged against the combined runtime.
// A range that does not include the requested episode is still rejected.
func TestCombinedEpisodeReleaseAcceptedForEachCoveredEpisode(t *testing.T) {
	episodes := map[int]string{1: "Enter the Iron Friends", 2: "Amadeus Cho's Eggcellent Eggventure", 3: "Great Expo-tations", 4: "Bouncy Boots"}
	const double12 = "Iron.Man.and.His.Awesome.Friends.S01E01-E02.Enter.the.Iron.Friends.-.Amadeus.Chos.Eggcellent.Eggventure.1080p.DSNP.WEB-DL.DDP5.1.H.264-Kitsune"
	const double34 = "Iron.Man.and.His.Awesome.Friends.S01E03E04.Great.Expotations..Bouncy.Boots.MULTI.1080p.DSNP.WEB-DL.DDP5.1.x264-AndreMor"
	titleFor := func(ep int) *core.Title {
		return &core.Title{Key: core.EpisodeKey(274388, 1, ep), Type: core.TypeEpisode, ShowIDs: core.IDs{TMDB: 274388},
			ShowTitle: "Iron Man and His Awesome Friends", ShowYear: 2025, Title: episodes[ep], Season: 1, Episode: ep, Runtime: 11 * time.Minute, Language: "en"}
	}
	for _, tc := range []struct {
		name    string
		episode int
		accept  bool
	}{
		{double12, 1, true},
		{double12, 2, true},
		{double34, 3, true},
		{double34, 4, true},
		{double12, 3, false},
		{double34, 1, false},
	} {
		title := titleFor(tc.episode)
		rel := withParsed(rel("x", tc.name, 1157))
		if got := rejectIdentity(rel, title, searchRequestFor(title), "en-US"); (got == "") != tc.accept {
			t.Errorf("E%02d %q: identity verdict %q, accept %v", tc.episode, tc.name, got, tc.accept)
		}
	}
	// The sibling episode's title in the name is not another episode's
	// release when the release covers that sibling too.
	local := localIdentity{core.EpisodeIdentity{Episodes: episodes}}
	title3 := titleFor(3)
	if got := local.reject(withParsed(rel("x", double34, 1157)), title3, searchRequestFor(title3)); got != "" {
		t.Errorf("combined release rejected for naming its other episode: %q", got)
	}
	// Size is judged per covered episode: the same bytes as a single
	// segment exceed the ceiling, as two segments they do not.
	profile := core.QualityProfile{EpisodeMaxSizeMBPerMin: 60}
	if got := rejectPolicy(withParsed(rel("x", double12, 1157)), titleFor(1), profile, core.ClientCaps{}, false, false); got != "" {
		t.Errorf("combined release size rejected: %q", got)
	}
	const single = "Iron.Man.and.His.Awesome.Friends.S01E01.Enter.the.Iron.Friends.1080p.DSNP.WEB-DL.DDP5.1.H.264-GRP"
	if got := rejectPolicy(withParsed(rel("x", single, 1157)), titleFor(1), profile, core.ClientCaps{}, false, false); got != "size too large for runtime" {
		t.Errorf("single-episode release of the same size: %q", got)
	}
}
