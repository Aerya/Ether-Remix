package pluginitems

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

func TestBrowseLastViewedAndHistoryFilterParentRollup(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})
	setupViewerStateTables(t, db)

	decl := plugins.MediaKindMeta{
		Key:     "music",
		Label:   "Music",
		Media:   string(core.MediaClassMusic),
		Library: plugins.KindLibraryDecl{Name: "Music"},
		Levels: []plugins.KindLevel{
			{Key: "artist", Label: "Artist", Role: string(core.MediaRoleArtist), Container: true},
			{Key: "album", Label: "Album", Role: string(core.MediaRoleAlbum), Container: true},
			{Key: "track", Label: "Track", Role: string(core.MediaRoleTrack), Playable: true},
		},
		Browse: plugins.KindBrowseDecl{Sorts: []string{"title", "lastViewed"}},
		Sync:   plugins.KindSyncDecl{Mode: plugins.KindSyncPush},
	}
	const libID = core.LibraryID(70)
	if _, err := s.UpsertKind(ctx, "community.music", libID, decl, false); err != nil {
		t.Fatal(err)
	}

	items := []core.KindItem{
		{ID: "ar1", Level: "artist", Title: "Miles Davis", ChildCount: 2},
		{ID: "ar2", Level: "artist", Title: "John Coltrane", ChildCount: 1},
		{ID: "al1", Level: "album", ParentID: "ar1", Title: "Kind of Blue", ChildCount: 2},
		{ID: "al2", Level: "album", ParentID: "ar1", Title: "Bitches Brew", ChildCount: 1},
		{ID: "al3", Level: "album", ParentID: "ar2", Title: "A Love Supreme", ChildCount: 1},
		{ID: "tr1", Level: "track", ParentID: "al1", Title: "So What", Playable: true, Index: 1},
		{ID: "tr2", Level: "track", ParentID: "al1", Title: "Freddie Freeloader", Playable: true, Index: 2},
		{ID: "tr3", Level: "track", ParentID: "al2", Title: "Pharaoh's Dance", Playable: true, Index: 1},
		{ID: "tr4", Level: "track", ParentID: "al3", Title: "Acknowledgement", Playable: true, Index: 1},
	}
	if _, err := s.UpsertItems(ctx, "community.music", "music", items); err != nil {
		t.Fatal(err)
	}

	keyOf := func(id string) int64 {
		it, _, err := s.GetByExternal(ctx, "community.music", "music", id)
		if err != nil {
			t.Fatalf("GetByExternal(%s): %v", id, err)
		}
		return int64(it.Key)
	}

	var historyTF core.TabFilter
	if err := json.Unmarshal([]byte(`{"match":"all","rules":[{"field":"viewer","op":"is","value":{"history":true}}]}`), &historyTF); err != nil {
		t.Fatal(err)
	}
	specs, fallback := historyTF.ToPolicy("")
	historyPolicy, err := s.CompilePolicy(ctx, libID, specs, fallback)
	if err != nil {
		t.Fatalf("CompilePolicy: %v", err)
	}

	// Viewer 1 has no history yet: history shelf MUST return 0 items (never unviewed).
	emptyPage, err := s.Browse(ctx, BrowseQuery{
		LibraryID: libID,
		ViewerID:  1,
		Level:     "album",
		Sort:      "lastViewed:desc",
		TabPolicy: historyPolicy,
		Limit:     20,
	})
	if err != nil {
		t.Fatalf("empty history browse: %v", err)
	}
	if len(emptyPage.Items) != 0 || emptyPage.Total != 0 {
		t.Fatalf("viewer with no history got %d items (total %d), want 0", len(emptyPage.Items), emptyPage.Total)
	}

	// Viewer 1 plays tr1 (al1) at t=100, tr3 (al2) at t=200, then tr2 (al1) at t=300.
	// Viewer 2 plays tr4 (al3) at t=400.
	for _, row := range []struct {
		uid int64
		key int64
		at  int64
	}{
		{1, keyOf("tr1"), 100},
		{1, keyOf("tr3"), 200},
		{1, keyOf("tr2"), 300},
		{2, keyOf("tr4"), 400},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO state_watch(user_id,rating_key,view_count,last_viewed_at) VALUES (?,?,1,?)`, row.uid, row.key, row.at); err != nil {
			t.Fatal(err)
		}
	}

	// Album history shelf for viewer 1: al1 (MAX=300) first, al2 (200) second;
	// al1 must NOT be duplicated, and al3 (only played by viewer 2) must NOT appear.
	albumPage, err := s.Browse(ctx, BrowseQuery{
		LibraryID: libID,
		ViewerID:  1,
		Level:     "album",
		Sort:      "lastViewed:desc",
		TabPolicy: historyPolicy,
		Limit:     20,
	})
	if err != nil {
		t.Fatalf("album history browse: %v", err)
	}
	if len(albumPage.Items) != 2 || albumPage.Total != 2 {
		t.Fatalf("album history len=%d total=%d, want 2: %+v", len(albumPage.Items), albumPage.Total, albumPage.Items)
	}
	if albumPage.Items[0].ID != "al1" || albumPage.Items[1].ID != "al2" {
		t.Fatalf("album history order = [%s, %s], want [al1, al2]", albumPage.Items[0].ID, albumPage.Items[1].ID)
	}

	// Grandparent artist rollup: viewer 1 played tracks under ar1 only.
	artistPage, err := s.Browse(ctx, BrowseQuery{
		LibraryID: libID,
		ViewerID:  1,
		Level:     "artist",
		Sort:      "lastViewed:desc",
		TabPolicy: historyPolicy,
		Limit:     20,
	})
	if err != nil {
		t.Fatalf("artist history browse: %v", err)
	}
	if len(artistPage.Items) != 1 || artistPage.Items[0].ID != "ar1" {
		t.Fatalf("artist history = %+v, want [ar1]", artistPage.Items)
	}

	// Without history filter, sort=lastViewed still ranks rolled-up albums
	// ahead of unviewed albums without duplicating al1 or al2.
	allAlbums, err := s.Browse(ctx, BrowseQuery{
		LibraryID: libID,
		ViewerID:  1,
		Level:     "album",
		Sort:      "lastViewed:desc",
		Limit:     20,
	})
	if err != nil {
		t.Fatalf("unfiltered lastViewed albums: %v", err)
	}
	if len(allAlbums.Items) != 3 || allAlbums.Items[0].ID != "al1" || allAlbums.Items[1].ID != "al2" || allAlbums.Items[2].ID != "al3" {
		t.Fatalf("unfiltered lastViewed albums = %+v, want [al1, al2, al3]", allAlbums.Items)
	}
}
