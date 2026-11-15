package livetv

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type guideRatingRule struct {
	rating string
	order  int
}
type guideRatingIndex struct {
	channels map[string]guideRatingRule
	groups   map[GuideGroup]guideRatingRule
	all      guideRatingRule
	at       time.Time
}

// Compile saved membership once, without enumerating the channel library.
// When guides overlap, the first rated, visible guide in the owner's order wins.
func indexGuideRatings(gs []OwnerGuide) *guideRatingIndex {
	x := &guideRatingIndex{channels: map[string]guideRatingRule{}, groups: map[GuideGroup]guideRatingRule{}}
	for i, g := range gs {
		if g.Hidden || g.UnratedRating == "" {
			continue
		}
		r := guideRatingRule{g.UnratedRating, i}
		if g.All && x.all.rating == "" {
			x.all = r
		}
		for _, id := range g.Channels {
			if _, ok := x.channels[id]; !ok {
				x.channels[id] = r
			}
		}
		for _, group := range g.Groups {
			if _, ok := x.groups[group]; !ok {
				x.groups[group] = r
			}
		}
	}
	return x
}
func (x *guideRatingIndex) rating(c Channel) string {
	r := x.all
	for _, candidate := range []guideRatingRule{x.channels[c.ID], x.groups[GuideGroup{c.SourceID, c.Group}]} {
		if candidate.rating != "" && (r.rating == "" || candidate.order < r.order) {
			r = candidate
		}
	}
	return r.rating
}
func (s *Service) inheritedGuideRating(ctx context.Context, c Channel) (string, error) {
	s.guideRatingsMu.Lock()
	defer s.guideRatingsMu.Unlock()
	if s.guideRatings == nil || s.o.Now().Sub(s.guideRatings.at) >= authCacheTTL {
		// Read only saved guides: generating default guides would scan the lineup.
		var raw []byte
		err := s.o.DB.QueryRowContext(ctx, `SELECT data FROM livetv_owner_guides WHERE id=1`).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		var gs []OwnerGuide
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &gs); err != nil {
				return "", err
			}
		}
		s.guideRatings = indexGuideRatings(gs)
		s.guideRatings.at = s.o.Now()
	}
	return s.guideRatings.rating(c), nil
}

// resolveChannelRating is the single precedence rule used by all client policy
// checks. Provider-classified adult channels never inherit either default.
func resolveChannelRating(c Channel, p *Programme, guide, source string, adult bool) string {
	if p != nil && p.Item.Rating != "" {
		return p.Item.Rating
	}
	if c.Rating != "" {
		return c.Rating
	}
	if c.Kind != "iptv" || adult {
		return ""
	}
	if guide != "" {
		return guide
	}
	return source
}
func (s *Service) channelContentRating(ctx context.Context, c Channel, p *Programme) string {
	if c.Kind != "iptv" || c.Rating != "" || (p != nil && p.Item.Rating != "") {
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
	guide, err := s.inheritedGuideRating(ctx, c)
	if err != nil {
		return ""
	}
	return resolveChannelRating(c, p, guide, e.unratedRating, adult)
}
