package collections

import (
	"context"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type cardSampleSource interface {
	CollectionCardSample(context.Context, core.Filter, int64, string, string, *core.FilterGroup, string, int) (core.Title, error)
}

// WithCollectionCardFilter installs the same indexed viewer predicates used
// for library browsing, including the viewer's share and availability rules.
func WithCollectionCardFilter(filter func(context.Context, *core.User, core.MediaType) (core.Filter, bool, error)) Option {
	return func(s *Service) { s.cardFilter = filter }
}

// CollectionCardHeaders reads enabled shared collection metadata and proves it
// has visible content without building or materializing its members.
func (s *Service) CollectionCardHeaders(ctx context.Context, user *core.User, keys []core.RatingKey) (map[core.RatingKey]*core.Collection, error) {
	out := make(map[core.RatingKey]*core.Collection, len(keys))
	probe, ok := s.cat.(cardSampleSource)
	if !ok || s.cardFilter == nil || user == nil || len(keys) == 0 {
		return out, nil
	}
	ctx = core.WithCatalogViewer(ctx, user)
	ids := make([]int64, 0, len(keys))
	for _, key := range keys {
		base, section := key.CollectionSection()
		if base.Kind() == core.KindSmart && section == "" && !key.IsEphemeral() {
			ids = append(ids, base.TMDB())
		}
	}
	rows, err := s.rowsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	type policy struct {
		filter core.Filter
		fits   bool
	}
	filters := make(map[core.MediaType]policy, 2)
	for _, row := range rows {
		if !visible(row, user) || row.owner != 0 {
			continue
		}
		var facet, value string
		if path, ok := localFacetSpec(row.spec); ok {
			_, rest, _ := strings.Cut(path, "|")
			facet, value, _ = strings.Cut(rest, "|")
		} else if row.itemCount == 0 || row.buildHash == "" {
			continue
		}
		types := []core.MediaType{core.MediaType(row.spec.MediaType)}
		if row.spec.MediaType == MediaMixed {
			types = []core.MediaType{core.TypeMovie, core.TypeShow}
		}
		for _, typ := range types {
			p, ok := filters[typ]
			if !ok {
				p.filter, p.fits, err = s.cardFilter(ctx, user, typ)
				if err != nil {
					return nil, err
				}
				filters[typ] = p
			}
			if !p.fits {
				continue
			}
			filter := p.filter
			var anyPersonal *core.FilterGroup
			filter.Group, anyPersonal = cardPersonalRules(row.spec, user.ID)
			sample, err := probe.CollectionCardSample(ctx, filter, row.id, facet, value, anyPersonal, row.spec.ReleaseMode, row.spec.LookaheadDays)
			if err != nil {
				return nil, err
			}
			if sample.Key == 0 {
				continue
			}
			c := s.collectionWithArt(ctx, row, "", nil, nil)
			c.Type = sample.Type
			// The cached count is a hint for the owner. For other
			// viewers the probe only proves one visible member.
			c.ChildCount = 1
			if user.IsOwner {
				c.ChildCount = max(1, row.itemCount)
			}
			if c.RankedTop10 {
				c.ChildCount = min(c.ChildCount, 10)
			}
			if c.PosterPath == "" {
				c.PosterPath = sample.PosterPath
			}
			if c.BackdropPath == "" {
				c.BackdropPath = sample.BackdropPath
			}
			out[c.Key] = &c
			break
		}
	}
	return out, nil
}

func cardPersonalRules(spec core.CollectionSpec, viewer int64) (required, anyPersonal *core.FilterGroup) {
	var all, any []core.FilterGroup
	for _, rules := range []struct {
		rules   []core.CollectionRule
		exclude bool
	}{{spec.Rules, false}, {spec.Exclude, true}} {
		compiled, _ := compileRules(rules.rules)
		for _, rule := range compiled {
			if !rule.personal() {
				continue
			}
			match := rule.boolVal != ((rule.op == "isNot") != rules.exclude)
			watched := core.FilterGroup{UserID: viewer, StateField: "viewCount", StateOperator: ">"}
			zeroCount := watched
			zeroCount.StateOperator = "="
			noHistory := false
			unwatched := core.FilterGroup{Operator: "or", Children: []core.FilterGroup{
				zeroCount, {UserID: viewer, History: &noHistory},
			}}
			g := unwatched
			if match {
				g = watched
			}
			if rule.field == "inProgress" {
				offset := core.FilterGroup{UserID: viewer, StateField: "viewOffset", StateOperator: ">"}
				if match {
					g = core.FilterGroup{Operator: "and", Children: []core.FilterGroup{offset, unwatched}}
				} else {
					offset.StateOperator = "<="
					g = core.FilterGroup{Operator: "or", Children: []core.FilterGroup{offset, watched}}
				}
			}
			if spec.Match == MatchAny && !rules.exclude {
				any = append(any, g)
			} else {
				all = append(all, g)
			}
		}
	}
	if len(all) > 0 {
		required = &core.FilterGroup{Operator: "and", Children: all}
	}
	if len(any) > 0 {
		anyPersonal = &core.FilterGroup{Operator: "or", Children: any}
	}
	return
}

// CustomCardArtwork is the owner console's preview of assigned artwork. It
// shares the image policy and sizing of Collection Studio.
func (s *Service) CustomCardArtwork(ctx context.Context, id string, width int) ([]byte, string, time.Time, error) {
	row, err := s.rowByID(ctx, id)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	if row.owner != 0 || !row.enabled || row.spec.Artwork.Mode != "custom" || row.spec.Artwork.URL == "" {
		return nil, "", time.Time{}, core.ErrNotFound
	}
	return s.artworkFor(ctx, row, nil, width)
}
