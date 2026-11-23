package livetv

import (
	"context"

	"github.com/braedonsaunders/ether/internal/contentrating"
)

type groupRatingRule struct {
	rating string
	order  int
}

type dynamicGroupRatingRule struct {
	compiled *compiledOwnerGroup
	rule     groupRatingRule
}

type groupRatingIndex struct {
	channels map[string]groupRatingRule
	groups   map[OwnerGroupSource]groupRatingRule
	all      groupRatingRule
	dynamic  []dynamicGroupRatingRule
}

func newGroupRatingIndex() *groupRatingIndex {
	return &groupRatingIndex{
		channels: map[string]groupRatingRule{},
		groups:   map[OwnerGroupSource]groupRatingRule{},
	}
}

// Compile saved membership once, without enumerating the channel library.
// When groups overlap, the first rated, visible group in the owner's order wins.
func indexGroupRatings(gs []OwnerGroup) *groupRatingIndex {
	return compileOwnerGroupIndex(gs).ratings
}

func (x *groupRatingIndex) rating(c Channel) string {
	if x == nil {
		return ""
	}
	r := x.all
	for _, candidate := range []groupRatingRule{x.channels[c.ID], x.groups[OwnerGroupSource{c.SourceID, c.Group}]} {
		if candidate.rating != "" && (r.rating == "" || candidate.order < r.order) {
			r = candidate
		}
	}
	for i := range x.dynamic {
		d := x.dynamic[i]
		if (r.rating == "" || d.rule.order < r.order) && d.compiled.matchesChannel(c) {
			r = d.rule
		}
	}
	return r.rating
}

func (s *Service) inheritedGroupRating(ctx context.Context, c Channel) (string, error) {
	idx, err := s.cachedOwnerGroupIndex(ctx)
	if err != nil || idx == nil {
		return "", err
	}
	return idx.ratings.rating(c), nil
}

// resolveChannelRating is the single precedence rule used by all client policy
// checks. Provider-classified adult channels never inherit either default, and
// uncertified adult items never fall back to a channel or group unrated rating.
func resolveChannelRating(c Channel, p *Programme, group, source string, adult bool) string {
	if p != nil {
		if p.Item.EnforcementRating == contentrating.UncertifiedAdult {
			return contentrating.UncertifiedAdult
		}
		if p.Item.Rating != "" {
			return p.Item.Rating
		}
		if p.Item.EnforcementRating != "" {
			return p.Item.EnforcementRating
		}
	}
	if c.Rating != "" {
		return c.Rating
	}
	if c.Kind != "iptv" || adult {
		return ""
	}
	if group != "" {
		return group
	}
	return source
}

func (s *Service) channelContentRating(ctx context.Context, c Channel, p *Programme) string {
	if c.Kind != "iptv" || c.Rating != "" || (p != nil && (p.Item.Rating != "" || p.Item.EnforcementRating != "")) {
		return resolveChannelRating(c, p, "", "", false)
	}
	e, err := s.sourceEntry(ctx, c.SourceID)
	if err != nil {
		return ""
	}
	adult := c.ProviderAdult || c.adult || adultGroupName.MatchString(c.Group) || e.adultGroups[normGroup(c.Group)]
	if adult {
		return ""
	}
	group, err := s.inheritedGroupRating(ctx, c)
	if err != nil {
		return ""
	}
	return resolveChannelRating(c, p, group, e.unratedRating, adult)
}
