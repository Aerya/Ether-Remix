package core

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestLiveEventNamespaceAndParse(t *testing.T) {
	id := NamespaceLiveEventID("community.acme.feed", "scores", "evt-42")
	if id != "community.acme.feed:scores:evt-42" {
		t.Fatalf("unexpected namespaced id: %q", id)
	}
	p, f, e, ok := ParseLiveEventID(id)
	if !ok || p != "community.acme.feed" || f != "scores" || e != "evt-42" {
		t.Fatalf("parse failed: ok=%v p=%q f=%q e=%q", ok, p, f, e)
	}
	// Plugin-scoped event IDs containing colons remain intact.
	p, f, e, ok = ParseLiveEventID("community.acme.feed:scores:league:99")
	if !ok || p != "community.acme.feed" || f != "scores" || e != "league:99" {
		t.Fatalf("parse with colon in event id failed: ok=%v p=%q f=%q e=%q", ok, p, f, e)
	}
	for _, bad := range []string{"", "a:b", ":feed:id", "plug::id", "plug:feed:", "a/b:feed:id", "plug:f/g:id"} {
		if _, _, _, ok := ParseLiveEventID(bad); ok {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

func TestLiveEntityKeyNamespaceAndParse(t *testing.T) {
	key := NamespaceLiveEntityKey("community.acme.feed", "scores", "team", "home-1")
	if key != "community.acme.feed:scores:team:home-1" {
		t.Fatalf("unexpected entity key: %q", key)
	}
	p, f, k, id, ok := ParseLiveEntityKey(key)
	if !ok || p != "community.acme.feed" || f != "scores" || k != "team" || id != "home-1" {
		t.Fatalf("parse entity key failed: ok=%v p=%q f=%q k=%q id=%q", ok, p, f, k, id)
	}
	for _, bad := range []string{"", "a:b:c", "a:b:c:", "a::c:d", "a:b::d", "a/x:b:c:d"} {
		if _, _, _, _, ok := ParseLiveEntityKey(bad); ok {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

func TestLiveEventDetailJSONAndClone(t *testing.T) {
	orig := LiveEventDetail{
		LiveEvent: LiveEvent{
			ID:       "community.acme.feed:main:101",
			Feed:     "main",
			Category: "ball",
			League:   LiveEventLeague{ID: "top", Name: "Top League", Abbr: "TL", Logo: "https://example.invalid/tl.png"},
			Title:    "Alpha vs Beta",
			Start:    "2026-11-22T18:00:00Z",
			Status:   LiveEventStatus{State: LiveEventStateIn, Detail: "P4 · 2:14", Period: 4, Clock: "2:14", Close: true},
			Competitors: []LiveEventCompetitor{
				{ID: "a", Name: "Alpha", Short: "ALP", Abbr: "ALP", Score: "24", Record: "8-3", Rank: 3, Home: false, EntityID: "team-a"},
				{ID: "b", Name: "Beta", Short: "BET", Abbr: "BET", Score: "27", Record: "9-2", Rank: 1, Home: true, Winner: false, EntityID: "team-b"},
			},
			Situation:   map[string]string{"State": "Active", "Turn": "BET"},
			Probability: &LiveEventProbability{Label: "Win", Value: 0.64, CompetitorID: "b"},
			Venue:       "Central Arena",
			Broadcasts:  []LiveEventBroadcast{{Name: "Net 1", Kind: LiveEventBroadcastTV, Region: "US"}},
			MatchHints:  &LiveEventMatchHints{Terms: []string{"Alpha", "Beta", "ALP", "BET"}, Callsigns: []string{"Net 1"}},
			UpdatedAt:   "2026-11-22T20:15:00Z",
		},
		Tables: []LiveEventTable{
			{Title: "Linescore", Columns: []string{"Side", "P1", "T"}, Rows: [][]string{{"ALP", "7", "24"}, {"BET", "10", "27"}}},
		},
		Plays: []LiveEventPlay{
			{Clock: "2:14", Period: 4, Text: "Goal scored", Emphasis: LiveEventPlayEmphasisScore, CompetitorID: "b"},
		},
		ProbabilitySeries: []LiveEventProbabilityPoint{
			{T: ProbFraction(0.25), Value: 0.5},
			{T: ProbTimestamp("2026-11-22T20:15:00Z"), Value: 0.64},
		},
		Links: []LiveEventLink{{Label: "Summary", URL: "https://example.invalid/101"}},
	}

	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var decoded LiveEventDetail
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, orig) {
		t.Fatalf("round-trip mismatch:\ngot:  %+v\nwant: %+v", decoded, orig)
	}

	cloned := orig.Clone()
	cloned.Competitors[0].Score = "99"
	cloned.Situation["Turn"] = "ALP"
	cloned.Probability.Value = 0.01
	cloned.MatchHints.Terms[0] = "Mutated"
	cloned.Tables[0].Rows[0][2] = "99"
	if orig.Competitors[0].Score != "24" || orig.Situation["Turn"] != "BET" || orig.Probability.Value != 0.64 || orig.MatchHints.Terms[0] != "Alpha" || orig.Tables[0].Rows[0][2] != "24" {
		t.Fatal("Clone aliased mutable fields")
	}

	// Numeric 0..1 game fraction preserves JSON number type on round-trip.
	var pt LiveEventProbabilityPoint
	if err := json.Unmarshal([]byte(`{"t":0.75,"value":0.62}`), &pt); err != nil {
		t.Fatal(err)
	}
	if pt.T.Fraction == nil || *pt.T.Fraction != 0.75 || pt.T.Time != "" || pt.Value != 0.62 {
		t.Fatalf("numeric t unmarshal: got %+v", pt)
	}
	ptRaw, err := json.Marshal(pt)
	if err != nil {
		t.Fatal(err)
	}
	if string(ptRaw) != `{"t":0.75,"value":0.62}` {
		t.Fatalf("numeric t marshal: got %s, want {\"t\":0.75,\"value\":0.62}", ptRaw)
	}

	// RFC3339 timestamp preserves JSON string type on round-trip.
	var tsPt LiveEventProbabilityPoint
	if err := json.Unmarshal([]byte(`{"t":"2026-11-22T20:15:00Z","value":0.64}`), &tsPt); err != nil {
		t.Fatal(err)
	}
	if tsPt.T.Fraction != nil || tsPt.T.Time != "2026-11-22T20:15:00Z" {
		t.Fatalf("timestamp t unmarshal: got %+v", tsPt)
	}
	tsRaw, err := json.Marshal(tsPt)
	if err != nil {
		t.Fatal(err)
	}
	if string(tsRaw) != `{"t":"2026-11-22T20:15:00Z","value":0.64}` {
		t.Fatalf("timestamp t marshal: got %s", tsRaw)
	}

	// LiveEventEntity encodes key and explicit favorite bool.
	entRaw, err := json.Marshal(LiveEventEntity{
		ID:       "t1",
		Kind:     "team",
		Name:     "Alpha",
		Key:      NamespaceLiveEntityKey("community.acme.feed", "scores", "team", "t1"),
		Favorite: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	var entMap map[string]any
	if err := json.Unmarshal(entRaw, &entMap); err != nil {
		t.Fatal(err)
	}
	if entMap["key"] != "community.acme.feed:scores:team:t1" {
		t.Fatalf("entity key = %v", entMap["key"])
	}
	if fav, ok := entMap["favorite"].(bool); !ok || fav {
		t.Fatalf("entity favorite = %#v (ok=%v), want explicit false", entMap["favorite"], ok)
	}
}

func TestLiveEventSpecArraysAndRankingSecondary(t *testing.T) {
	// Ranking event with nil Competitors must still serialize "competitors":[] (never omitted or null)
	// and preserve ranking[].secondary.
	ev := LiveEvent{
		ID:     "p:f:golf-1",
		Feed:   "f",
		League: LiveEventLeague{ID: "pga", Name: "PGA Tour", Abbr: "PGA"},
		Start:  "2026-11-22T12:00:00Z",
		Status: LiveEventStatus{State: LiveEventStateIn},
		Ranking: []LiveEventRankRow{
			{Pos: 1, Name: "Player A", Abbr: "PLA", Detail: "-10", Secondary: "thru 14"},
			{Pos: 2, Name: "Player B", Abbr: "PLB", Detail: "-9", Secondary: "F"},
			{Pos: 2, Name: "Player C", Abbr: "PLC", Detail: "-9", Secondary: "thru 12"},
		},
	}

	evRaw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal LiveEvent: %v", err)
	}
	var evMap map[string]any
	if err := json.Unmarshal(evRaw, &evMap); err != nil {
		t.Fatalf("unmarshal LiveEvent: %v", err)
	}
	comps, present := evMap["competitors"]
	if !present || comps == nil {
		t.Fatalf("LiveEvent missing or null competitors: %s", evRaw)
	}
	if arr, ok := comps.([]any); !ok || len(arr) != 0 {
		t.Fatalf("LiveEvent competitors = %#v, want []", comps)
	}
	rows := evMap["ranking"].([]any)
	if rows[0].(map[string]any)["secondary"] != "thru 14" {
		t.Fatalf("ranking[0].secondary = %#v, want 'thru 14'", rows[0])
	}

	// LiveEventDetail preserves both embedded LiveEvent fields and empty competitors[].
	detailRaw, err := json.Marshal(LiveEventDetail{
		LiveEvent: ev,
		Tables:    []LiveEventTable{{Title: "Leaderboard"}},
	})
	if err != nil {
		t.Fatalf("marshal LiveEventDetail: %v", err)
	}
	var detailMap map[string]any
	if err := json.Unmarshal(detailRaw, &detailMap); err != nil {
		t.Fatalf("unmarshal LiveEventDetail: %v", err)
	}
	if arr, ok := detailMap["competitors"].([]any); !ok || len(arr) != 0 {
		t.Fatalf("LiveEventDetail competitors = %#v, want []", detailMap["competitors"])
	}
	tbl := detailMap["tables"].([]any)[0].(map[string]any)
	if _, ok := tbl["columns"].([]any); !ok {
		t.Fatalf("LiveEventTable.columns = %#v, want []", tbl["columns"])
	}
	if _, ok := tbl["rows"].([]any); !ok {
		t.Fatalf("LiveEventTable.rows = %#v, want []", tbl["rows"])
	}

	// LiveTickerItem preserves empty competitors[].
	tickerRaw, err := json.Marshal(TickerItemFromEvent(ev))
	if err != nil {
		t.Fatalf("marshal LiveTickerItem: %v", err)
	}
	var tickerMap map[string]any
	if err := json.Unmarshal(tickerRaw, &tickerMap); err != nil {
		t.Fatalf("unmarshal LiveTickerItem: %v", err)
	}
	if arr, ok := tickerMap["competitors"].([]any); !ok || len(arr) != 0 {
		t.Fatalf("LiveTickerItem competitors = %#v, want []", tickerMap["competitors"])
	}

	// Response wrappers never emit null arrays.
	for name, payload := range map[string]any{
		"events":    LiveEventsResponse{},
		"entities":  LiveEventEntitiesResponse{},
		"standings": LiveEventStandingsResponse{},
	} {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if string(b) == "" || reflect.DeepEqual(string(b), `{"events":null}`) {
			t.Fatalf("%s encoded null: %s", name, b)
		}
	}
}

func TestLiveEventWatchTargetAvailabilityWireAndClone(t *testing.T) {
	healthy := LiveEventWatchTarget{
		Kind:       LiveEventWatchChannel,
		ChannelID:  "ch-1",
		Name:       "ESPN",
		Live:       true,
		Confidence: 0.9,
	}
	if !healthy.IsAvailable() {
		t.Fatal("healthy target IsAvailable() = false, want true")
	}
	rawHealthy, err := json.Marshal(healthy)
	if err != nil {
		t.Fatalf("marshal healthy target: %v", err)
	}
	var mHealthy map[string]any
	if err := json.Unmarshal(rawHealthy, &mHealthy); err != nil {
		t.Fatalf("unmarshal healthy target: %v", err)
	}
	if _, present := mHealthy["available"]; present {
		t.Fatalf("healthy watch target must omit available on wire, got %s", rawHealthy)
	}

	availFalse := false
	unavail := LiveEventWatchTarget{
		Kind:       LiveEventWatchChannel,
		ChannelID:  "ch-2",
		Name:       "ESPN 2",
		Live:       true,
		Confidence: 0.85,
		Available:  &availFalse,
	}
	if unavail.IsAvailable() {
		t.Fatal("unavailable target IsAvailable() = true, want false")
	}
	rawUnavail, err := json.Marshal(unavail)
	if err != nil {
		t.Fatalf("marshal unavailable target: %v", err)
	}
	var mUnavail map[string]any
	if err := json.Unmarshal(rawUnavail, &mUnavail); err != nil {
		t.Fatalf("unmarshal unavailable target: %v", err)
	}
	if av, ok := mUnavail["available"].(bool); !ok || av {
		t.Fatalf("unavailable watch target available = %#v, want false (%s)", mUnavail["available"], rawUnavail)
	}

	orig := LiveEvent{
		ID:    "p:f:e1",
		Feed:  "f",
		Watch: []LiveEventWatchTarget{unavail},
	}
	cloned := orig.Clone()
	if cloned.Watch[0].Available == nil || *cloned.Watch[0].Available {
		t.Fatalf("cloned watch target lost Available=false: %+v", cloned.Watch[0])
	}
	*cloned.Watch[0].Available = true
	if *orig.Watch[0].Available {
		t.Fatal("mutating cloned WatchTarget.Available mutated original")
	}
}
