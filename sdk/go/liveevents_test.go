package etherplugin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

type fakeLiveFeed struct {
	lastEventsReq    LiveEventsRequest
	lastDetailReq    LiveEventDetailRequest
	lastEntitiesReq  LiveEntitiesRequest
	lastStandingsReq LiveStandingsRequest
}

func (f *fakeLiveFeed) Events(_ *Context, req LiveEventsRequest) (LiveEventsResponse, error) {
	f.lastEventsReq = req
	return LiveEventsResponse{
		Events: []LiveEvent{
			{
				ID:     "e1",
				Feed:   req.Feed,
				League: LiveEventLeague{ID: "l1", Name: "League One"},
				Start:  "2026-11-22T18:00:00Z",
				Status: LiveEventStatus{State: LiveEventStateIn, Detail: "P2", Period: 2, Clock: "10:00"},
			},
		},
		NextPollSec: 15,
	}, nil
}

func (f *fakeLiveFeed) EventDetail(_ *Context, req LiveEventDetailRequest) (LiveEventDetail, error) {
	f.lastDetailReq = req
	return LiveEventDetail{
		LiveEvent: LiveEvent{
			ID:     req.ID,
			Feed:   req.Feed,
			League: LiveEventLeague{ID: "l1", Name: "League One"},
			Start:  "2026-11-22T18:00:00Z",
			Status: LiveEventStatus{State: LiveEventStateIn},
		},
		Tables: []LiveEventTable{{Title: "Line", Columns: []string{"Side", "T"}, Rows: [][]string{{"A", "1"}}}},
	}, nil
}

func (f *fakeLiveFeed) Entities(_ *Context, req LiveEntitiesRequest) (LiveEventEntitiesResponse, error) {
	f.lastEntitiesReq = req
	return LiveEventEntitiesResponse{
		Entities: []LiveEventEntity{{ID: "t1", Kind: "team", Name: "Alpha", League: req.League}},
	}, nil
}

func (f *fakeLiveFeed) Standings(_ *Context, req LiveStandingsRequest) (LiveEventStandingsResponse, error) {
	f.lastStandingsReq = req
	return LiveEventStandingsResponse{
		Tables: []LiveEventTable{{Title: "Standings", Columns: []string{"Team", "W"}, Rows: [][]string{{"Alpha", "10"}}}},
	}, nil
}

func TestLiveFeedDispatch(t *testing.T) {
	p := New(Definition{
		Manifest: Manifest{
			ID: "community.acme.live", Version: "1.0.0", Name: "Live",
			Ether: &EtherManifestBlock{
				Protocol: 1, Kind: KindExtension, Capabilities: []Capability{CapLiveEvents}, Summary: "Live feed",
				Runtime:     RuntimeDecl{Kind: "remote"},
				Contributes: &Contributes{LiveFeeds: []LiveFeedMeta{{ID: "main", Title: "Main", MinIntervalSec: 15}}},
			},
		},
	})
	f := &fakeLiveFeed{}
	p.OnLiveFeed("main", f)

	// GET /events
	resp := p.Dispatch(context.Background(), RawRequest{
		Method: http.MethodGet,
		Path:   "/ether/v1/live/main/events",
		Query:  map[string]string{"from": "2026-11-22T00:00:00Z", "to": "2026-11-23T00:00:00Z", "league": "l1", "ids": "e1, e2"},
	})
	if resp.Status != http.StatusOK {
		t.Fatalf("events status=%d body=%s", resp.Status, resp.Body)
	}
	var evResp LiveEventsResponse
	if err := json.Unmarshal(resp.Body, &evResp); err != nil || len(evResp.Events) != 1 || evResp.NextPollSec != 15 {
		t.Fatalf("events body=%s err=%v", resp.Body, err)
	}
	if len(f.lastEventsReq.IDs) != 2 || f.lastEventsReq.IDs[1] != "e2" || f.lastEventsReq.League != "l1" {
		t.Fatalf("unexpected events req: %+v", f.lastEventsReq)
	}

	// GET /events/{id}
	resp = p.Dispatch(context.Background(), RawRequest{Method: http.MethodGet, Path: "/ether/v1/live/main/events/e1"})
	if resp.Status != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", resp.Status, resp.Body)
	}
	var det LiveEventDetail
	if err := json.Unmarshal(resp.Body, &det); err != nil || det.ID != "e1" || len(det.Tables) != 1 {
		t.Fatalf("detail body=%s err=%v", resp.Body, err)
	}

	// GET /entities
	resp = p.Dispatch(context.Background(), RawRequest{Method: http.MethodGet, Path: "/ether/v1/live/main/entities?kind=team&league=l1&q=alp"})
	if resp.Status != http.StatusOK || f.lastEntitiesReq.Kind != "team" || f.lastEntitiesReq.Q != "alp" {
		t.Fatalf("entities status=%d req=%+v body=%s", resp.Status, f.lastEntitiesReq, resp.Body)
	}

	// GET /standings
	resp = p.Dispatch(context.Background(), RawRequest{Method: http.MethodGet, Path: "/ether/v1/live/main/standings?league=l1"})
	if resp.Status != http.StatusOK || f.lastStandingsReq.League != "l1" {
		t.Fatalf("standings status=%d req=%+v body=%s", resp.Status, f.lastStandingsReq, resp.Body)
	}

	// Unknown feed -> 404
	resp = p.Dispatch(context.Background(), RawRequest{Method: http.MethodGet, Path: "/ether/v1/live/unknown/events"})
	if resp.Status != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown feed, got %d", resp.Status)
	}
}
