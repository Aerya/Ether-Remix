package etherplugin

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Live event state values (A2 live event contract).
const (
	LiveEventStatePre       = "pre"
	LiveEventStateIn        = "in"
	LiveEventStatePost      = "post"
	LiveEventStateDelayed   = "delayed"
	LiveEventStatePostponed = "postponed"
	LiveEventStateCanceled  = "canceled"
)

// LiveEventLeague identifies the competition or circuit an event belongs to.
type LiveEventLeague struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Abbr string `json:"abbr,omitempty"`
	Logo string `json:"logo,omitempty"`
}

// LiveEventStatus describes the current lifecycle and clock state of a live event.
type LiveEventStatus struct {
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
	Period int    `json:"period,omitempty"`
	Clock  string `json:"clock,omitempty"`
	Close  bool   `json:"close,omitempty"`
}

// LiveEventCompetitor describes one participant in a head-to-head or multi-side event.
type LiveEventCompetitor struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Short    string `json:"short,omitempty"`
	Abbr     string `json:"abbr,omitempty"`
	Logo     string `json:"logo,omitempty"`
	Color    string `json:"color,omitempty"`
	AltColor string `json:"altColor,omitempty"`
	Score    string `json:"score,omitempty"`
	Record   string `json:"record,omitempty"`
	Rank     int    `json:"rank,omitempty"`
	Home     bool   `json:"home,omitempty"`
	Winner   bool   `json:"winner,omitempty"`
	EntityID string `json:"entityId,omitempty"`
}

// LiveEventRankRow describes one leaderboard row for N-competitor events.
type LiveEventRankRow struct {
	Pos       int    `json:"pos"`
	Name      string `json:"name"`
	Abbr      string `json:"abbr,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Secondary string `json:"secondary,omitempty"`
	EntityID  string `json:"entityId,omitempty"`
}

// LiveEventProbability holds an optional live outcome probability (0..1).
type LiveEventProbability struct {
	Label        string  `json:"label,omitempty"`
	Value        float64 `json:"value"`
	CompetitorID string  `json:"competitorId,omitempty"`
}

// LiveEventBroadcast describes one network, stream, or radio carriage for an event.
type LiveEventBroadcast struct {
	Name   string `json:"name"`
	Kind   string `json:"kind,omitempty"` // tv|stream|radio
	Region string `json:"region,omitempty"`
}

// LiveEvent watch target kind values (A5).
const (
	LiveEventWatchChannel = "channel"
	LiveEventWatchAddon   = "addon"
	LiveEventWatchPlugin  = "plugin"
)

// LiveEventMatchHints supplies terms and broadcast callsigns for matching
// events against Live TV guide programmes and channels.
type LiveEventMatchHints struct {
	Terms     []string `json:"terms,omitempty"`
	Callsigns []string `json:"callsigns,omitempty"`
}

// LiveEventWatchTarget links a live event to a playable channel, add-on, or
// plugin-provided stream target. Ether attaches this; plugins do not emit it.
// Available is omitted when playable and set to false when the only matching
// channels are currently unhealthy.
type LiveEventWatchTarget struct {
	Kind       string  `json:"kind"` // channel|addon|plugin
	ChannelID  string  `json:"channelId,omitempty"`
	Name       string  `json:"name"`
	Number     string  `json:"number,omitempty"`
	Logo       string  `json:"logo,omitempty"`
	Live       bool    `json:"live"`
	Confidence float64 `json:"confidence,omitempty"`
	URL        string  `json:"url,omitempty"`
	Available  *bool   `json:"available,omitempty"`
}

// LiveEvent is the generic live event payload returned by a plugin live feed.
type LiveEvent struct {
	ID          string                 `json:"id"`
	Feed        string                 `json:"feed"`
	Category    string                 `json:"category,omitempty"`
	League      LiveEventLeague        `json:"league"`
	Title       string                 `json:"title,omitempty"`
	Start       string                 `json:"start"`
	Status      LiveEventStatus        `json:"status"`
	Competitors []LiveEventCompetitor  `json:"competitors"`
	Ranking     []LiveEventRankRow     `json:"ranking,omitempty"`
	Situation   map[string]string      `json:"situation,omitempty"`
	Probability *LiveEventProbability  `json:"probability,omitempty"`
	Venue       string                 `json:"venue,omitempty"`
	Broadcasts  []LiveEventBroadcast   `json:"broadcasts,omitempty"`
	MatchHints  *LiveEventMatchHints   `json:"matchHints,omitempty"`
	Watch       []LiveEventWatchTarget `json:"watch,omitempty"`
	Spoiler     bool                   `json:"spoiler,omitempty"`
	UpdatedAt   string                 `json:"updatedAt,omitempty"`
	// Prominence ranks featured lists: how big the event is, 0..1.
	Prominence float64 `json:"prominence,omitempty"`
}

// LiveEventTable is a generic table for box scores, line scores, or standings.
type LiveEventTable struct {
	Title   string     `json:"title"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
	// RowEntityIDs names the entity each row is about, in row order.
	RowEntityIDs []string `json:"rowEntityIds,omitempty"`
}

// LiveEventPlay is one entry in an event's play-by-play or race timeline.
type LiveEventPlay struct {
	Clock        string `json:"clock,omitempty"`
	Period       int    `json:"period,omitempty"`
	Text         string `json:"text"`
	Emphasis     string `json:"emphasis,omitempty"` // score|key|normal
	CompetitorID string `json:"competitorId,omitempty"`
	// At is the play's wall-clock time (RFC3339).
	At string `json:"at,omitempty"`
	// Score is every competitor's score after the play, by competitor id.
	Score map[string]string `json:"score,omitempty"`
}

// LiveEventProbabilityPoint is one sample along an event's probability series.
// T is either a 0..1 game fraction (float64, preferred when known) or an
// RFC3339 timestamp string.
type LiveEventProbabilityPoint struct {
	T     any     `json:"t"`
	Value float64 `json:"value"`
}

// LiveEventLink is an external link attached to an event detail view.
type LiveEventLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// LiveEventDetail extends LiveEvent with tables, plays, probability series, and links.
type LiveEventDetail struct {
	LiveEvent
	Tables            []LiveEventTable            `json:"tables,omitempty"`
	Plays             []LiveEventPlay             `json:"plays,omitempty"`
	ProbabilitySeries []LiveEventProbabilityPoint `json:"probabilitySeries,omitempty"`
	Links             []LiveEventLink             `json:"links,omitempty"`
}

// LiveEventsResponse is returned by GET /ether/v1/live/{feed}/events.
type LiveEventsResponse struct {
	Events      []LiveEvent `json:"events"`
	NextPollSec int         `json:"nextPollSec,omitempty"`
}

// LiveEventEntity describes one followable entity returned by /entities.
type LiveEventEntity struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Abbr   string `json:"abbr,omitempty"`
	Logo   string `json:"logo,omitempty"`
	League string `json:"league,omitempty"`
	Color  string `json:"color,omitempty"`
}

// LiveEventEntitiesResponse is returned by GET /ether/v1/live/{feed}/entities.
type LiveEventEntitiesResponse struct {
	Entities []LiveEventEntity `json:"entities"`
}

// LiveEventStandingsResponse is returned by GET /ether/v1/live/{feed}/standings.
type LiveEventStandingsResponse struct {
	Tables []LiveEventTable `json:"tables"`
}

// LiveEventsRequest is passed to LiveFeedProvider.Events.
type LiveEventsRequest struct {
	Feed   string
	From   string
	To     string
	League string
	IDs    []string
}

// LiveEventDetailRequest is passed to LiveFeedProvider.EventDetail.
type LiveEventDetailRequest struct {
	Feed string
	ID   string
}

// LiveEntitiesRequest is passed to LiveEntitiesProvider.Entities.
type LiveEntitiesRequest struct {
	Feed   string
	Kind   string
	League string
	Q      string
}

// LiveStandingsRequest is passed to LiveStandingsProvider.Standings.
type LiveStandingsRequest struct {
	Feed   string
	League string
}

// LiveFeedProvider answers GET /ether/v1/live/{feed}/events and /events/{id}.
// Optionally implement LiveEntitiesProvider and LiveStandingsProvider too.
type LiveFeedProvider interface {
	Events(ctx *Context, req LiveEventsRequest) (LiveEventsResponse, error)
	EventDetail(ctx *Context, req LiveEventDetailRequest) (LiveEventDetail, error)
}

// LiveEntitiesProvider answers GET /ether/v1/live/{feed}/entities.
type LiveEntitiesProvider interface {
	Entities(ctx *Context, req LiveEntitiesRequest) (LiveEventEntitiesResponse, error)
}

// LiveStandingsProvider answers GET /ether/v1/live/{feed}/standings.
type LiveStandingsProvider interface {
	Standings(ctx *Context, req LiveStandingsRequest) (LiveEventStandingsResponse, error)
}

// OnLiveFeed registers the live events feed provider for one declared feed ID.
func (p *Plugin) OnLiveFeed(feed string, provider LiveFeedProvider) {
	p.kinds().liveFeeds[feed] = provider
}

func (p *Plugin) dispatchLiveFeed(pctx *Context, method, feed string, tail []string) RawResponse {
	if method != http.MethodGet {
		return errorJSON(http.StatusMethodNotAllowed, "method_not_allowed", "Live feed routes use GET.")
	}
	var prov LiveFeedProvider
	if p.kindHandlers != nil {
		prov = p.kindHandlers.liveFeeds[feed]
	}
	if prov == nil {
		return errorJSON(http.StatusNotFound, "not_found", fmt.Sprintf("No live feed provider is registered for feed %q.", feed))
	}
	q := pctx.Query
	switch {
	case len(tail) == 1 && tail[0] == "events":
		var ids []string
		if raw := strings.TrimSpace(q["ids"]); raw != "" {
			for _, part := range strings.Split(raw, ",") {
				if id := strings.TrimSpace(part); id != "" {
					ids = append(ids, id)
				}
			}
		}
		res, err := prov.Events(pctx, LiveEventsRequest{
			Feed:   feed,
			From:   q["from"],
			To:     q["to"],
			League: q["league"],
			IDs:    ids,
		})
		if err != nil {
			return toRouteErrorResponse(err)
		}
		if res.Events == nil {
			res.Events = []LiveEvent{}
		}
		for i := range res.Events {
			if res.Events[i].Competitors == nil {
				res.Events[i].Competitors = []LiveEventCompetitor{}
			}
		}
		return okJSON(res, http.StatusOK)

	case len(tail) == 2 && tail[0] == "events" && tail[1] != "":
		id, _ := url.PathUnescape(tail[1])
		res, err := prov.EventDetail(pctx, LiveEventDetailRequest{Feed: feed, ID: id})
		if err != nil {
			return toRouteErrorResponse(err)
		}
		if res.Competitors == nil {
			res.Competitors = []LiveEventCompetitor{}
		}
		for i := range res.Tables {
			if res.Tables[i].Columns == nil {
				res.Tables[i].Columns = []string{}
			}
			if res.Tables[i].Rows == nil {
				res.Tables[i].Rows = [][]string{}
			}
		}
		return okJSON(res, http.StatusOK)

	case len(tail) == 1 && tail[0] == "entities":
		ep, ok := prov.(LiveEntitiesProvider)
		if !ok {
			return errorJSON(http.StatusNotImplemented, "not_implemented", "This live feed does not list entities.")
		}
		res, err := ep.Entities(pctx, LiveEntitiesRequest{
			Feed:   feed,
			Kind:   q["kind"],
			League: q["league"],
			Q:      q["q"],
		})
		if err != nil {
			return toRouteErrorResponse(err)
		}
		if res.Entities == nil {
			res.Entities = []LiveEventEntity{}
		}
		return okJSON(res, http.StatusOK)

	case len(tail) == 1 && tail[0] == "standings":
		sp, ok := prov.(LiveStandingsProvider)
		if !ok {
			return errorJSON(http.StatusNotImplemented, "not_implemented", "This live feed does not serve standings.")
		}
		res, err := sp.Standings(pctx, LiveStandingsRequest{
			Feed:   feed,
			League: q["league"],
		})
		if err != nil {
			return toRouteErrorResponse(err)
		}
		if res.Tables == nil {
			res.Tables = []LiveEventTable{}
		}
		for i := range res.Tables {
			if res.Tables[i].Columns == nil {
				res.Tables[i].Columns = []string{}
			}
			if res.Tables[i].Rows == nil {
				res.Tables[i].Rows = [][]string{}
			}
		}
		return okJSON(res, http.StatusOK)
	}
	return errorJSON(http.StatusNotFound, "not_found", "No live feed route matched.")
}
