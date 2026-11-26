package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type episodeShowMetadataExp struct {
	frontendtest.Experience
	shows       map[core.RatingKey]core.Title
	items       map[core.RatingKey]core.Title
	homeRows    []frontend.Row
	libraryRows []frontend.Row
	batchCalls  [][]core.RatingKey
	itemCalls   int
}

func (e *episodeShowMetadataExp) BatchTitles(_ context.Context, _ *core.Viewer, keys []core.RatingKey) ([]core.Title, error) {
	e.batchCalls = append(e.batchCalls, append([]core.RatingKey(nil), keys...))
	out := make([]core.Title, 0, len(keys))
	for _, k := range keys {
		if sh, ok := e.shows[k]; ok {
			out = append(out, sh)
		}
	}
	return out, nil
}

func (e *episodeShowMetadataExp) Views(_ context.Context, _ *core.Viewer) ([]frontend.View, error) {
	return []frontend.View{{ID: "2", Name: "TV Shows", Type: frontend.TypeShow}}, nil
}

func (e *episodeShowMetadataExp) Rows(_ context.Context, req frontend.RowRequest) ([]frontend.Row, error) {
	if req.Placement == frontend.PlacementLibrary {
		return e.libraryRows, nil
	}
	return e.homeRows, nil
}

func (e *episodeShowMetadataExp) Item(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
	e.itemCalls++
	if t, ok := e.items[req.Key]; ok {
		return frontend.ItemDetail{Title: t}, nil
	}
	return frontend.ItemDetail{}, core.ErrNotFound
}

func TestVelaEpisodeHomeAndRowPayloadsCarryShowNameAndShowID(t *testing.T) {
	showA := core.Title{Key: core.ShowKey(101), Type: core.TypeShow, Title: "Severance"}
	showB := core.Title{Key: core.ShowKey(202), Type: core.TypeShow, Title: "Andor"}
	showC := core.Title{Key: core.ShowKey(303), Type: core.TypeShow, Title: "Slow Horses"}
	showD := core.Title{Key: core.ShowKey(404), Type: core.TypeShow, Title: "Silo"}

	// Episode 1 (Continue Watching): already carries ShowKey and ShowTitle.
	epContinue := core.Title{
		Key:       core.EpisodeKey(101, 1, 3),
		Type:      core.TypeEpisode,
		Title:     "In Perpetuity",
		ShowKey:   showA.Key,
		ShowTitle: showA.Title,
		Season:    1,
		Episode:   3,
		Runtime:   55 * time.Minute,
	}
	// Episode 2 (Next Up): its provider-only key cannot encode the parent.
	// Resolve its missing show name in the single batched parent lookup.
	allocated, _ := core.AllocatedKey(core.KindEpisode, 1)
	epNextUp := core.Title{
		Key:     allocated,
		ShowKey: showB.Key,
		Type:    core.TypeEpisode,
		Title:   "The Eye",
		Season:  1,
		Episode: 6,
		Runtime: 52 * time.Minute,
	}
	// Episode 3 (non-continue/nextUp row purpose): ShowKey set, ShowTitle empty.
	epRecent := core.Title{
		Key:     core.EpisodeKey(303, 2, 1),
		Type:    core.TypeEpisode,
		Title:   "Last Stop",
		ShowKey: showC.Key,
		Season:  2,
		Episode: 1,
		Runtime: 46 * time.Minute,
	}
	// Episode 4 (recommendation row): ShowTitle empty, but showD is already a
	// card on the same Home page so it should reuse showD without querying it.
	epSamePage := core.Title{
		Key:     core.EpisodeKey(404, 1, 2),
		Type:    core.TypeEpisode,
		Title:   "Holston's Pick",
		ShowKey: showD.Key,
		Season:  1,
		Episode: 2,
		Runtime: 48 * time.Minute,
	}
	// Another episode from the same show supplies the name without a lookup.
	epSibling := epContinue
	epSibling.Key = core.EpisodeKey(101, 1, 4)
	epSibling.ShowTitle = ""
	epSibling.Episode = 4
	// Non-video track in a Home row must never leak showId/showName.
	musicTrack := core.Title{
		Key:        core.PluginKey(9001),
		Type:       core.TypeEpisode,
		Title:      "So What",
		ShowKey:    core.PluginKey(9000),
		ShowTitle:  "Miles Davis",
		MediaClass: core.MediaClassMusic,
		MediaRole:  core.MediaRoleTrack,
		Runtime:    9 * time.Minute,
	}

	exp := &episodeShowMetadataExp{
		shows: map[core.RatingKey]core.Title{
			showA.Key: showA,
			showB.Key: showB,
			showC.Key: showC,
			showD.Key: showD,
		},
		items: map[core.RatingKey]core.Title{
			epContinue.Key: epContinue,
			epNextUp.Key:   epNextUp,
			epRecent.Key:   epRecent,
		},
		homeRows: []frontend.Row{
			{
				ID:      "home.continue",
				Title:   "Continue Watching",
				Purpose: frontend.RowContinue,
				Entries: []frontend.RowEntry{{Kind: frontend.RowEntryTitle, Title: &epContinue, Handle: "r-cont"}},
			},
			{
				ID:      "home.nextup",
				Title:   "Next Up",
				Purpose: frontend.RowNextUp,
				Entries: []frontend.RowEntry{{Kind: frontend.RowEntryTitle, Title: &epNextUp, Handle: "r-next"}},
			},
			{
				ID:      "home.recent",
				Title:   "Recently Added Episodes",
				Purpose: frontend.RowRecentlyAdded,
				Entries: []frontend.RowEntry{
					{Kind: frontend.RowEntryTitle, Title: &epRecent, Handle: "r-rec"},
					{Kind: frontend.RowEntryTitle, Title: &epSibling, Handle: "r-sibling"},
					{Kind: frontend.RowEntryTitle, Title: &musicTrack, Handle: "r-track"},
				},
			},
			{
				ID:      "home.picks",
				Title:   "Recommended",
				Purpose: frontend.RowRecommendation,
				Entries: []frontend.RowEntry{
					{Kind: frontend.RowEntryTitle, Title: &showD, Handle: "r-showd"},
					{Kind: frontend.RowEntryTitle, Title: &epSamePage, Handle: "r-epd"},
				},
			},
		},
		libraryRows: []frontend.Row{
			{
				ID:      "lib.recent",
				Title:   "New Episodes",
				Purpose: frontend.RowRecentlyAdded,
				Entries: []frontend.RowEntry{{Kind: frontend.RowEntryTitle, Title: &epRecent, Handle: "r-lib"}},
			},
		},
	}

	seal, err := encryption(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	viewer := &core.Viewer{ID: 7, Username: "alice", IsOwner: true}
	inst := &instance{
		ctx:     context.Background(),
		seal:    seal,
		replays: make(map[string]*replay),
		rowMeta: map[string]frontend.RowView{},
		mux:     http.NewServeMux(),
		host: frontend.Host{
			Identity: frontendtest.Identity{
				ResolveDeviceFunc: func(_ context.Context, token string, _ core.Device) (*core.Viewer, time.Time, error) {
					if token != "tok" {
						return nil, time.Time{}, core.ErrUnauthorized
					}
					return viewer, time.Now().Add(time.Hour), nil
				},
			},
			Experience:     exp,
			ServerIdentity: frontend.ServerIdentity{MachineID: "srv-1", FriendlyName: "Ether", Version: "0.3.21"},
		},
	}
	inst.routes()

	doGet := func(path string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer tok")
		req.Header.Set("X-Vela-Device-Id", "appletv")
		rr := httptest.NewRecorder()
		inst.serve(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, rr.Code, rr.Body.String())
		}
		var doc map[string]any
		if err := json.NewDecoder(rr.Body).Decode(&doc); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return doc
	}

	assertEpisode := func(contextLabel string, title map[string]any, wantShowKey core.RatingKey, wantShowName string, wantSeason, wantEp int) {
		t.Helper()
		wantShowID := ids.RatingKeyGUID(wantShowKey)
		if title["kind"] != "episode" {
			t.Fatalf("%s: kind=%v, want episode", contextLabel, title["kind"])
		}
		if title["showId"] != wantShowID {
			t.Fatalf("%s: showId=%v, want %q", contextLabel, title["showId"], wantShowID)
		}
		if title["showName"] != wantShowName {
			t.Fatalf("%s: showName=%v, want %q", contextLabel, title["showName"], wantShowName)
		}
		if title["seasonNumber"] != float64(wantSeason) || title["episodeNumber"] != float64(wantEp) {
			t.Fatalf("%s: S%vE%v, want S%dE%d", contextLabel, title["seasonNumber"], title["episodeNumber"], wantSeason, wantEp)
		}
	}

	// 1. GET /vela/v0/bootstrap: all episodes across all row purposes carry
	// showId + showName, resolved with ONE batched BatchTitles call (only
	// showB and showC; showA already had ShowTitle and showD was on the page),
	// and ZERO per-item Experience.Item calls.
	boot := doGet("/vela/v0/bootstrap")
	if exp.itemCalls != 0 {
		t.Fatalf("bootstrap triggered %d per-item Item calls, want 0", exp.itemCalls)
	}
	if len(exp.batchCalls) != 1 {
		t.Fatalf("bootstrap BatchTitles calls = %d (%v), want 1", len(exp.batchCalls), exp.batchCalls)
	}
	if !slices.Equal(exp.batchCalls[0], []core.RatingKey{showB.Key, showC.Key}) {
		t.Fatalf("bootstrap BatchTitles keys = %v, want [%v %v]", exp.batchCalls[0], showB.Key, showC.Key)
	}
	byRowID := map[string][]map[string]any{}
	for _, rawRow := range boot["rows"].([]any) {
		row := rawRow.(map[string]any)
		var titles []map[string]any
		for _, rawItem := range row["items"].([]any) {
			titles = append(titles, rawItem.(map[string]any)["title"].(map[string]any))
		}
		byRowID[row["id"].(string)] = titles
	}
	assertEpisode("bootstrap continue", byRowID["home.continue"][0], showA.Key, "Severance", 1, 3)
	assertEpisode("bootstrap nextUp", byRowID["home.nextup"][0], showB.Key, "Andor", 1, 6)
	assertEpisode("bootstrap recentlyAdded", byRowID["home.recent"][0], showC.Key, "Slow Horses", 2, 1)
	assertEpisode("bootstrap recommendation", byRowID["home.picks"][1], showD.Key, "Silo", 1, 2)
	assertEpisode("bootstrap sibling", byRowID["home.recent"][1], showA.Key, "Severance", 1, 4)
	trackWire := byRowID["home.recent"][2]
	for _, forbidden := range []string{"showId", "showName", "seasonNumber", "episodeNumber"} {
		if _, has := trackWire[forbidden]; has {
			t.Fatalf("bootstrap music track leaked video field %q: %v", forbidden, trackWire)
		}
	}

	// 2. GET /vela/v0/rows and /vela/v0/rows/{id} carry the exact same fields.
	exp.batchCalls = nil
	rowsDoc := doGet("/vela/v0/rows")
	if len(exp.batchCalls) != 1 {
		t.Fatalf("/rows BatchTitles calls = %d, want 1", len(exp.batchCalls))
	}
	for _, rawRow := range rowsDoc["items"].([]any) {
		row := rawRow.(map[string]any)
		if row["id"] == "home.nextup" {
			ep := row["items"].([]any)[0].(map[string]any)["title"].(map[string]any)
			assertEpisode("/rows nextUp", ep, showB.Key, "Andor", 1, 6)
		}
	}

	exp.batchCalls = nil
	singleRow := doGet("/vela/v0/rows/home.recent")
	if len(exp.batchCalls) != 1 {
		t.Fatalf("/rows/{id} BatchTitles calls = %d, want 1", len(exp.batchCalls))
	}
	singleEp := singleRow["items"].([]any)[0].(map[string]any)["title"].(map[string]any)
	assertEpisode("/rows/{id}", singleEp, showC.Key, "Slow Horses", 2, 1)

	// 3. GET /vela/v0/libraries/{id}/rows carries showId + showName.
	exp.batchCalls = nil
	libRows := doGet("/vela/v0/libraries/2/rows")
	if len(exp.batchCalls) != 1 {
		t.Fatalf("/libraries/{id}/rows BatchTitles calls = %d, want 1", len(exp.batchCalls))
	}
	libEp := libRows["items"].([]any)[0].(map[string]any)["items"].([]any)[0].(map[string]any)["title"].(map[string]any)
	assertEpisode("/libraries/{id}/rows", libEp, showC.Key, "Slow Horses", 2, 1)

	// 4. Detail read matches row payload showId and showName identically.
	detail := doGet("/vela/v0/titles/" + ids.RatingKeyGUID(epNextUp.Key))
	detailTitle := detail["title"].(map[string]any)
	assertEpisode("detail read", detailTitle, showB.Key, "Andor", 1, 6)
}
