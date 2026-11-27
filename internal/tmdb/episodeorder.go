package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// EpisodeGroupSummary describes a TMDB alternate order (type 1 original air,
// 2 absolute, 3 DVD, 4 digital, 5 story arc, 6 production, 7 TV).
type EpisodeGroupSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Type         int    `json:"type"`
	EpisodeCount int    `json:"episode_count"`
	GroupCount   int    `json:"group_count"`
}
type OrderedEpisode struct {
	Episode
	Order int `json:"order"`
}
type EpisodeSubgroup struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Order    int              `json:"order"`
	Episodes []OrderedEpisode `json:"episodes"`
}
type EpisodeGroup struct {
	EpisodeGroupSummary
	Groups []EpisodeSubgroup `json:"groups"`
}

func (c *Client) EpisodeGroups(ctx context.Context, show int64) ([]EpisodeGroupSummary, error) {
	var out struct {
		Results []EpisodeGroupSummary `json:"results"`
	}
	err := c.getJSON(ctx, fmt.Sprintf("/tv/%d/episode_groups", show), url.Values{}, &out)
	return out.Results, err
}
func (c *Client) EpisodeGroup(ctx context.Context, id string) (*EpisodeGroup, error) {
	var out EpisodeGroup
	err := c.getJSON(ctx, "/tv/episode_group/"+url.PathEscape(id), url.Values{}, &out)
	return &out, err
}

// GroupSeasonOffset converts subgroup order to the displayed season number.
func GroupSeasonOffset(g *EpisodeGroup) int {
	// TMDB subgroup order is already the season number when the orders
	// start at 1, or when order 0 is the Specials subgroup (One Piece
	// "TVDB Order": 0=Specials, 16=Season 16). Only a set whose first
	// subgroup is a regular season at order 0 numbers seasons order+1.
	seasonOffset := 0
	for _, group := range g.Groups {
		if group.Order != 0 {
			continue
		}
		specials := strings.EqualFold(strings.TrimSpace(group.Name), "specials")
		if !specials && len(group.Episodes) > 0 {
			specials = true
			for _, ep := range group.Episodes {
				if ep.SeasonNumber != 0 {
					specials = false
					break
				}
			}
		}
		if !specials {
			seasonOffset = 1
		}
		break
	}
	return seasonOffset
}
