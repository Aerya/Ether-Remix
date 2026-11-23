package frontend

import (
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Library tab kinds stay owned by core. Adapters read and compare them through
// these constants.
const (
	TabRecommended = core.TabRecommended
	TabLibrary     = core.TabLibrary
	TabCollections = core.TabCollections
	TabPeople      = core.TabPeople
	TabView        = core.TabView
)

// NormalizeCatalogueTabFacet normalizes a catalogue library facet name to its
// canonical singular key, returning ("", false) when unrecognized. For
// collections, an empty facet means owner collections.
func NormalizeCatalogueTabFacet(kind, facet string) (string, bool) {
	return core.NormalizeCatalogueTabFacet(kind, facet)
}

// CatalogueFacetFilter identifies one catalogue library collections-tab facet
// and the bucket IDs selected for it. Frontends construct it from wire
// parameters and apply it to a browse Filter without reaching into core's
// band parsers, credit filters, or filter-group representation.
type CatalogueFacetFilter struct {
	Facet     string
	BucketIDs []string
}

// Apply validates and translates c onto dst. Facet may be a canonical
// collections-tab facet key or any alias recognized by
// NormalizeCatalogueTabFacet(TabCollections, ...). Apply returns false and
// leaves dst unchanged when dst is nil, BucketIDs is empty, Facet is empty or
// unrecognized, or any bucket ID is invalid or conflicts with an existing
// single-value filter field.
func (c CatalogueFacetFilter) Apply(dst *Filter) bool {
	if dst == nil || len(c.BucketIDs) == 0 {
		return false
	}
	canon, valid := core.NormalizeCatalogueTabFacet(core.TabCollections, c.Facet)
	if !valid || canon == "" {
		return false
	}
	return applyCatalogueFacetBuckets(dst, canon, c.BucketIDs)
}

func applyCatalogueFacetBuckets(f *core.Filter, canon string, bucketIDs []string) bool {
	switch canon {
	case "genre":
		f.Genres = append(f.Genres, bucketIDs...)
		return true
	case "studio", "network":
		if len(bucketIDs) != 1 || f.Studio != "" {
			return false
		}
		f.Studio = bucketIDs[0]
		return true
	case "country":
		f.Countries = append(f.Countries, bucketIDs...)
		return true
	case "year":
		years := make([]int, 0, len(bucketIDs))
		for _, raw := range bucketIDs {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 9999 {
				return false
			}
			years = append(years, n)
		}
		f.Years = append(f.Years, years...)
		return true
	case "decade":
		if len(bucketIDs) == 1 && len(f.Years) == 0 && f.YearFrom == 0 && f.YearTo == 0 {
			trimmed := strings.TrimSuffix(strings.TrimSpace(bucketIDs[0]), "s")
			n, err := strconv.Atoi(trimmed)
			if err != nil || n < 1800 || n > 9990 || n%10 != 0 {
				return false
			}
			f.YearFrom = n
			f.YearTo = n + 9
			return true
		}
		decades := make([]int, 0, len(bucketIDs))
		for _, raw := range bucketIDs {
			trimmed := strings.TrimSuffix(strings.TrimSpace(raw), "s")
			n, err := strconv.Atoi(trimmed)
			if err != nil || n < 1800 || n > 9990 || n%10 != 0 {
				return false
			}
			decades = append(decades, n)
		}
		for _, n := range decades {
			for y := n; y <= n+9; y++ {
				f.Years = append(f.Years, y)
			}
		}
		return true
	case "franchise":
		cols := make([]int64, 0, len(bucketIDs))
		for _, raw := range bucketIDs {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				return false
			}
			cols = append(cols, id)
		}
		f.Collections = append(f.Collections, cols...)
		return true
	case "language":
		f.Languages = append(f.Languages, bucketIDs...)
		return true
	case "contentRating":
		f.ContentRatings = append(f.ContentRatings, bucketIDs...)
		return true
	case "tag":
		f.Tags = append(f.Tags, bucketIDs...)
		return true
	case "keyword":
		f.Keywords = append(f.Keywords, bucketIDs...)
		return true
	case "director", "writer":
		people := make([]int64, 0, len(bucketIDs))
		for _, raw := range bucketIDs {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				return false
			}
			people = append(people, id)
		}
		f.Credits = append(f.Credits, core.CreditFilter{People: people, Kind: canon})
		return true
	case "resolution":
		vals := make([]string, 0, len(bucketIDs))
		for _, raw := range bucketIDs {
			v := strings.ToLower(strings.TrimSpace(raw))
			switch v {
			case "4k", "1080p", "720p", "sd":
				vals = append(vals, v)
			default:
				return false
			}
		}
		f.EvidenceResolutions = append(f.EvidenceResolutions, vals...)
		return true
	case "hdr":
		vals := make([]string, 0, len(bucketIDs))
		for _, raw := range bucketIDs {
			v := strings.ToLower(strings.TrimSpace(raw))
			switch v {
			case "dv", "hdr10", "sdr":
				vals = append(vals, v)
			default:
				return false
			}
		}
		f.EvidenceHDR = append(f.EvidenceHDR, vals...)
		return true
	case "audioLanguage":
		vals := make([]string, 0, len(bucketIDs))
		for _, raw := range bucketIDs {
			v := strings.TrimSpace(raw)
			if v == "" {
				return false
			}
			vals = append(vals, v)
		}
		f.EvidenceAudioLanguages = append(f.EvidenceAudioLanguages, vals...)
		return true
	case "rating":
		if len(bucketIDs) == 1 && f.MinRating == 0 && f.MaxRating == 0 {
			minR, maxR, excl, ok := core.ParseCatalogueRatingBand(bucketIDs[0])
			if !ok {
				return false
			}
			f.MinRating = minR
			f.MaxRating = maxR
			f.MaxRatingExclusive = excl
			return true
		}
		children := make([]core.FilterGroup, 0, len(bucketIDs))
		for _, raw := range bucketIDs {
			minR, maxR, excl, ok := core.ParseCatalogueRatingBand(raw)
			if !ok {
				return false
			}
			children = append(children, core.FilterGroup{
				Operator: "and",
				Filter:   &core.Filter{MinRating: minR, MaxRating: maxR, MaxRatingExclusive: excl},
			})
		}
		appendOrFilterGroup(f, children)
		return true
	case "runtime":
		if len(bucketIDs) == 1 && f.MinRuntime == 0 && f.MaxRuntime == 0 {
			minS, maxS, ok := core.ParseCatalogueRuntimeBand(bucketIDs[0])
			if !ok {
				return false
			}
			f.MinRuntime = time.Duration(minS) * time.Second
			if maxS > 0 {
				f.MaxRuntime = time.Duration(maxS) * time.Second
			}
			return true
		}
		children := make([]core.FilterGroup, 0, len(bucketIDs))
		for _, raw := range bucketIDs {
			minS, maxS, ok := core.ParseCatalogueRuntimeBand(raw)
			if !ok {
				return false
			}
			sub := &core.Filter{MinRuntime: time.Duration(minS) * time.Second}
			if maxS > 0 {
				sub.MaxRuntime = time.Duration(maxS) * time.Second
			}
			children = append(children, core.FilterGroup{Operator: "and", Filter: sub})
		}
		appendOrFilterGroup(f, children)
		return true
	case "addedMonth":
		if len(bucketIDs) == 1 && f.EtherAddedFrom == 0 && f.EtherAddedTo == 0 {
			fromTS, toTS, ok := core.ParseCatalogueAddedMonth(bucketIDs[0])
			if !ok {
				return false
			}
			f.EtherAddedFrom = fromTS
			f.EtherAddedTo = toTS
			return true
		}
		children := make([]core.FilterGroup, 0, len(bucketIDs))
		for _, raw := range bucketIDs {
			fromTS, toTS, ok := core.ParseCatalogueAddedMonth(raw)
			if !ok {
				return false
			}
			children = append(children, core.FilterGroup{
				Operator: "and",
				Filter:   &core.Filter{EtherAddedFrom: fromTS, EtherAddedTo: toTS},
			})
		}
		appendOrFilterGroup(f, children)
		return true
	default:
		return false
	}
}

func appendOrFilterGroup(f *core.Filter, children []core.FilterGroup) {
	orGroup := core.FilterGroup{Operator: "or", Children: children}
	if f.Group == nil {
		f.Group = &orGroup
		return
	}
	f.Group = &core.FilterGroup{
		Operator: "and",
		Children: []core.FilterGroup{*f.Group, orGroup},
	}
}
