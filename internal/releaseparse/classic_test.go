package releaseparse

import "testing"

// In a range pack the folder nearest the file decides its season, not the
// outer "Season 1-5" / "S01-S05" (#798). A bare NxM folder never decides.
func TestRangePackSeasonFolderDecides(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"Show.Season.1-5.1080p/Season 1/05 - Pilot.mkv", false},
		{"Show.Season.1-5.1080p/Season 2/05 - Title.mkv", true},
		{"Show.S01-S05.1080p/S01/05 - Pilot.mkv", false},
		{"Show.S01-S05.1080p/S02/05 - Title.mkv", true},
		{"Show.Season.2.DVD/4x3/05 - Title.mkv", true},
	} {
		if got := ClassicPackEpisodeMatches(tc.path, 2, 5); got != tc.want {
			t.Errorf("ClassicPackEpisodeMatches(%q, 2, 5) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
