package resolver

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestYearlessEpisodeRescueAfterRetained4KExceedsClient(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		t.Run(map[bool]string{true: "unique catalogue identity", false: "ambiguous catalogue identity"}[confirmed], func(t *testing.T) {
			old := withParsed(rel("retained-4k", "The.Rings.of.Power.2022.S01E01.2160p.WEB.H264-GRP", 4000))
			fresh := withParsed(rel("yearless-1080", "The.Rings.of.Power.S01E01.1080p.WEB.H264-GRP", 1500))
			idx := &rescueFakeIndexer{fakeIndexer: &fakeIndexer{releases: []core.Release{old}}, rescued: []core.Release{fresh}}
			prepared := 0
			s := New(idx, fakeHealth{}, nil, testCfg(), Options{
				ReferenceAdmission: releaseparse.AssociationReason,
				PrepareReferenceRequest: func(_ context.Context, q core.SearchRequest) core.SearchRequest {
					prepared++
					q.SeriesIdentityChecked = confirmed
					return q
				},
			})
			defer s.Close()
			title := &core.Title{Key: core.EpisodeKey(84773, 1, 1), ShowKey: core.ShowKey(84773), Type: core.TypeEpisode, IDs: core.IDs{TMDB: 84773}, Title: "The Rings of Power", Year: 2022, ShowYear: 2022, Season: 1, Episode: 1, Runtime: time.Hour}
			client := appleTV4K()
			client.MaxHeight = 1080
			result, err := s.Resolve(t.Context(), core.ResolveRequest{Title: title, Client: client})
			if confirmed {
				if err != nil {
					t.Fatal(err)
				}
				if result.Release.ID != "yearless-1080" {
					t.Fatalf("wrong release: %s", result.Release.ID)
				}
			} else if err == nil {
				t.Fatal("ambiguous yearless rescue admitted")
			}
			if prepared == 0 {
				t.Fatal("reference identity preparation bypassed")
			}
		})
	}
}
