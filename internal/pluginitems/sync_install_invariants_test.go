package pluginitems

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

func musicAlbumDecl() plugins.MediaKindMeta {
	return plugins.MediaKindMeta{
		Key:   "albums",
		Label: "Music",
		Library: plugins.KindLibraryDecl{
			Name: "Music",
		},
		Sync: plugins.KindSyncDecl{
			Mode:    plugins.KindSyncPull,
			Refresh: "15m",
		},
		Levels: []plugins.KindLevel{
			{Key: "album", Label: "Album", Playable: true},
		},
		Fields: []plugins.KindField{
			{Key: "artists", Type: "people", Label: "Artists", Indexed: true},
			{Key: "genres", Type: "strings", Label: "Genres", Indexed: true},
			{Key: "record_type", Type: "enum", Label: "Type", Indexed: true, Enum: []string{"album", "ep", "single"}},
		},
		Browse: plugins.KindBrowseDecl{
			Facets: []string{"artists", "genres", "record_type"},
		},
	}
}

// TestInitialWalkPersistsPagesIncrementallyAndSurvivesInterrupt verifies that
// initial pull sync writes each fetched page immediately (bumping SyncGeneration
// and reporting live progress while still running) and leaves last_ok unset if
// interrupted so the next run resumes initial walk cleanly.
func TestInitialWalkPersistsPagesIncrementallyAndSurvivesInterrupt(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	defer s.Close()

	if _, err := s.UpsertKind(ctx, "music.plugin", 20, musicAlbumDecl(), false); err != nil {
		t.Fatal(err)
	}

	page2Entered := make(chan struct{})
	releasePage2 := make(chan struct{})
	var failPage2 atomic.Bool
	failPage2.Store(true)

	caller := callerFunc(func(callCtx context.Context, _ string, req plugins.Request) (plugins.Response, error) {
		switch req.Path {
		case "/ether/v1/kinds/albums/browse":
			var br core.KindBrowseRequest
			_ = json.Unmarshal(req.Body, &br)
			if br.Cursor == "" {
				body, _ := json.Marshal(core.KindPage{
					Items: []core.KindItem{
						{ID: "alb-1", Level: "album", Title: "Album One", Playable: true},
						{ID: "alb-2", Level: "album", Title: "Album Two", Playable: true},
					},
					NextCursor: "p2",
				})
				return plugins.Response{Status: http.StatusOK, Body: body}, nil
			}
			if failPage2.Load() {
				close(page2Entered)
				<-releasePage2
				return plugins.Response{Status: http.StatusBadGateway, Body: []byte(`{"error":"upstream failed"}`)}, nil
			}
			body, _ := json.Marshal(core.KindPage{
				Items: []core.KindItem{
					{ID: "alb-3", Level: "album", Title: "Album Three", Playable: true},
				},
			})
			return plugins.Response{Status: http.StatusOK, Body: body}, nil
		case "/ether/v1/kinds/albums/changes":
			return plugins.Response{Status: http.StatusNotFound}, nil
		default:
			return plugins.Response{Status: http.StatusNotFound}, nil
		}
	})

	genBefore := s.SyncGeneration()
	done1 := make(chan error, 1)
	go func() {
		_, err := s.SyncKind(ctx, "music.plugin", "albums", SyncOptions{Caller: caller})
		done1 <- err
	}()

	// Wait until page 2 begins; page 1 must ALREADY be persisted and visible.
	<-page2Entered
	tally, err := s.LibraryTally(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if tally["album"] != 2 {
		t.Fatalf("expected 2 items persisted mid-walk before page 2 finishes, got %v", tally)
	}
	if s.SyncGeneration() <= genBefore {
		t.Fatalf("expected SyncGeneration to bump on first flushed page: before=%d after=%d", genBefore, s.SyncGeneration())
	}
	st, err := s.SyncStateOf(ctx, "music.plugin", "albums")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.Phase != "syncing" || st.SyncedItems != 2 || !st.LastOK.IsZero() {
		t.Fatalf("unexpected mid-sync state: %+v", st)
	}

	// Let page 2 fail; page 1 items must remain stored and LastOK must stay zero.
	close(releasePage2)
	if err := <-done1; err == nil {
		t.Fatal("expected error from failed page 2")
	}
	stAfterFail, err := s.SyncStateOf(ctx, "music.plugin", "albums")
	if err != nil {
		t.Fatal(err)
	}
	if stAfterFail.Running || !stAfterFail.LastOK.IsZero() || stAfterFail.Items != 2 {
		t.Fatalf("expected running=false, lastOk=zero, items=2 after interrupted walk, got %+v", stAfterFail)
	}

	// Subsequent retry succeeds and completes cleanly with all 3 items and LastOK set.
	failPage2.Store(false)
	if _, err := s.SyncKind(ctx, "music.plugin", "albums", SyncOptions{Caller: caller}); err != nil {
		t.Fatalf("retry SyncKind: %v", err)
	}
	stOK, err := s.SyncStateOf(ctx, "music.plugin", "albums")
	if err != nil {
		t.Fatal(err)
	}
	if stOK.Running || stOK.LastOK.IsZero() || stOK.Items != 3 {
		t.Fatalf("expected clean completed sync with 3 items, got %+v", stOK)
	}
}

// TestStartKindSyncAndCancelDoNotBlockOnInFlightPass verifies that cancelling or
// restarting a kind sync returns immediately without waiting for an in-flight
// pass to exit, while serializing the replacement pass behind the cancelled one.
func TestStartKindSyncAndCancelDoNotBlockOnInFlightPass(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	defer s.Close()

	if _, err := s.UpsertKind(ctx, "music.plugin", 21, musicAlbumDecl(), false); err != nil {
		t.Fatal(err)
	}

	pass1Started := make(chan struct{})
	pass1Cancelled := make(chan struct{})
	releasePass1 := make(chan struct{})
	pass2Started := make(chan struct{})
	var calls atomic.Int32

	s.SetCaller(callerFunc(func(callCtx context.Context, _ string, req plugins.Request) (plugins.Response, error) {
		if req.Path != "/ether/v1/kinds/albums/browse" {
			return plugins.Response{Status: http.StatusNotFound}, nil
		}
		n := calls.Add(1)
		if n == 1 {
			close(pass1Started)
			<-callCtx.Done()
			close(pass1Cancelled)
			<-releasePass1
			return plugins.Response{}, callCtx.Err()
		}
		if n == 2 {
			close(pass2Started)
		}
		body, _ := json.Marshal(core.KindPage{Items: []core.KindItem{{ID: "a1", Level: "album", Title: "Done", Playable: true}}})
		return plugins.Response{Status: http.StatusOK, Body: body}, nil
	}))

	s.StartKindSync(ctx, "music.plugin", "albums", time.Hour)
	<-pass1Started

	// CancelKindSync + StartKindSync MUST return while pass 1 is still blocked on releasePass1.
	s.CancelKindSync("music.plugin", "albums")
	<-pass1Cancelled
	s.StartKindSync(ctx, "music.plugin", "albums", time.Hour)

	// Pass 2 must NOT start until pass 1 exits.
	select {
	case <-pass2Started:
		t.Fatal("pass 2 started before superseded pass 1 exited")
	default:
	}

	close(releasePass1)
	<-pass2Started
	s.StopKindSync("music.plugin", "albums")
}

// TestUpsertKindIndexShapeChangeReindexesInBackground verifies that adding an
// indexed field to a populated kind defers reindexing to a chunked background
// worker and finishes indexing without failing.
func TestUpsertKindIndexShapeChangeReindexesInBackground(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	defer s.Close()

	decl := musicAlbumDecl()
	decl.Fields = append(decl.Fields, plugins.KindField{Key: "label", Type: "string", Label: "Label", Indexed: false})
	if _, err := s.UpsertKind(ctx, "music.plugin", 22, decl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertItems(ctx, "music.plugin", "albums", []core.KindItem{
		{
			ID:       "a1",
			Level:    "album",
			Title:    "Reindex Me",
			Playable: true,
			Fields: map[string]json.RawMessage{
				"label": rawJSON(t, "Fat Wreck"),
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Flip label from Indexed: false -> Indexed: true to trigger indexShapeChanged.
	decl2 := musicAlbumDecl()
	decl2.Fields = append(decl2.Fields, plugins.KindField{Key: "label", Type: "string", Label: "Label", Indexed: true})
	if _, err := s.UpsertKind(ctx, "music.plugin", 22, decl2, false); err != nil {
		t.Fatalf("UpsertKind upgrade: %v", err)
	}

	// Wait for the background chunked reindexer via StopKindSync (which drains reindexers).
	// Wait until fts_version reaches currentFTSVersion (bounded loop on DB state, no wall-clock assert).
	for i := 0; i < 200; i++ {
		var ftsVer int64
		if err := s.db.QueryRowContext(ctx, `SELECT fts_version FROM plugin_kinds WHERE plugin_id = ? AND kind_key = ?`, "music.plugin", "albums").Scan(&ftsVer); err == nil && ftsVer == currentFTSVersion {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Querying by the newly indexed field must find the existing item.
	page, err := s.Browse(ctx, BrowseQuery{
		LibraryID: 22,
		Level:     "album",
		Facets:    map[string][]string{"label": {"Fat Wreck"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "a1" {
		t.Fatalf("expected newly indexed field to match after background reindex, got %+v", page.Items)
	}
}

// TestExtractGenresExcludesPeopleAndNonGenreFacets proves that when a kind
// declares an explicit genres field alongside people/enum browse facets (such
// as artists and record_type), Title.Genres contains only the real genres.
func TestExtractGenresExcludesPeopleAndNonGenreFacets(t *testing.T) {
	decl := musicAlbumDecl()
	item := core.KindItem{
		ID:       "alb-1",
		Level:    "album",
		Title:    "Warmth",
		Playable: true,
		Fields: map[string]json.RawMessage{
			"artists":     rawJSON(t, []string{"Bracket"}),
			"genres":      rawJSON(t, []string{"Punk Rock", "Alternative"}),
			"record_type": rawJSON(t, "album"),
		},
	}
	title := Project(item, decl, core.Presentations{})
	if len(title.Genres) != 2 || title.Genres[0] != "Punk Rock" || title.Genres[1] != "Alternative" {
		t.Fatalf("expected only real genres, got %v", title.Genres)
	}
}

// TestArtistStringsFieldIndexedAsPeople verifies that music plugins declaring
// artists as type:"strings" (e.g. Deezer Music) still index synthetic person IDs
// for FacetPeople, PersonByID, Browse(PersonID), and Project(Title.Cast), and
// self-heal pre-existing rows whose v_int was 0.
func TestArtistStringsFieldIndexedAsPeople(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	defer s.Close()

	decl := plugins.MediaKindMeta{
		Key:    "music",
		Label:  "Music",
		Levels: []plugins.KindLevel{{Key: "album", Label: "Album", Playable: true}},
		Fields: []plugins.KindField{
			{Key: "artists", Type: "strings", Label: "Artists", Indexed: true},
			{Key: "genres", Type: "strings", Label: "Genres", Indexed: true},
		},
		Browse: plugins.KindBrowseDecl{
			Sorts:  []string{"added", "title"},
			Facets: []string{"artists", "genres"},
		},
	}
	if _, err := s.UpsertKind(ctx, "deezer.music", 33, decl, false); err != nil {
		t.Fatal(err)
	}
	item := core.KindItem{
		ID:       "dz_1",
		Level:    "album",
		Title:    "Chaleur Humaine",
		Playable: true,
		Fields: map[string]json.RawMessage{
			"artists": rawJSON(t, []string{"Christine and the Queens"}),
			"genres":  rawJSON(t, []string{"Pop"}),
		},
	}
	if _, err := s.UpsertItems(ctx, "deezer.music", "music", []core.KindItem{item}); err != nil {
		t.Fatal(err)
	}

	// Projection populates Title.Cast for AlbumArtist/AlbumArtists credits.
	projected := Project(item, decl, core.Presentations{})
	wantPID := core.PluginPersonID("Christine and the Queens")
	if len(projected.Cast) != 1 || projected.Cast[0].Name != "Christine and the Queens" || projected.Cast[0].TMDB != wantPID {
		t.Fatalf("projected Cast = %+v, want Christine and the Queens (%d)", projected.Cast, wantPID)
	}
	if len(projected.Genres) != 1 || projected.Genres[0] != "Pop" {
		t.Fatalf("projected Genres = %v, want [Pop]", projected.Genres)
	}

	// FacetPeople, PersonByID, and Browse(PersonID) all resolve the strings-typed artist.
	fp, err := s.FacetPeople(ctx, FacetPeopleQuery{LibraryID: 33, FieldKey: "artists", Limit: 10})
	if err != nil {
		t.Fatalf("FacetPeople: %v", err)
	}
	if fp.Total != 1 || len(fp.Items) != 1 || fp.Items[0].PersonID != wantPID || fp.Items[0].Name != "Christine and the Queens" {
		t.Fatalf("FacetPeople = %+v, want 1 match (%d)", fp, wantPID)
	}
	pm, ok, err := s.PersonByID(ctx, 33, nil, wantPID, 10)
	if err != nil || !ok || pm.Name != "Christine and the Queens" {
		t.Fatalf("PersonByID = (%+v, %v, %v)", pm, ok, err)
	}
	bPage, err := s.Browse(ctx, BrowseQuery{LibraryID: 33, Level: "album", PersonID: wantPID, Limit: 10})
	if err != nil || len(bPage.Items) != 1 || bPage.Items[0].ID != "dz_1" {
		t.Fatalf("Browse(PersonID) = %+v, err=%v", bPage, err)
	}

	// Simulate a pre-upgrade DB row with v_int = 0 and verify UpsertKind self-heals via background reindex.
	if _, err := s.db.ExecContext(ctx, `UPDATE plugin_item_fields SET v_int = 0 WHERE field = 'artists'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertKind(ctx, "deezer.music", 33, decl, false); err != nil {
		t.Fatal(err)
	}
	// Wait for the background chunked reindex triggered by UpsertKind to finish.
	for i := 0; i < 200; i++ {
		var gotInt int64
		if err := s.db.QueryRowContext(ctx, `SELECT v_int FROM plugin_item_fields WHERE field = 'artists' LIMIT 1`).Scan(&gotInt); err == nil && gotInt == wantPID {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	var gotInt int64
	if err := s.db.QueryRowContext(ctx, `SELECT v_int FROM plugin_item_fields WHERE field = 'artists' LIMIT 1`).Scan(&gotInt); err != nil {
		t.Fatal(err)
	}
	if gotInt != wantPID {
		t.Fatalf("expected self-healed v_int=%d, got %d", wantPID, gotInt)
	}
}

type callerFunc func(context.Context, string, plugins.Request) (plugins.Response, error)

func (f callerFunc) Call(ctx context.Context, pluginID string, req plugins.Request) (plugins.Response, error) {
	if f == nil {
		return plugins.Response{}, errors.New("nil caller")
	}
	return f(ctx, pluginID, req)
}
