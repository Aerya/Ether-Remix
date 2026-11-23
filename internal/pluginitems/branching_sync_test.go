package pluginitems

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
)

func branchingMusicDecl() plugins.MediaKindMeta {
	return plugins.MediaKindMeta{
		Key:   "music",
		Label: "Music",
		Media: string(core.MediaClassMusic),
		Library: plugins.KindLibraryDecl{
			Name: "Music",
			Card: "square",
		},
		Levels: []plugins.KindLevel{
			{Key: "artist", Label: "Artist", Role: "artist", Container: true},
			{Key: "album", Label: "Album", Parent: "artist", Role: "album", Container: true},
			{Key: "track", Label: "Track", Parent: "album", Role: "track", Playable: true},
			{Key: "concert", Label: "Concert", Parent: "artist", Media: "video", Role: "concert", Playable: true},
		},
		Present: core.Presentations{
			Vela:         &core.VelaPresentation{Type: "shows"},
			Plex:         &core.PlexPresentation{Base: "artist"},
			MediaBrowser: &core.MediaBrowserPresentation{CollectionType: "music"},
		},
		Sync: plugins.KindSyncDecl{Mode: plugins.KindSyncPull},
	}
}

func TestBranchingSyncWalkParentTallyProjectionAndFilteredBrowse(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	defer s.Close()

	const (
		pluginID = "test.music"
		kindKey  = "music"
		libID    = core.LibraryID(91)
	)
	decl := branchingMusicDecl()
	if _, err := s.UpsertKind(ctx, pluginID, libID, decl, false); err != nil {
		t.Fatal(err)
	}

	// Pull sync BFS must walk both child branches under artist (album -> track AND concert).
	caller := callerFunc(func(_ context.Context, _ string, req plugins.Request) (plugins.Response, error) {
		switch req.Path {
		case "/ether/v1/kinds/music/browse":
			var br core.KindBrowseRequest
			_ = json.Unmarshal(req.Body, &br)
			var items []core.KindItem
			switch br.Level {
			case "artist":
				items = []core.KindItem{
					{ID: "art-1", Level: "artist", Title: "Miles Davis"},
				}
			case "album":
				if br.ParentID == "art-1" {
					items = []core.KindItem{
						{ID: "alb-1", Level: "album", ParentID: "art-1", Index: 1, Title: "Kind of Blue"},
						{ID: "alb-2", Level: "album", ParentID: "art-1", Index: 2, Title: "Bitches Brew"},
					}
				}
			case "track":
				switch br.ParentID {
				case "alb-1":
					items = []core.KindItem{
						{ID: "trk-1", Level: "track", ParentID: "alb-1", Index: 1, Title: "So What", Playable: true},
						{ID: "trk-2", Level: "track", ParentID: "alb-1", Index: 2, Title: "Blue in Green", Playable: true},
					}
				case "alb-2":
					items = []core.KindItem{
						{ID: "trk-3", Level: "track", ParentID: "alb-2", Index: 1, Title: "Pharaoh's Dance", Playable: true},
					}
				}
			case "concert":
				if br.ParentID == "art-1" {
					items = []core.KindItem{
						{ID: "con-1", Level: "concert", ParentID: "art-1", Index: 1, Title: "Live at Newport 1958", Playable: true},
						{ID: "con-2", Level: "concert", ParentID: "art-1", Index: 2, Title: "Live in Paris 1960", Playable: true},
						{ID: "con-3", Level: "concert", ParentID: "art-1", Index: 3, Title: "Isle of Wight 1970", Playable: true},
					}
				}
			}
			body, _ := json.Marshal(core.KindPage{Items: items})
			return plugins.Response{Status: http.StatusOK, Body: body}, nil
		case "/ether/v1/kinds/music/changes":
			return plugins.Response{Status: http.StatusNotFound}, nil
		default:
			return plugins.Response{Status: http.StatusNotFound}, nil
		}
	})
	if _, err := s.SyncKind(ctx, pluginID, kindKey, SyncOptions{Caller: caller}); err != nil {
		t.Fatalf("SyncKind branching tree failed: %v", err)
	}

	// Verify per-level parent tallies.
	ref := func(id string) core.ItemRef { return core.NewItemRef(pluginID, kindKey, id) }
	counts, err := s.ParentCountsMany(ctx, libID, []core.ItemRef{ref("art-1"), ref("alb-1"), ref("alb-2")})
	if err != nil {
		t.Fatal(err)
	}
	artCounts := counts[ref("art-1")]
	wantArtByLevel := map[string]int{"album": 2, "concert": 3}
	if artCounts.ChildCount != 5 || artCounts.LeafCount != 6 || !maps.Equal(artCounts.ByLevel, wantArtByLevel) {
		t.Fatalf("art-1 counts = %+v, want ChildCount=5 LeafCount=6 ByLevel=%v", artCounts, wantArtByLevel)
	}
	if alb1 := counts[ref("alb-1")]; alb1.ChildCount != 2 || alb1.LeafCount != 2 || alb1.ByLevel["track"] != 2 {
		t.Fatalf("alb-1 counts = %+v, want ChildCount=2 LeafCount=2 track=2", alb1)
	}

	// ValidateItem rejects concert parented under album instead of artist.
	if _, err := ValidateItem(decl, core.KindItem{ID: "bad-con", Level: "concert", ParentID: "alb-1", Title: "Wrong"}, "album"); err == nil {
		t.Fatal("expected ValidateItem to reject concert under album")
	}
	if _, err := ValidateItem(decl, core.KindItem{ID: "ok-con", Level: "concert", ParentID: "art-1", Title: "Right"}, "artist"); err != nil {
		t.Fatalf("ValidateItem rejected valid concert under artist: %v", err)
	}

	// Verify SQL-pushed Browse with ParentRef + Levels pages within that child level and reports exact O(1) total.
	p1, err := s.Browse(ctx, BrowseQuery{LibraryID: libID, ParentRef: ref("art-1"), Levels: []string{"concert"}, Sort: "index", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if p1.Total != 3 || !p1.TotalExact || len(p1.Items) != 2 || p1.Items[0].ID != "con-1" || p1.Items[1].ID != "con-2" || p1.NextCursor == "" {
		t.Fatalf("concert page 1 = %+v", p1)
	}
	p2, err := s.Browse(ctx, BrowseQuery{LibraryID: libID, ParentRef: ref("art-1"), Levels: []string{"concert"}, Sort: "index", Cursor: p1.NextCursor, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if p2.Total != 3 || len(p2.Items) != 1 || p2.Items[0].ID != "con-3" || p2.NextCursor != "" {
		t.Fatalf("concert page 2 = %+v", p2)
	}

	// Verify Projection of video-media child inside a music library vs music levels.
	conItem, _, err := s.GetByRef(ctx, ref("con-1"))
	if err != nil {
		t.Fatal(err)
	}
	conTitle := Project(conItem, decl, decl.Present)
	if conTitle.Type != core.TypeMovie || conTitle.MediaClass != core.MediaClassVideo || conTitle.MediaRole != "concert" || conTitle.MBItemType != "Video" {
		t.Fatalf("projected concert = Type:%q MediaClass:%q MediaRole:%q MBItemType:%q", conTitle.Type, conTitle.MediaClass, conTitle.MediaRole, conTitle.MBItemType)
	}
	trkItem, _, err := s.GetByRef(ctx, ref("trk-1"))
	if err != nil {
		t.Fatal(err)
	}
	trkTitle := Project(trkItem, decl, decl.Present)
	if trkTitle.Type != core.TypeEpisode || trkTitle.MediaClass != core.MediaClassMusic || trkTitle.MediaRole != core.MediaRoleTrack || trkTitle.MBItemType != "Audio" {
		t.Fatalf("projected track = Type:%q MediaClass:%q MediaRole:%q MBItemType:%q", trkTitle.Type, trkTitle.MediaClass, trkTitle.MediaRole, trkTitle.MBItemType)
	}

	// Verify deleting one concert and running Migration 16 backfill preserves exact per-level tallies.
	if err := s.KindDelete(ctx, pluginID, kindKey, []string{"con-2"}); err != nil {
		t.Fatal(err)
	}
	afterDel, err := s.ParentCountsMany(ctx, libID, []core.ItemRef{ref("art-1")})
	if err != nil {
		t.Fatal(err)
	}
	if g := afterDel[ref("art-1")]; g.ChildCount != 4 || g.LeafCount != 5 || g.ByLevel["album"] != 2 || g.ByLevel["concert"] != 2 {
		t.Fatalf("after delete art-1 counts = %+v, want ChildCount=4 LeafCount=5 album=2 concert=2", g)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM plugin_item_parent_tally`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := backfillParentTallyTx(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	afterBackfill, err := s.ParentCountsMany(ctx, libID, []core.ItemRef{ref("art-1")})
	if err != nil {
		t.Fatal(err)
	}
	if g := afterBackfill[ref("art-1")]; g.ChildCount != 4 || g.LeafCount != 5 || g.ByLevel["album"] != 2 || g.ByLevel["concert"] != 2 {
		t.Fatalf("after backfill art-1 counts = %+v, want ChildCount=4 LeafCount=5 album=2 concert=2", g)
	}
}

func TestBranchingParentTallyRebuiltOnChunkedReindexUpgradeAndStartup(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	defer s.Close()

	const (
		pluginID = "test.music.tally"
		kindKey  = "music"
		libID    = core.LibraryID(92)
	)
	ref := func(id string) core.ItemRef { return core.NewItemRef(pluginID, kindKey, id) }

	// Start with a 3-level kind (artist -> album -> track), before the concert branch exists.
	baseDecl := branchingMusicDecl()
	baseDecl.Levels = baseDecl.Levels[:3]
	if _, err := s.UpsertKind(ctx, pluginID, libID, baseDecl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KindUpsert(ctx, pluginID, kindKey, []core.KindItem{
		{ID: "art-1", Level: "artist", Title: "AC/DC"},
		{ID: "alb-1", Level: "album", ParentID: "art-1", Index: 1, Title: "Back in Black"},
		{ID: "trk-1", Level: "track", ParentID: "alb-1", Index: 1, Title: "Hells Bells", Playable: true},
		{ID: "trk-2", Level: "track", ParentID: "alb-1", Index: 2, Title: "Shoot to Thrill", Playable: true},
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate missing parent tally before an additive level-tree upgrade (adding concert branch).
	if _, err := db.ExecContext(ctx, `DELETE FROM plugin_item_parent_tally WHERE library_id = ?`, int64(libID)); err != nil {
		t.Fatal(err)
	}
	fullDecl := branchingMusicDecl()
	if _, err := s.UpsertKind(ctx, pluginID, libID, fullDecl, false); err != nil {
		t.Fatalf("UpsertKind adding concert branch: %v", err)
	}
	counts, err := s.ParentCountsMany(ctx, libID, []core.ItemRef{ref("art-1"), ref("alb-1")})
	if err != nil {
		t.Fatal(err)
	}
	if g := counts[ref("art-1")]; g.ChildCount != 1 || g.LeafCount != 2 || g.ByLevel["album"] != 1 {
		t.Fatalf("after level-upgrade UpsertKind art-1 = %+v, want ChildCount=1 LeafCount=2 album=1", g)
	}

	// Add a concert child so art-1 has both branches.
	if _, err := s.KindUpsert(ctx, pluginID, kindKey, []core.KindItem{
		{ID: "con-1", Level: "concert", ParentID: "art-1", Index: 1, Title: "Live at Donington", Playable: true},
	}); err != nil {
		t.Fatal(err)
	}

	// PrimaryChildLevel is set to the primary branch ("album") on branching artist, empty on non-branching album.
	artItem, _, err := s.GetByRef(ctx, ref("art-1"))
	if err != nil {
		t.Fatal(err)
	}
	if artTitle := Project(artItem, fullDecl, fullDecl.Present); artTitle.PrimaryChildLevel != "album" {
		t.Fatalf("artist PrimaryChildLevel = %q, want album", artTitle.PrimaryChildLevel)
	}
	albItem, _, err := s.GetByRef(ctx, ref("alb-1"))
	if err != nil {
		t.Fatal(err)
	}
	if albTitle := Project(albItem, fullDecl, fullDecl.Present); albTitle.PrimaryChildLevel != "" {
		t.Fatalf("album PrimaryChildLevel = %q, want empty", albTitle.PrimaryChildLevel)
	}

	// 1. Chunked reindex must rebuild plugin_item_parent_tally.
	if _, err := db.ExecContext(ctx, `DELETE FROM plugin_item_parent_tally WHERE library_id = ?`, int64(libID)); err != nil {
		t.Fatal(err)
	}
	if err := s.reindexKindChunked(ctx, store.DialectOf(db) == store.Postgres, pluginID, kindKey, libID, fullDecl); err != nil {
		t.Fatalf("reindexKindChunked: %v", err)
	}
	afterReindex, err := s.ParentCountsMany(ctx, libID, []core.ItemRef{ref("art-1")})
	if err != nil {
		t.Fatal(err)
	}
	if g := afterReindex[ref("art-1")]; g.ChildCount != 2 || g.LeafCount != 3 || g.ByLevel["album"] != 1 || g.ByLevel["concert"] != 1 {
		t.Fatalf("after chunked reindex art-1 = %+v, want ChildCount=2 LeafCount=3 album=1 concert=1", g)
	}

	// 2. Startup ensureParentTallies must detect and heal partial/missing parent tallies.
	if _, err := db.ExecContext(ctx, `DELETE FROM plugin_item_parent_tally WHERE library_id = ? AND parent_ref = ?`, int64(libID), string(ref("art-1"))); err != nil {
		t.Fatal(err)
	}
	s.ensureParentTallies(ctx)
	afterStartup, err := s.ParentCountsMany(ctx, libID, []core.ItemRef{ref("art-1")})
	if err != nil {
		t.Fatal(err)
	}
	if g := afterStartup[ref("art-1")]; g.ChildCount != 2 || g.LeafCount != 3 || g.ByLevel["album"] != 1 || g.ByLevel["concert"] != 1 {
		t.Fatalf("after ensureParentTallies art-1 = %+v, want ChildCount=2 LeafCount=3 album=1 concert=1", g)
	}
}
