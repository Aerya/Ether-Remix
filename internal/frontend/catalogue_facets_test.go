package frontend

import (
	"slices"
	"testing"
	"time"
)

func TestCatalogueFacetFilterApply(t *testing.T) {
	t.Run("simple and alias facets", func(t *testing.T) {
		var f Filter
		cases := []CatalogueFacetFilter{
			{Facet: "genres", BucketIDs: []string{"Action", "Sci-Fi"}},
			{Facet: "studio", BucketIDs: []string{"A24"}},
			{Facet: "countries", BucketIDs: []string{"US"}},
			{Facet: "years", BucketIDs: []string{"2020", "2024"}},
			{Facet: "franchises", BucketIDs: []string{"10", "20"}},
			{Facet: "languages", BucketIDs: []string{"en", "ja"}},
			{Facet: "content_rating", BucketIDs: []string{"PG-13"}},
			{Facet: "tags", BucketIDs: []string{"staff-pick"}},
			{Facet: "keywords", BucketIDs: []string{"cyberpunk"}},
			{Facet: "directors", BucketIDs: []string{"137427"}},
			{Facet: "writers", BucketIDs: []string{"42", "99"}},
			{Facet: "resolutions", BucketIDs: []string{"4K", "1080p"}},
			{Facet: "hdr", BucketIDs: []string{"DV", "hdr10"}},
			{Facet: "audio-language", BucketIDs: []string{"en", " fr "}},
		}
		for _, c := range cases {
			if !c.Apply(&f) {
				t.Fatalf("Apply(%+v) = false, want true", c)
			}
		}
		if !slices.Equal(f.Genres, []string{"Action", "Sci-Fi"}) ||
			f.Studio != "A24" ||
			!slices.Equal(f.Countries, []string{"US"}) ||
			!slices.Equal(f.Years, []int{2020, 2024}) ||
			!slices.Equal(f.Collections, []int64{10, 20}) ||
			!slices.Equal(f.Languages, []string{"en", "ja"}) ||
			!slices.Equal(f.ContentRatings, []string{"PG-13"}) ||
			!slices.Equal(f.Tags, []string{"staff-pick"}) ||
			!slices.Equal(f.Keywords, []string{"cyberpunk"}) ||
			!slices.Equal(f.EvidenceResolutions, []string{"4k", "1080p"}) ||
			!slices.Equal(f.EvidenceHDR, []string{"dv", "hdr10"}) ||
			!slices.Equal(f.EvidenceAudioLanguages, []string{"en", "fr"}) {
			t.Fatalf("unexpected filter fields: %+v", f)
		}
		if len(f.Credits) != 2 ||
			f.Credits[0].Kind != "director" || !slices.Equal(f.Credits[0].People, []int64{137427}) ||
			f.Credits[1].Kind != "writer" || !slices.Equal(f.Credits[1].People, []int64{42, 99}) {
			t.Fatalf("unexpected credits: %+v", f.Credits)
		}
	})

	t.Run("decade single vs multi", func(t *testing.T) {
		var single Filter
		if !(CatalogueFacetFilter{Facet: "decade", BucketIDs: []string{"2020s"}}).Apply(&single) {
			t.Fatal("single decade failed")
		}
		if single.YearFrom != 2020 || single.YearTo != 2029 || len(single.Years) != 0 {
			t.Fatalf("single decade = %d..%d years=%v", single.YearFrom, single.YearTo, single.Years)
		}

		var multi Filter
		if !(CatalogueFacetFilter{Facet: "decades", BucketIDs: []string{"1990s", "2020"}}).Apply(&multi) {
			t.Fatal("multi decade failed")
		}
		if multi.YearFrom != 0 || multi.YearTo != 0 || len(multi.Years) != 20 || multi.Years[0] != 1990 || multi.Years[19] != 2029 {
			t.Fatalf("multi decade = %d..%d years=%v", multi.YearFrom, multi.YearTo, multi.Years)
		}
	})

	t.Run("rating runtime and addedMonth single and multi OR groups", func(t *testing.T) {
		var single Filter
		if !(CatalogueFacetFilter{Facet: "rating", BucketIDs: []string{"8-8.9"}}).Apply(&single) {
			t.Fatal("single rating failed")
		}
		if single.MinRating != 8.0 || single.MaxRating != 9.0 || !single.MaxRatingExclusive || single.Group != nil {
			t.Fatalf("single rating = %+v", single)
		}
		if !(CatalogueFacetFilter{Facet: "runtime", BucketIDs: []string{"90-120m"}}).Apply(&single) {
			t.Fatal("single runtime failed")
		}
		if single.MinRuntime != 5400*time.Second || single.MaxRuntime != 7199*time.Second {
			t.Fatalf("single runtime = %v..%v", single.MinRuntime, single.MaxRuntime)
		}
		if !(CatalogueFacetFilter{Facet: "addedMonth", BucketIDs: []string{"2026-02"}}).Apply(&single) {
			t.Fatal("single addedMonth failed")
		}
		wantFrom := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Unix()
		wantTo := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Add(-time.Second).Unix()
		if single.EtherAddedFrom != wantFrom || single.EtherAddedTo != wantTo {
			t.Fatalf("single addedMonth = %d..%d, want %d..%d", single.EtherAddedFrom, single.EtherAddedTo, wantFrom, wantTo)
		}

		var openEnd Filter
		if !(CatalogueFacetFilter{Facet: "runtime", BucketIDs: []string{"180m-plus"}}).Apply(&openEnd) {
			t.Fatal("180m-plus failed")
		}
		if openEnd.MinRuntime != 10800*time.Second || openEnd.MaxRuntime != 0 {
			t.Fatalf("180m-plus = %v..%v", openEnd.MinRuntime, openEnd.MaxRuntime)
		}

		var multi Filter
		if !(CatalogueFacetFilter{Facet: "rating", BucketIDs: []string{"9-10", "under-6"}}).Apply(&multi) {
			t.Fatal("multi rating failed")
		}
		if multi.Group == nil || multi.Group.Operator != "or" || len(multi.Group.Children) != 2 {
			t.Fatalf("multi rating group = %+v", multi.Group)
		}
		if !(CatalogueFacetFilter{Facet: "runtime", BucketIDs: []string{"under-30m", "180m-plus"}}).Apply(&multi) {
			t.Fatal("multi runtime failed")
		}
		if multi.Group.Operator != "and" || len(multi.Group.Children) != 2 {
			t.Fatalf("combined group = %+v", multi.Group)
		}
		if !(CatalogueFacetFilter{Facet: "addedMonth", BucketIDs: []string{"2026-01", "2026-02"}}).Apply(&multi) {
			t.Fatal("multi addedMonth failed")
		}
		if multi.Group.Operator != "and" || len(multi.Group.Children) != 2 || multi.Group.Children[1].Operator != "or" || len(multi.Group.Children[1].Children) != 2 {
			t.Fatalf("triple combined group = %+v", multi.Group)
		}
	})

	t.Run("rejects invalid inputs without mutating filter", func(t *testing.T) {
		if (CatalogueFacetFilter{Facet: "genre", BucketIDs: []string{"Action"}}).Apply(nil) {
			t.Fatal("nil destination accepted")
		}
		base := Filter{Studio: "A24", Years: []int{2020}}
		bad := []CatalogueFacetFilter{
			{Facet: "", BucketIDs: []string{"Action"}},
			{Facet: "unknown", BucketIDs: []string{"x"}},
			{Facet: "genre", BucketIDs: nil},
			{Facet: "studio", BucketIDs: []string{"Warner"}},
			{Facet: "network", BucketIDs: []string{"HBO", "FX"}},
			{Facet: "year", BucketIDs: []string{"2021", "notayear"}},
			{Facet: "decade", BucketIDs: []string{"2025s"}},
			{Facet: "franchise", BucketIDs: []string{"10", "0"}},
			{Facet: "director", BucketIDs: []string{"-1"}},
			{Facet: "resolution", BucketIDs: []string{"4k", "8k"}},
			{Facet: "hdr", BucketIDs: []string{"hdr10+"}},
			{Facet: "audioLanguage", BucketIDs: []string{"en", "   "}},
			{Facet: "rating", BucketIDs: []string{"11-12"}},
			{Facet: "runtime", BucketIDs: []string{"short"}},
			{Facet: "addedMonth", BucketIDs: []string{"2026-13"}},
		}
		for _, c := range bad {
			f := Filter{Studio: base.Studio, Years: append([]int(nil), base.Years...)}
			if c.Apply(&f) {
				t.Fatalf("Apply(%+v) = true, want false", c)
			}
			if f.Studio != base.Studio || !slices.Equal(f.Years, base.Years) || f.Group != nil {
				t.Fatalf("Apply(%+v) mutated filter on failure: %+v", c, f)
			}
		}
	})
}

func TestNormalizeCatalogueTabFacet(t *testing.T) {
	if canon, ok := NormalizeCatalogueTabFacet(TabCollections, "genres"); !ok || canon != "genre" {
		t.Fatalf("got (%q, %v), want (genre, true)", canon, ok)
	}
	if canon, ok := NormalizeCatalogueTabFacet(TabCollections, "added_month"); !ok || canon != "addedMonth" {
		t.Fatalf("got (%q, %v), want (addedMonth, true)", canon, ok)
	}
	if canon, ok := NormalizeCatalogueTabFacet(TabCollections, "platforms"); ok || canon != "" {
		t.Fatalf("got (%q, %v), want (\"\", false)", canon, ok)
	}
}
