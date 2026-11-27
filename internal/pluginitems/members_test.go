package pluginitems

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// A list level (a playlist) is walked as a root of its own; its page brings
// its members with their parents, and its members come back in its order.
func TestListLevelSyncStoresMembersInOrder(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	defer s.Close()
	const pluginID, kindKey, libID = "test.music", "music", core.LibraryID(92)
	decl := branchingMusicDecl()
	decl.Levels = append(decl.Levels, plugins.KindLevel{Key: "playlist", Label: "Playlist", Role: "playlist", Container: true, Members: true})
	if _, err := s.UpsertKind(ctx, pluginID, libID, decl, false); err != nil {
		t.Fatal(err)
	}
	members := []string{"trk-2", "trk-missing", "trk-1"}
	caller := callerFunc(func(_ context.Context, _ string, req plugins.Request) (plugins.Response, error) {
		if req.Path != "/ether/v1/kinds/music/browse" {
			return plugins.Response{Status: http.StatusNotFound}, nil
		}
		var br core.KindBrowseRequest
		_ = json.Unmarshal(req.Body, &br)
		var items []core.KindItem
		switch {
		case br.Level == "artist":
			items = []core.KindItem{{ID: "art-1", Level: "artist", Title: "Seed"}}
		case br.Level == "playlist":
			items = []core.KindItem{
				{ID: "art-2", Level: "artist", Title: "Guest"},
				{ID: "alb-9", Level: "album", ParentID: "art-2", Title: "Guest Album"},
				{ID: "trk-1", Level: "track", ParentID: "alb-9", Title: "First", Playable: true},
				{ID: "trk-2", Level: "track", ParentID: "alb-9", Title: "Second", Playable: true},
				{ID: "pl-1", Level: "playlist", Title: "Top Worldwide", Members: members},
			}
		}
		body, _ := json.Marshal(core.KindPage{Items: items})
		return plugins.Response{Status: http.StatusOK, Body: body}, nil
	})
	if _, err := s.SyncKind(ctx, pluginID, kindKey, SyncOptions{Caller: caller}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	ref := core.NewItemRef(pluginID, kindKey, "pl-1")
	got, total, err := s.MembersPage(ctx, ref, 0, 10)
	if err != nil || total != 3 || len(got) != 2 || got[0].ID != "trk-2" || got[1].ID != "trk-1" {
		t.Fatalf("members: %+v total=%d err=%v", got, total, err)
	}
	// The list names its members again in a new order on the next walk.
	members = []string{"trk-1", "trk-2"}
	if _, err := s.SyncKind(ctx, pluginID, kindKey, SyncOptions{Caller: caller, FullWalk: true}); err != nil {
		t.Fatal(err)
	}
	if got, total, _ = s.MembersPage(ctx, ref, 0, 10); total != 2 || got[0].ID != "trk-1" {
		t.Fatalf("reordered members: %+v %d", got, total)
	}
	// A list item cannot name a parent, and only a list level carries members.
	if _, err := ValidateItem(decl, core.KindItem{ID: "pl-2", Level: "playlist", ParentID: "art-1", Title: "x"}, "artist"); err == nil {
		t.Fatal("a list item with a parent was accepted")
	}
	if _, err := ValidateItem(decl, core.KindItem{ID: "alb-3", Level: "album", ParentID: "art-1", Title: "x", Members: []string{"trk-1"}}, "artist"); err == nil {
		t.Fatal("members on a non-list level were accepted")
	}
}

// A recording identity is stored with the item, read back with it, carried
// onto the projected title, and a change to it alone is a change.
func TestRecordingStoredAndProjected(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	defer s.Close()
	const pluginID, kindKey, libID = "test.music", "music", core.LibraryID(93)
	decl := branchingMusicDecl()
	if _, err := s.UpsertKind(ctx, pluginID, libID, decl, false); err != nil {
		t.Fatal(err)
	}
	recording := "isrc:GBDUW0000053"
	caller := callerFunc(func(_ context.Context, _ string, req plugins.Request) (plugins.Response, error) {
		var br core.KindBrowseRequest
		_ = json.Unmarshal(req.Body, &br)
		var items []core.KindItem
		if br.Level == "artist" {
			items = []core.KindItem{
				{ID: "art-1", Level: "artist", Title: "Daft Punk"},
				{ID: "alb-1", Level: "album", ParentID: "art-1", Title: "Discovery"},
				{ID: "trk-1", Level: "track", ParentID: "alb-1", Title: "One More Time", Playable: true, Recording: recording},
				{ID: "alb-2", Level: "album", ParentID: "art-1", Title: "Alive 2007"},
				{ID: "trk-2", Level: "track", ParentID: "alb-2", Title: "One More Time (Live)", Playable: true, Recording: "isrc:GBDUW0000053"},
			}
		}
		body, _ := json.Marshal(core.KindPage{Items: items})
		return plugins.Response{Status: http.StatusOK, Body: body}, nil
	})
	read := func() core.KindItem {
		t.Helper()
		ref := core.NewItemRef(pluginID, kindKey, "trk-1")
		it, _, err := s.GetByRef(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	if _, err := s.SyncKind(ctx, pluginID, kindKey, SyncOptions{Caller: caller}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Recording != recording || Project(got, decl, core.Presentations{}).RecordingID != recording {
		t.Fatalf("recording = %q", got.Recording)
	}
	// Two copies of one recording make a duplicate group.
	groups, total, err := s.Duplicates(ctx, libID, 0, 10)
	if err != nil || total != 1 || len(groups) != 1 || groups[0].Count != 2 || len(groups[0].Items) != 2 {
		t.Fatalf("duplicates: %+v total=%d err=%v", groups, total, err)
	}
	recording = "isrc:GBDUW0000059"
	if _, err := s.SyncKind(ctx, pluginID, kindKey, SyncOptions{Caller: caller, FullWalk: true}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.Recording != recording {
		t.Fatalf("changed recording not stored: %q", got.Recording)
	}
	// trk-1 is now another recording: no recording has two copies.
	if groups, total, err := s.Duplicates(ctx, libID, 0, 10); err != nil || total != 0 || len(groups) != 0 {
		t.Fatalf("after the change: %+v total=%d err=%v", groups, total, err)
	}
}

// An expansion made under an earlier declaration of the kind is no longer
// fresh: a plugin update re-expands containers as they are opened.
func TestExpansionOutdatedByNewDeclaration(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	defer s.Close()
	const pluginID, kindKey, libID = "test.music", "music", core.LibraryID(94)
	decl := branchingMusicDecl()
	if _, err := s.UpsertKind(ctx, pluginID, libID, decl, false); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkFacetExpansionRunning(ctx, pluginID, kindKey, libID, "__container__", "art-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishFacetExpansion(ctx, pluginID, kindKey, libID, "__container__", "art-1", ExpansionStateDone, 3, ""); err != nil {
		t.Fatal(err)
	}
	exp, ok, err := s.GetFacetExpansion(ctx, pluginID, kindKey, "__container__", "art-1")
	if err != nil || !ok || !exp.IsFresh(s.Now(), time.Hour) {
		t.Fatalf("fresh expansion: %+v ok=%v err=%v", exp, ok, err)
	}
	decl.Label = decl.Label + " (updated)"
	if _, err := s.UpsertKind(ctx, pluginID, libID, decl, false); err != nil {
		t.Fatal(err)
	}
	exp, _, _ = s.GetFacetExpansion(ctx, pluginID, kindKey, "__container__", "art-1")
	if !exp.Outdated || exp.IsFresh(s.Now(), time.Hour) {
		t.Fatalf("an expansion under the old declaration still counts as fresh: %+v", exp)
	}
}
