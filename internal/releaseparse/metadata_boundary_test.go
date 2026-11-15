package releaseparse

import (
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Language and edition words need adjacent release evidence, while format
// tokens are unambiguous even without a release year or resolution.
func TestReleaseMetadataTitleBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, title string
		languages   []string
		source      core.Source
		codec       string
	}{
		{"Terkel.In.Trouble.2004.1080p.SWEDiSH.WEB.h264-SYNKKiDS", "Terkel In Trouble", []string{"swedish"}, core.SourceWEBDL, "h264"},
		{"Terkel.I.Knibe.2004.1080p.DANISH.WEBRip.x264-L5", "Terkel I Knibe", []string{"danish"}, core.SourceWEBRip, "h264"},
		{"Terkel.I.Knipe.NORWEGIAN.DivX.DVDRip-iP", "Terkel I Knipe", []string{"norwegian"}, core.SourceDVD, "xvid"},
		{"Terkel.I.Knipe.NORSK.XviD.DVDRip-iP", "Terkel I Knipe", []string{"norwegian"}, core.SourceDVD, "xvid"},
		{"Movie.Title.DVDRip-GRP", "Movie Title", nil, core.SourceDVD, ""},
		{"Movie.Title.DivX-GRP", "Movie Title", nil, core.SourceUnknown, "xvid"},
		{"Movie.Title.BDRip.XviD-GRP", "Movie Title", nil, core.SourceBluRay, "xvid"},
		{"Movie.Title.HDTV.x264-GRP", "Movie Title", nil, core.SourceHDTV, "h264"},
		{"Movie.Title.DANISH.NORWEGIAN.DivX-GRP", "Movie Title", []string{"norwegian", "danish"}, core.SourceUnknown, "xvid"},
		{"Movie.Title.NORWEGIAN-iP", "Movie Title", []string{"norwegian"}, core.SourceUnknown, ""},
		{"Movie.Title.Directors.Cut.DVDRip-GRP", "Movie Title", nil, core.SourceDVD, ""},
		{"Movie.Title.Extended-GRP", "Movie Title", nil, core.SourceUnknown, ""},
		{"French.Kiss.DivX.DVDRip-GRP", "French Kiss", nil, core.SourceDVD, "xvid"},
		{"The.Danish.Girl.WEBRip.x264-GRP", "The Danish Girl", nil, core.SourceWEBRip, "h264"},
		{"The.German.Doctor.BDRip.XviD-GRP", "The German Doctor", nil, core.SourceBluRay, "xvid"},
		{"The.Good.German.2006.1080p.BluRay.x264-GRP", "The Good German", nil, core.SourceBluRay, "h264"},
		{"German.DVDRip-GRP", "German", nil, core.SourceDVD, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.name)
			if got.Title != tc.title || !slices.Equal(got.Languages, tc.languages) || got.Source != tc.source || got.VideoCodec != tc.codec {
				t.Errorf("got title=%q languages=%v source=%q codec=%q; want %q %v %q %q", got.Title, got.Languages, got.Source, got.VideoCodec, tc.title, tc.languages, tc.source, tc.codec)
			}
		})
	}
}
