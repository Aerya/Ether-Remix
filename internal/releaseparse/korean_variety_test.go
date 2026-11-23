package releaseparse

import (
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestKoreanVarietyReleaseParsingAndMatching(t *testing.T) {
	airDate := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
	req := core.SearchRequest{
		Type:              core.TypeEpisode,
		Title:             "Home Alone",
		OriginalTitle:     "나 혼자 산다",
		AlternativeTitles: []string{"I Live Alone"},
		Year:              2013,
		Season:            1,
		Episode:           674,
		Released:          airDate,
	}

	cases := []struct {
		name      string
		wantTitle string
		wantMatch string // expected title/alias TitlesMatch succeeds against
		wantEp    []int
		wantAir   string
		wantYear  int
	}{
		{
			name:      "I.Live.Alone.E674.241115.1080p.WEB-DL.AAC.H.264-xxx",
			wantTitle: "I Live Alone",
			wantMatch: "I Live Alone",
			wantEp:    []int{674},
			wantAir:   "2024-11-15",
			wantYear:  0,
		},
		{
			name:      "Na.Honja.Sanda.E674.241115.720p-NEXT",
			wantTitle: "Na Honja Sanda",
			wantMatch: "나 혼자 산다",
			wantEp:    []int{674},
			wantAir:   "2024-11-15",
			wantYear:  0,
		},
		{
			name:      "나 혼자 산다.E674.241115.1080p",
			wantTitle: "나 혼자 산다",
			wantMatch: "나 혼자 산다",
			wantEp:    []int{674},
			wantAir:   "2024-11-15",
			wantYear:  0,
		},
		{
			name:      "나혼자산다.E674.241115.1080p-NEXT",
			wantTitle: "나혼자산다",
			wantMatch: "나 혼자 산다",
			wantEp:    []int{674},
			wantAir:   "2024-11-15",
			wantYear:  0,
		},
		{
			name:      "나 혼자 산다.674회.241115.1080p",
			wantTitle: "나 혼자 산다",
			wantMatch: "나 혼자 산다",
			wantEp:    []int{674},
			wantAir:   "2024-11-15",
			wantYear:  0,
		},
		{
			name:      "I Live Alone 2013 E674",
			wantTitle: "I Live Alone",
			wantMatch: "I Live Alone",
			wantEp:    []int{674},
			wantAir:   "",
			wantYear:  2013,
		},
		{
			name:      "Home Alone (2013) - S01E674",
			wantTitle: "Home Alone",
			wantMatch: "Home Alone",
			wantEp:    []int{674},
			wantAir:   "",
			wantYear:  2013,
		},
		{
			name:      "I.Live.Alone.241115",
			wantTitle: "I Live Alone",
			wantMatch: "I Live Alone",
			wantEp:    nil,
			wantAir:   "2024-11-15",
			wantYear:  0,
		},
		{
			name:      "I.Live.Alone.2024.11.15.1080p.WEB-DL-NEXT",
			wantTitle: "I Live Alone",
			wantMatch: "I Live Alone",
			wantEp:    nil,
			wantAir:   "2024-11-15",
			wantYear:  0,
		},
		{
			name:      "I.Live.Alone.E674.2024-11-15.1080p.WEB-DL-NEXT",
			wantTitle: "I Live Alone",
			wantMatch: "I Live Alone",
			wantEp:    []int{674},
			wantAir:   "2024-11-15",
			wantYear:  0,
		},
		{
			name:      "나 혼자 산다 / I Live Alone E674 241115 1080p",
			wantTitle: "나 혼자 산다 / I Live Alone",
			wantMatch: "I Live Alone",
			wantEp:    []int{674},
			wantAir:   "2024-11-15",
			wantYear:  0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := Parse(tc.name)
			if info.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", info.Title, tc.wantTitle)
			}
			if !slices.Equal(info.Episodes, tc.wantEp) {
				t.Errorf("Episodes = %v, want %v", info.Episodes, tc.wantEp)
			}
			if info.AirDate != tc.wantAir {
				t.Errorf("AirDate = %q, want %q", info.AirDate, tc.wantAir)
			}
			if info.Year != tc.wantYear {
				t.Errorf("Year = %d, want %d", info.Year, tc.wantYear)
			}
			if sy := EpisodeSeriesYear(tc.name, info.Year); sy != tc.wantYear {
				t.Errorf("EpisodeSeriesYear = %d, want %d", sy, tc.wantYear)
			}
			if IsObfuscated(info.Title) {
				t.Errorf("IsObfuscated(%q) = true, want false", info.Title)
			}
			if !TitleMatches(info, tc.wantMatch) {
				t.Errorf("TitleMatches(%q, %q) = false, want true", info.Title, tc.wantMatch)
			}
			if !EpisodeMatchesRequest(info, req) {
				t.Errorf("EpisodeMatchesRequest(%+v) = false, want true", info)
			}
			if !EpisodeTitleMatches(tc.name, "Special Guest Star") {
				t.Errorf("EpisodeTitleMatches(%q, %q) = false, want true (air date is metadata, not an episode title)", tc.name, "Special Guest Star")
			}
		})
	}
}

func TestKoreanVarietyGuardsAndEdgeCases(t *testing.T) {
	airDate := time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC)
	req := core.SearchRequest{
		Type:              core.TypeEpisode,
		Title:             "Home Alone",
		OriginalTitle:     "나 혼자 산다",
		AlternativeTitles: []string{"I Live Alone"},
		Year:              2013,
		Season:            1,
		Episode:           674,
		Released:          airDate,
	}

	// Wrong air date must not match.
	if info := Parse("I.Live.Alone.241114.1080p"); EpisodeMatchesRequest(info, req) {
		t.Fatalf("wrong air date matched: %+v", info)
	}
	// Explicit wrong episode must not be overridden by matching air date.
	if info := Parse("I.Live.Alone.E673.241115.1080p"); EpisodeMatchesRequest(info, req) {
		t.Fatalf("wrong episode number matched despite matching air date: %+v", info)
	}
	// Bare episode E674 matches multi-season show via AbsoluteEpisode.
	multiReq := core.SearchRequest{
		Type:            core.TypeEpisode,
		Title:           "Long Running Show",
		Season:          4,
		Episode:         1,
		AbsoluteEpisode: 674,
		Released:        airDate,
	}
	if info := Parse("Long.Running.Show.E674.241115.1080p"); !EpisodeMatchesRequest(info, multiReq) {
		t.Fatalf("bare E674 failed to match multi-season AbsoluteEpisode 674: %+v", info)
	}
	// Cumulative bare episode with exact air date matches multi-season show even when AbsoluteEpisode is unknown.
	cumReq := core.SearchRequest{
		Type:     core.TypeEpisode,
		Title:    "2 Days and 1 Night",
		Season:   4,
		Episode:  1,
		Released: time.Date(2019, 12, 8, 0, 0, 0, 0, time.UTC),
	}
	if info := Parse("2.Days.and.1.Night.E600.191208.1080p"); !EpisodeMatchesRequest(info, cumReq) {
		t.Fatalf("cumulative bare E600 + exact air date failed to match S04E01: %+v", info)
	}
	// Korean season token ("시즌4").
	ks := Parse("1박 2일.시즌4.E01.191208.1080p-NEXT")
	if ks.Title != "1박 2일" || !slices.Equal(ks.Seasons, []int{4}) || !slices.Equal(ks.Episodes, []int{1}) || ks.AirDate != "2019-12-08" {
		t.Fatalf("Korean season parse = %+v, want title='1박 2일' S4E1 2019-12-08", ks)
	}
	// Non-Latin single-word title is never obfuscated; ASCII hashes still are.
	if IsObfuscated("나혼자산다") {
		t.Fatal("Hangul title falsely marked obfuscated")
	}
	if !IsObfuscated("a8f3b2c1d9e0") || !IsObfuscated("bcdfghjklm") {
		t.Fatal("ASCII obfuscated tokens should remain obfuscated")
	}
	// Non-date year + range ("Show (2024) 01-02") must keep Year=2024 and AirDate="".
	nonDate := Parse("Show (2024) 01-02 1080p")
	if nonDate.AirDate != "" || nonDate.Year != 2024 {
		t.Fatalf("Show (2024) 01-02 parsed as %+v, want Year=2024 AirDate=''", nonDate)
	}
	// Four-digit bare episode (>999) parses cleanly.
	e1024 := Parse("Running.Man.E1024.241115.1080p-NEXT")
	if !slices.Equal(e1024.Episodes, []int{1024}) || e1024.AirDate != "2024-11-15" {
		t.Fatalf("4-digit bare episode = %+v, want E1024 2024-11-15", e1024)
	}
}
