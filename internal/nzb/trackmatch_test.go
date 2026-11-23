package nzb

import "testing"

func TestMatchTrackIndexSceneRARAndMultiDisc(t *testing.T) {
	sceneAlbum := []string{
		"00-metallica-72_seasons-2023-moonblood.nfo",
		"00-metallica-72_seasons-2023-moonblood.m3u",
		"00-metallica-72_seasons-2023-moonblood.sfv",
		"cover.jpg",
		"01-metallica-72_seasons-moonblood.flac",
		"02-metallica-shadows_follow-moonblood.flac",
		"03-metallica-screaming_suicide-moonblood.flac",
		"04-metallica-sleepwalk_my_life_away-moonblood.flac",
		"05-metallica-you_must_burn-moonblood.flac",
		"06-metallica-lux_aeterna-moonblood.flac",
	}

	// Track number + title matches scene member even though exact fileHint wouldn't.
	idx, ok, ambig := MatchTrackIndex(sceneAlbum, TrackHint{Number: 5, Title: "You Must Burn!"}, "audio/flac")
	if !ok || ambig || idx != 8 {
		t.Fatalf("MatchTrackIndex(num=5, title) = (%d, %v, %v), want (8, true, false)", idx, ok, ambig)
	}

	// Single-disc album accepts disc=1 when files carry no disc tags.
	idx, ok, ambig = MatchTrackIndex(sceneAlbum, TrackHint{Disc: 1, Number: 5, Title: "You Must Burn!"}, "audio/*")
	if !ok || ambig || idx != 8 {
		t.Fatalf("MatchTrackIndex(disc=1, num=5) = (%d, %v, %v), want (8, true, false)", idx, ok, ambig)
	}

	// Title-only fallback matches normalised tokens (ignoring punctuation & underscores).
	idx, ok, ambig = MatchTrackIndex(sceneAlbum, TrackHint{Title: "You Must Burn!"}, "audio/flac")
	if !ok || ambig || idx != 8 {
		t.Fatalf("MatchTrackIndex(title only) = (%d, %v, %v), want (8, true, false)", idx, ok, ambig)
	}

	// Non-matching track number + title fails cleanly.
	idx, ok, ambig = MatchTrackIndex(sceneAlbum, TrackHint{Number: 99, Title: "Missing Song"}, "audio/flac")
	if ok || ambig || idx != -1 {
		t.Fatalf("MatchTrackIndex(missing) = (%d, %v, %v), want (-1, false, false)", idx, ok, ambig)
	}

	// Exact fileHint (MatchPathIndex) semantics remain unchanged: "05 You Must Burn!" does NOT match.
	if _, ok, _ := MatchPathIndex(sceneAlbum, "05 You Must Burn!"); ok {
		t.Fatalf("MatchPathIndex should keep exact semantics and reject fuzzy hint")
	}

	// Multi-disc release: disc disambiguates, missing disc without title is ambiguous, title breaks tie.
	multiDisc := []string{
		"CD1/05-artist-first_song.flac",
		"CD2/05-artist-second_song.flac",
		"CD1/01-artist-interlude.flac",
		"CD2/09-artist-interlude.flac",
	}
	if _, ok, ambig := MatchTrackIndex(multiDisc, TrackHint{Number: 5}, "audio/flac"); ok || !ambig {
		t.Fatalf("multi-disc num=5 without disc/title: ok=%v ambig=%v, want false/true", ok, ambig)
	}
	if idx, ok, ambig := MatchTrackIndex(multiDisc, TrackHint{Disc: 2, Number: 5}, "audio/flac"); !ok || ambig || idx != 1 {
		t.Fatalf("multi-disc disc=2 num=5 = (%d, %v, %v), want (1, true, false)", idx, ok, ambig)
	}
	if idx, ok, ambig := MatchTrackIndex(multiDisc, TrackHint{Number: 5, Title: "Second Song"}, "audio/flac"); !ok || ambig || idx != 1 {
		t.Fatalf("multi-disc num=5 title tiebreak = (%d, %v, %v), want (1, true, false)", idx, ok, ambig)
	}
	if _, ok, ambig := MatchTrackIndex(multiDisc, TrackHint{Title: "Interlude"}, "audio/flac"); ok || !ambig {
		t.Fatalf("duplicate title without number: ok=%v ambig=%v, want false/true", ok, ambig)
	}
	if idx, ok, ambig := MatchTrackIndex(multiDisc, TrackHint{Disc: 2, Title: "Interlude"}, "audio/flac"); !ok || ambig || idx != 3 {
		t.Fatalf("duplicate title disambiguated by disc = (%d, %v, %v), want (3, true, false)", idx, ok, ambig)
	}

	// Compact disc-track prefix "2-05" works too.
	compact := []string{"1-05_alpha.mp3", "2-05_beta.mp3"}
	if idx, ok, ambig := MatchTrackIndex(compact, TrackHint{Disc: 2, Number: 5}, "audio/mpeg"); !ok || ambig || idx != 1 {
		t.Fatalf("compact 2-05 = (%d, %v, %v), want (1, true, false)", idx, ok, ambig)
	}
}
