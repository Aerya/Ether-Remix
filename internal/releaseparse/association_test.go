package releaseparse

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestAssociationRealSourceAnswers(t *testing.T) {
	movie := core.SearchRequest{Type: core.TypeMovie, Title: "Incident au Loch Ness", OriginalTitle: "Incident at Loch Ness", Year: 2004, IDs: core.IDs{TMDB: 48463, IMDB: "tt0374639"}}
	show := core.SearchRequest{Type: core.TypeEpisode, Title: "Selena", OriginalTitle: "Selena", Year: 1997, Season: 1, Episode: 1, IDs: core.IDs{TMDB: 100}}
	for _, tc := range []struct {
		name string
		q    core.SearchRequest
		r    core.Release
		ok   bool
	}{
		{"translated movie", movie, core.Release{Name: "Incident.at.Loch.Ness.2004.1080p.WEBRip.x265"}, true},
		{"russian descriptor", movie, core.Release{Name: "Incident at Loch Ness [2004, DVDRip] DVO + Original (Eng) + Sub (Rus)"}, true},
		{"bilingual movie", movie, core.Release{Name: "Incident.au.Loch.Ness.(Incident.at.Loch.Ness).2004.1080p"}, true},
		{"live source ignored IMDb", movie, core.Release{Name: "the.unthinkable.daughter.2026.polish.hdr.2160p.web.h265-chopin"}, false},
		{"wrong movie", movie, core.Release{Name: "Superman.2025.1080p.BluRay.HEVC.10bit.AAC5.1-LuCY"}, false},
		{"echoed request is not evidence", movie, core.Release{Name: "Elio.2025.2160p.UHD.BluRay.REMUX", TitleIDs: movie.IDs, TitleIDsEchoed: true}, false},
		{"identifier contradiction", movie, core.Release{Name: "Incident.at.Loch.Ness.2004.1080p", TitleIDs: core.IDs{TMDB: 999}}, false},
		{"reported identity localized name", movie, core.Release{Name: "Rencontre.au.Loch.Ness.2004.1080p", TitleIDs: movie.IDs}, true},
		{"missing stream", movie, core.Release{Name: "aucun_flux", TitleIDs: movie.IDs}, false},
		{"wrong series", show, core.Release{Name: "VisionQuest.S01E01.1080p.DSNP.WEBRip"}, false},
		{"namesake year", show, core.Release{Name: "Selena.La.Serie.2020.S01.MULTI.1080p.WEBRip"}, false},
		{"correct episode", show, core.Release{Name: "Selena.1997.S01E01.1080p"}, true},
		{"correct season pack", show, core.Release{Name: "Selena.1997.S01.MULTI.1080p", Protocol: core.ProtocolTorrent}, true},
		{"wrong season pack", show, core.Release{Name: "Selena.1997.S02.MULTI.1080p", Protocol: core.ProtocolTorrent}, false},
		{"different episode", show, core.Release{Name: "Selena.1997.S01E02.1080p", TitleIDs: show.IDs}, false},
		{"movie given episode", movie, core.Release{Name: "Incident.at.Loch.Ness.2004.S01E01", TitleIDs: movie.IDs}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reason := AssociationReason(tc.q, tc.r)
			if (reason == "") != tc.ok {
				t.Fatalf("reason=%q expected allowed=%v", reason, tc.ok)
			}
		})
	}
}
