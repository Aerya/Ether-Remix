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
		{"reported identity localized name", movie, core.Release{Name: "Rencontre.au.Loch.Ness.2004.1080p", TitleIDs: movie.IDs}, false},
		{"missing stream", movie, core.Release{Name: "aucun_flux", TitleIDs: movie.IDs}, false},
		{"wrong series", show, core.Release{Name: "VisionQuest.S01E01.1080p.DSNP.WEBRip"}, false},
		{"namesake year", show, core.Release{Name: "Selena.La.Serie.2020.S01.MULTI.1080p.WEBRip"}, false},
		{"correct episode", show, core.Release{Name: "Selena.1997.S01E01.1080p"}, true},
		{"correct season pack", show, core.Release{Name: "Selena.1997.S01.MULTI.1080p", Protocol: core.ProtocolTorrent}, false},
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

func TestAssociationMatchingIDNeverOverridesFilename(t *testing.T) {
	q := core.SearchRequest{Type: core.TypeMovie, Title: "Le gang sauvage", OriginalTitle: "Motorpsycho!", Year: 1965, IDs: core.IDs{TMDB: 5720, IMDB: "tt0059474"}}
	for _, tc := range []struct {
		name    string
		allowed bool
	}{
		{"Motorpsycho.1965.VOSTFR.1080p.BluRay.AAC.2.0.x264", true},
		{"Motor Psycho.1965 (Russ Meyer) VOSTFR.DVD5.PAL", true},
		{"Les Gendarmes de Saint-Tropez - L'intégrale 1964-1982.FRENCH.BLURAY.REMUX", false},
		{"Motorpsycho.1966.1080p", false},
		{"Motorpsycho.1965-1970.Collection.1080p", false},
		{"Motorpsycho.1080p", false},
		{"Motorpsycho.II.1965.1080p", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AssociationReason(q, core.Release{Name: tc.name, TitleIDs: q.IDs}); (got == "") != tc.allowed {
				t.Fatalf("allowed=%v reason=%s", tc.allowed, got)
			}
		})
	}
}

func TestAssociationSeriesMissingCatalogueYearNeedsReportedIdentity(t *testing.T) {
	q := core.SearchRequest{Type: core.TypeEpisode, Title: "Selena", Season: 1, Episode: 1, IDs: core.IDs{TMDB: 100}}
	r := core.Release{Name: "Selena.1997.S01E01.1080p"}
	if AssociationReason(q, r) == "" {
		t.Fatal("unknown catalogue year accepted without independent ID")
	}
	r.TitleIDs = q.IDs
	if reason := AssociationReason(q, r); reason != "" {
		t.Fatal(reason)
	}
}

func TestAssociationYearlessEpisodesNeedCatalogueCheck(t *testing.T) {
	q := core.SearchRequest{Type: core.TypeEpisode, Title: "Le Seigneur des anneaux : Les Anneaux de pouvoir", OriginalTitle: "The Lord of the Rings: The Rings of Power", Year: 2022, Season: 1, Episode: 1, IDs: core.IDs{TMDB: 84773, IMDB: "tt7631058"}, SeriesIdentityChecked: true}
	for _, tc := range []struct{ name, want string }{
		{"The.Lord.of.the.Rings.The.Rings.of.Power.S01E01.MULTi.VF2.1080p.WEB.H265-FW", ""},
		{"The.Lord.Of.The.Rings.The.Rings.Of.Power.S01E01.MULTi.TRUEFRENCH.VF2.1080p.AMZN.WEB-DL.DDP5.1.H264-FRATERNiTY", ""},
		{"The.Lord.of.the.Rings.The.Rings.of.Power.S01E01.A.Shadow.of.the.Past.MULTi.1080p.AMZN.WEB.H264-BYOR", ""},
		{"the.lord.of.the.rings.the.rings.of.power.s01e01.multi.1080p.web.h264-higgsboson", ""},
		{"The Lord of the Rings The Rings of Power (2022) S01E01 MULTi VFF 2160p 10bit 4KLight DV HDR10Plus WebRip DDP 5.1 Atmos x265-QTZ", ""},
		{"The.Lord.of.the.Rings.The.Rings.of.Power.2019.S01E01.MULTi.1080p.WEB.H265-GRP", "year_conflict"},
		{"The.Lord.of.the.Rings.The.Rings.of.Power.S01E02.MULTi.1080p.WEB.H265-GRP", "episode_conflict"},
		{"The.Power.S01E01.MULTi.VF2.1080p.WEBRip.AC3.5.1.H265-TARDiS", "unconfirmed_title"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := AssociationReason(q, core.Release{Name: tc.name}); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	r := core.Release{Name: "The.Lord.of.the.Rings.The.Rings.of.Power.S01E01.1080p"}
	q.SeriesIdentityChecked = false
	if got := AssociationReason(q, r); got != "unconfirmed_series_year" {
		t.Fatal(got)
	}
	r.TitleIDs, r.TitleIDsEchoed = q.IDs, true
	if AssociationReason(q, r) == "" {
		t.Fatal("echoed identifiers accepted")
	}
	r.TitleIDsEchoed = false
	if got := AssociationReason(q, r); got != "" {
		t.Fatal(got)
	}
	q.SeriesIdentityChecked, q.Year = true, 0
	r.TitleIDs = core.IDs{}
	if AssociationReason(q, r) == "" {
		t.Fatal("unknown catalogue year accepted")
	}
}
