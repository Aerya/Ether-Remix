package pluginitems

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

func TestPostgresCompatQueries(t *testing.T) {
	ctx := context.Background()
	s, db := openTestService(t, Options{})

	const libID = core.LibraryID(91)
	decl := plugins.MediaKindMeta{
		Key:   "movie",
		Label: "Movies",
		Library: plugins.KindLibraryDecl{
			Name: "Movies",
		},
		Levels: []plugins.KindLevel{{Key: "movie", Label: "Movie", Playable: true}},
		Fields: []plugins.KindField{
			{Key: "genre", Type: "string", Label: "Genre", Indexed: true},
			{Key: "runtime", Type: "int", Label: "Runtime", Indexed: true},
			{Key: "score", Type: "float", Label: "Score", Indexed: true},
			{Key: "cast", Type: "people", Label: "Cast", Indexed: true},
		},
		Browse: plugins.KindBrowseDecl{Sorts: []string{"title", "year", "rating"}},
		Sync:   plugins.KindSyncDecl{Mode: plugins.KindSyncPull},
	}

	if _, err := s.UpsertKind(ctx, "test.pgcompat", libID, decl, false); err != nil {
		t.Fatalf("UpsertKind fresh: %v", err)
	}

	rawJSON := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	items := []core.KindItem{
		{
			ID:       "m1",
			Level:    "movie",
			Title:    "Alpha",
			Year:     2020,
			Rating:   7.5,
			Playable: true,
			Fields: map[string]json.RawMessage{
				"genre":   rawJSON("Action"),
				"runtime": rawJSON(100),
				"score":   rawJSON(7.5),
				"cast":    rawJSON([]core.Person{{Name: "Alice", TMDB: core.PluginPersonIDBase + 1}}),
			},
		},
		{
			ID:       "m2",
			Level:    "movie",
			Title:    "Beta",
			Year:     2021,
			Rating:   8.5,
			Playable: true,
			Fields: map[string]json.RawMessage{
				"genre":   rawJSON("Drama"),
				"runtime": rawJSON(120),
				"score":   rawJSON(8.5),
				"cast":    rawJSON([]core.Person{{Name: "Bob", TMDB: core.PluginPersonIDBase + 2}}),
			},
		},
	}
	if _, err := s.UpsertItems(ctx, "test.pgcompat", "movie", items); err != nil {
		t.Fatalf("UpsertItems: %v", err)
	}

	// Exercise browseTallyProbe when plugin_item_tally is empty but plugin_items has rows.
	if _, err := db.ExecContext(ctx, `DELETE FROM plugin_item_tally WHERE library_id = ?`, int64(libID)); err != nil {
		t.Fatal(err)
	}
	page, err := s.Browse(ctx, BrowseQuery{LibraryID: libID, Level: "movie", Limit: 10})
	if err != nil {
		t.Fatalf("Browse without tally: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("Browse len = %d, want 2", len(page.Items))
	}

	// Exercise UpsertKind with pre-existing plugin_items rows (EXISTS() -> bool).
	if _, err := db.ExecContext(ctx, `DELETE FROM plugin_kinds WHERE plugin_id = ? AND kind_key = ?`, "test.pgcompat", "movie"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertKind(ctx, "test.pgcompat", libID, decl, false); err != nil {
		t.Fatalf("UpsertKind with pre-existing items: %v", err)
	}

	// Exercise libraryHasAtMostItems subquery alias.
	small, err := s.libraryHasAtMostItems(ctx, `i.library_id = ?`, []any{int64(libID)}, 10)
	if err != nil || !small {
		t.Fatalf("libraryHasAtMostItems = (%v, %v), want (true, nil)", small, err)
	}

	// Exercise all 6 WITH RECURSIVE large-library helpers (smallLib=false).
	itemWhere := `i.library_id = ? AND i.level = ?`
	itemArgs := []any{int64(libID), "movie"}
	if yrs, err := s.scanEnvelopeYearCounts(ctx, libID, itemWhere, itemArgs, false); err != nil || len(yrs) != 2 {
		t.Fatalf("scanEnvelopeYearCounts(large) = (%d, %v), want 2", len(yrs), err)
	}
	if rats, err := s.scanEnvelopeRatingCounts(ctx, libID, itemWhere, itemArgs, false); err != nil || len(rats) != 2 {
		t.Fatalf("scanEnvelopeRatingCounts(large) = (%d, %v), want 2", len(rats), err)
	}
	if ints, err := s.scanFieldIntCounts(ctx, "runtime", itemWhere, itemWhere, itemArgs, false); err != nil || len(ints) != 2 {
		t.Fatalf("scanFieldIntCounts(large) = (%d, %v), want 2", len(ints), err)
	}
	if flts, err := s.scanFieldFloatCounts(ctx, "score", itemWhere, itemWhere, itemArgs, false); err != nil || len(flts) != 2 {
		t.Fatalf("scanFieldFloatCounts(large) = (%d, %v), want 2", len(flts), err)
	}
	if txts, err := s.scanFieldTextCountsLarge(ctx, "genre", itemWhere, itemArgs, 2001); err != nil || len(txts) != 2 {
		t.Fatalf("scanFieldTextCountsLarge = (%d, %v), want 2", len(txts), err)
	}
	if ppl, err := s.scanPeopleCandidatesLarge(ctx, "cast", itemWhere, itemArgs, 2001); err != nil || len(ppl) != 2 {
		t.Fatalf("scanPeopleCandidatesLarge = (%d, %v), want 2", len(ppl), err)
	}

	// Exercise resolveNewestByPredicateBuckets with >= 2 buckets (UNION ALL CAST).
	fc, err := s.FacetCollections(ctx, FacetCollectionsQuery{
		LibraryID: libID,
		Level:     "movie",
		FieldKey:  "year",
		Limit:     10,
	})
	if err != nil || len(fc.Items) != 2 {
		t.Fatalf("FacetCollections(year) = (%d items, %v), want 2", len(fc.Items), err)
	}

	// Exercise multi-arm UNION ALL span helpers (scanEnvelopeReleasedBuckets,
	// scanFieldIntSpanBuckets with includeZero=true).
	t.Run("value_picker_tallies_and_reps", func(t *testing.T) {
		const tallyLibID = core.LibraryID(92)
		const tallyPlugin = "test.pgcompat.tally"
		const tallyKind = "film"
		tallyDecl := plugins.MediaKindMeta{
			Key:     tallyKind,
			Label:   "Films",
			Library: plugins.KindLibraryDecl{Name: "Films"},
			Levels:  []plugins.KindLevel{{Key: "film", Label: "Film", Playable: true}},
			Fields: []plugins.KindField{
				{Key: "genre", Type: "strings", Label: "Genre", Indexed: true, Values: []string{"Café Noir", "Sci-Fi", "Unpopulated"}},
				{Key: "runtime", Type: "int", Label: "Runtime", Indexed: true},
				{Key: "cast", Type: "people", Label: "Cast", Indexed: true},
				{Key: "directors", Type: "people", Label: "Directors", Indexed: true},
			},
			Browse: plugins.KindBrowseDecl{
				Sorts:  []string{"title", "released", "added", "year", "rating"},
				Facets: []string{"genre", "cast", "directors"},
			},
			Sync: plugins.KindSyncDecl{Mode: plugins.KindSyncPull},
		}
		if _, err := s.UpsertKind(ctx, tallyPlugin, tallyLibID, tallyDecl, false); err != nil {
			t.Fatalf("UpsertKind(tally): %v", err)
		}

		rel2020 := time.Date(2020, 6, 15, 0, 0, 0, 0, time.UTC).Unix()
		rel2021 := time.Date(2021, 6, 15, 0, 0, 0, 0, time.UTC).Unix()
		rel2022 := time.Date(2022, 6, 15, 0, 0, 0, 0, time.UTC).Unix()
		tallyItems := []core.KindItem{
			{
				ID:       "f1",
				Level:    "film",
				Title:    "First Film",
				Year:     2020,
				Released: rel2020,
				Rating:   7.0,
				Playable: true,
				Art:      []core.KindArtRef{{Role: "poster", URL: "https://img.example.test/f1.jpg"}},
				Fields: map[string]json.RawMessage{
					"genre":   rawJSON([]string{"Café Noir", "Sci-Fi"}),
					"runtime": rawJSON(0),
					"cast":    rawJSON([]map[string]any{{"name": "Élodie Moreau", "thumb": "https://img.example.test/elodie-f1.jpg"}}),
				},
			},
			{
				ID:       "f2",
				Level:    "film",
				Title:    "Second Film",
				Year:     2021,
				Released: rel2021,
				Rating:   8.0,
				Playable: true,
				Art:      []core.KindArtRef{{Role: "poster", URL: "https://img.example.test/f2.jpg"}},
				Fields: map[string]json.RawMessage{
					"genre":     rawJSON([]string{"Sci-Fi"}),
					"runtime":   rawJSON(95),
					"cast":      rawJSON([]map[string]any{{"name": "Élodie Moreau"}}),
					"directors": rawJSON([]map[string]any{{"name": "Élodie Moreau", "thumb": "https://img.example.test/elodie-dir.jpg"}}),
				},
			},
			{
				ID:       "f3",
				Level:    "film",
				Title:    "Third Film",
				Year:     2022,
				Released: rel2022,
				Rating:   9.0,
				Playable: true,
				Art:      []core.KindArtRef{{Role: "poster", URL: "https://img.example.test/f3.jpg"}},
				Fields: map[string]json.RawMessage{
					"genre":   rawJSON([]string{"Sci-Fi"}),
					"runtime": rawJSON(140),
					"cast":    rawJSON([]map[string]any{{"name": "Élodie Moreau"}, {"name": "Marc Dupont"}}),
				},
			},
		}
		if _, err := s.UpsertItems(ctx, tallyPlugin, tallyKind, tallyItems); err != nil {
			t.Fatalf("UpsertItems(tally): %v", err)
		}

		// Value picker (FieldValues): count/name order, paging, folded search,
		// non-folded LIKE ESCAPE fallback, declared zero-count values + offset, people.
		fvPage1, err := s.FieldValues(ctx, FieldValuesQuery{
			LibraryID:       tallyLibID,
			FieldKey:        "genre",
			Order:           "count",
			Limit:           1,
			Offset:          0,
			IncludeDeclared: true,
		})
		if err != nil || len(fvPage1.Values) != 1 || fvPage1.Values[0].Value != "Sci-Fi" || fvPage1.Values[0].Count != 3 || !fvPage1.More {
			t.Fatalf("FieldValues(page1) = (%+v, %v), want Sci-Fi(3) more=true", fvPage1, err)
		}
		fvUnpop, err := s.FieldValues(ctx, FieldValuesQuery{
			LibraryID:       tallyLibID,
			FieldKey:        "genre",
			Order:           "name",
			Limit:           5,
			Offset:          2,
			IncludeDeclared: true,
		})
		if err != nil || len(fvUnpop.Values) != 1 || fvUnpop.Values[0].Value != "Unpopulated" || fvUnpop.Values[0].Count != 0 || fvUnpop.More {
			t.Fatalf("FieldValues(unpopulated offset) = (%+v, %v), want Unpopulated(0)", fvUnpop, err)
		}
		fvFold, err := s.FieldValues(ctx, FieldValuesQuery{LibraryID: tallyLibID, FieldKey: "genre", Query: "cafe", Limit: 10})
		if err != nil || len(fvFold.Values) != 1 || fvFold.Values[0].Value != "Café Noir" {
			t.Fatalf("FieldValues(folded) = (%+v, %v), want Café Noir", fvFold, err)
		}
		fvFallbackLike, err := s.FieldValues(ctx, FieldValuesQuery{LibraryID: tallyLibID, FieldKey: "genre", Query: "-", Limit: 10})
		if err != nil || len(fvFallbackLike.Values) != 1 || fvFallbackLike.Values[0].Value != "Sci-Fi" {
			t.Fatalf("FieldValues(non-folded LIKE) = (%+v, %v), want Sci-Fi", fvFallbackLike, err)
		}
		fvPeople, err := s.FieldValues(ctx, FieldValuesQuery{LibraryID: tallyLibID, FieldKey: "cast", Query: "elodie", Limit: 10})
		if err != nil || len(fvPeople.Values) != 1 || fvPeople.Values[0].Value != "Élodie Moreau" || fvPeople.Values[0].Count != 3 {
			t.Fatalf("FieldValues(people) = (%+v, %v), want Élodie Moreau(3)", fvPeople, err)
		}

		// Tally-backed FacetBuckets, FacetCollections (rep_ref), and FacetPeople (thumb_ref).
		textBuckets, err := s.FacetBuckets(ctx, tallyLibID, "film", nil, "genre")
		if err != nil || len(textBuckets) != 2 {
			t.Fatalf("FacetBuckets(genre) = (%+v, %v), want 2 buckets", textBuckets, err)
		}
		pplBuckets, err := s.FacetBuckets(ctx, tallyLibID, "film", nil, "cast")
		if err != nil || len(pplBuckets) != 2 {
			t.Fatalf("FacetBuckets(cast) = (%+v, %v), want 2 buckets", pplBuckets, err)
		}
		fcGenre, err := s.FacetCollections(ctx, FacetCollectionsQuery{LibraryID: tallyLibID, Level: "film", FieldKey: "genre", Order: "count", Limit: 10})
		if err != nil || len(fcGenre.Items) != 2 || fcGenre.Items[0].NewestItem == nil || fcGenre.Items[0].NewestItem.ID != "f3" {
			t.Fatalf("FacetCollections(genre) = (%+v, %v), want Sci-Fi rep=f3", fcGenre.Items, err)
		}
		fpCast, err := s.FacetPeople(ctx, FacetPeopleQuery{LibraryID: tallyLibID, Level: "film", FieldKey: "cast", Order: "count", Limit: 10})
		if err != nil || len(fpCast.Items) != 2 || fpCast.Items[0].SampleItem == nil || fpCast.Items[0].SampleItem.ID != "f2" || fpCast.Items[0].Thumb != "https://img.example.test/elodie-dir.jpg" {
			t.Fatalf("FacetPeople(cast) = (%+v, %v), want Élodie thumb_ref=f2", fpCast.Items, err)
		}

		// Multi-field SearchPeople (GROUP BY + SUM(cnt) AS cnt), PersonByID,
		// LookupPersonFacetByName, LookupPersonFacetsByID, FindPersonID.
		sp, err := s.SearchPeople(ctx, tallyLibID, nil, "moreau", 10, 5)
		if err != nil || len(sp) != 1 || sp[0].CreditCount != 3 {
			t.Fatalf("SearchPeople(multi-field) = (%+v, %v), want 1 match with count=3", sp, err)
		}
		elodiePID, canon, ok, err := s.LookupPersonFacetByName(ctx, tallyLibID, "cast", "elodie moreau")
		if err != nil || !ok || canon != "Élodie Moreau" {
			t.Fatalf("LookupPersonFacetByName = (%d, %q, %v, %v)", elodiePID, canon, ok, err)
		}
		facetMatches, err := s.LookupPersonFacetsByID(ctx, tallyLibID, []string{"cast", "directors"}, elodiePID)
		if err != nil || len(facetMatches) != 2 {
			t.Fatalf("LookupPersonFacetsByID = (%+v, %v), want 2 fields", facetMatches, err)
		}
		pm, ok, err := s.PersonByID(ctx, tallyLibID, nil, elodiePID, 5)
		if err != nil || !ok || pm.CreditCount != 3 || pm.Roles["actor"] != 3 || pm.Roles["director"] != 1 {
			t.Fatalf("PersonByID = (%+v, %v, %v), want count=3 actor=3 director=1", pm, ok, err)
		}
		foundPID, ok, err := s.FindPersonID(ctx, tallyLibID, nil, func(pid int64, _ string) bool { return pid == elodiePID })
		if err != nil || !ok || foundPID != elodiePID {
			t.Fatalf("FindPersonID = (%d, %v, %v), want %d", foundPID, ok, err, elodiePID)
		}

		// Representative recomputation on delete (rep_ref shifts f3 -> f2, thumb_ref shifts f2 -> f1).
		if err := s.KindDelete(ctx, tallyPlugin, tallyKind, []string{"f3", "f2"}); err != nil {
			t.Fatalf("KindDelete(f3, f2): %v", err)
		}
		fcAfterDel, err := s.FacetCollections(ctx, FacetCollectionsQuery{LibraryID: tallyLibID, Level: "film", FieldKey: "genre", Order: "count", Limit: 10})
		if err != nil || len(fcAfterDel.Items) != 2 || fcAfterDel.Items[0].NewestItem == nil || fcAfterDel.Items[0].NewestItem.ID != "f1" {
			t.Fatalf("FacetCollections after delete = (%+v, %v), want rep=f1", fcAfterDel.Items, err)
		}
		fpAfterDel, err := s.FacetPeople(ctx, FacetPeopleQuery{LibraryID: tallyLibID, Level: "film", FieldKey: "cast", Order: "count", Limit: 10})
		if err != nil || len(fpAfterDel.Items) != 1 || fpAfterDel.Items[0].SampleItem == nil || fpAfterDel.Items[0].SampleItem.ID != "f1" || fpAfterDel.Items[0].Thumb != "https://img.example.test/elodie-f1.jpg" {
			t.Fatalf("FacetPeople after delete = (%+v, %v), want thumb_ref=f1", fpAfterDel.Items, err)
		}

		// Re-seed f2/f3 so multi-arm UNION ALL helpers have >= 2 spans/shelves.
		if _, err := s.UpsertItems(ctx, tallyPlugin, tallyKind, tallyItems[1:]); err != nil {
			t.Fatalf("UpsertItems(re-seed): %v", err)
		}
		tWhere := `i.library_id = ? AND i.level = ?`
		tArgs := []any{int64(tallyLibID), "film"}
		relBuckets, err := s.scanEnvelopeReleasedBuckets(ctx, tallyLibID, tWhere, tArgs, false)
		if err != nil || len(relBuckets) != 3 {
			t.Fatalf("scanEnvelopeReleasedBuckets(large) = (%+v, %v), want 3 year buckets", relBuckets, err)
		}
		spanDefs := []intSpanDef{
			{id: "0..100", label: "Short", lo: 0, hi: 100},
			{id: "101..200", label: "Long", lo: 101, hi: 200},
		}
		spanBuckets, err := s.scanFieldIntSpanBuckets(ctx, "runtime", tWhere, tArgs, spanDefs, true)
		if err != nil || len(spanBuckets) != 2 || spanBuckets[0].Count != 2 || spanBuckets[1].Count != 1 {
			t.Fatalf("scanFieldIntSpanBuckets(includeZero) = (%+v, %v), want [2, 1]", spanBuckets, err)
		}
		batchPages, err := s.BrowseBatch(ctx, []BrowseQuery{
			{LibraryID: tallyLibID, Level: "film", Sort: "released:desc", NoTotal: true, Limit: 5},
			{LibraryID: tallyLibID, Level: "film", Sort: "title:asc", Facets: map[string][]string{"genre": {"Sci-Fi"}}, NoTotal: true, Limit: 5},
			{LibraryID: tallyLibID, Level: "film", Sort: "rating:desc", Facets: map[string][]string{"genre": {"Café Noir", "Sci-Fi"}}, NoTotal: true, Limit: 5},
		})
		if err != nil || len(batchPages) != 3 || len(batchPages[0].Items) != 3 || len(batchPages[1].Items) != 3 || len(batchPages[2].Items) != 3 {
			t.Fatalf("BrowseBatch = (%d pages, %v), want 3x3 items", len(batchPages), err)
		}
	})

	t.Run("parent_tally_parts_and_expansion_freshness", func(t *testing.T) {
		const musicLibID = core.LibraryID(93)
		const musicPlugin = "test.pgcompat.music"
		const musicKind = "music"
		if _, err := s.UpsertKind(ctx, musicPlugin, musicLibID, branchingMusicDecl(), false); err != nil {
			t.Fatalf("UpsertKind(music): %v", err)
		}
		artistRef := core.NewItemRef(musicPlugin, musicKind, "ar1")
		albumRef := core.NewItemRef(musicPlugin, musicKind, "al1")
		trackRef := core.NewItemRef(musicPlugin, musicKind, "tr1")

		// Empty container freshness before children exist.
		hasChildren, _, err := s.ChildrenFreshness(ctx, musicLibID, artistRef)
		if err != nil || hasChildren {
			t.Fatalf("ChildrenFreshness(empty) = (%v, %v), want false", hasChildren, err)
		}
		cnt, _, err := s.ChildrenCountAndFreshness(ctx, musicLibID, artistRef)
		if err != nil || cnt != 0 {
			t.Fatalf("ChildrenCountAndFreshness(empty) = (%d, %v), want 0", cnt, err)
		}

		// Upsert branching hierarchy: artist -> album -> track and artist -> concert.
		if _, err := s.UpsertItems(ctx, musicPlugin, musicKind, []core.KindItem{
			{ID: "ar1", Level: "artist", Title: "Artist One"},
			{ID: "al1", Level: "album", ParentID: "ar1", Index: 1, Title: "Album One"},
			{ID: "tr1", Level: "track", ParentID: "al1", Index: 1, Title: "Track One", Playable: true},
			{ID: "tr2", Level: "track", ParentID: "al1", Index: 2, Title: "Track Two", Playable: true},
			{ID: "co1", Level: "concert", ParentID: "ar1", Index: 1, Title: "Concert One", Playable: true},
		}); err != nil {
			t.Fatalf("UpsertItems(music): %v", err)
		}

		// Per-level parent tally queries: ParentCountsMany + Browse(ParentRef, Level/Levels).
		pCounts, err := s.ParentCountsMany(ctx, musicLibID, []core.ItemRef{artistRef, albumRef})
		if err != nil {
			t.Fatalf("ParentCountsMany: %v", err)
		}
		if g := pCounts[artistRef]; g.ChildCount != 2 || g.LeafCount != 3 || g.ByLevel["album"] != 1 || g.ByLevel["concert"] != 1 {
			t.Fatalf("ParentCountsMany[artist] = %+v, want ChildCount=2 LeafCount=3 album=1 concert=1", g)
		}
		allKids, err := s.Browse(ctx, BrowseQuery{LibraryID: musicLibID, ParentRef: artistRef, Limit: 10})
		if err != nil || allKids.Total != 2 || len(allKids.Items) != 2 {
			t.Fatalf("Browse(parent all levels) = (total=%d len=%d err=%v), want 2/2", allKids.Total, len(allKids.Items), err)
		}
		albumKids, err := s.Browse(ctx, BrowseQuery{LibraryID: musicLibID, ParentRef: artistRef, Level: "album", Limit: 10})
		if err != nil || albumKids.Total != 1 || len(albumKids.Items) != 1 {
			t.Fatalf("Browse(parent level=album) = (total=%d len=%d err=%v), want 1/1", albumKids.Total, len(albumKids.Items), err)
		}
		multiKids, err := s.Browse(ctx, BrowseQuery{LibraryID: musicLibID, ParentRef: artistRef, Levels: []string{"album", "concert"}, Limit: 10})
		if err != nil || multiKids.Total != 2 || len(multiKids.Items) != 2 {
			t.Fatalf("Browse(parent levels=album,concert) = (total=%d len=%d err=%v), want 2/2", multiKids.Total, len(multiKids.Items), err)
		}

		// Delete parent tallies and re-run Browse(ParentRef) with both single level
		// and all levels to exercise browseTallyProbe's EXISTS() -> bool fallback.
		if _, err := db.ExecContext(ctx, `DELETE FROM plugin_item_parent_tally WHERE library_id = ?`, int64(musicLibID)); err != nil {
			t.Fatal(err)
		}
		if pg, err := s.Browse(ctx, BrowseQuery{LibraryID: musicLibID, ParentRef: artistRef, Level: "album", Limit: 10}); err != nil || len(pg.Items) != 1 {
			t.Fatalf("Browse(parent fallback single level) = (%d, %v), want 1", len(pg.Items), err)
		}
		if pg, err := s.Browse(ctx, BrowseQuery{LibraryID: musicLibID, ParentRef: artistRef, Limit: 10}); err != nil || len(pg.Items) != 2 {
			t.Fatalf("Browse(parent fallback all levels) = (%d, %v), want 2", len(pg.Items), err)
		}

		// CountPartsMany empty-table EXISTS() -> bool probe, then populated parts.
		s.partsState.Store(0)
		partCounts, err := s.CountPartsMany(ctx, []core.ItemRef{trackRef})
		if err != nil || len(partCounts) != 0 {
			t.Fatalf("CountPartsMany(empty) = (%v, %v), want empty", partCounts, err)
		}
		if _, err := s.SaveParts(ctx, trackRef, []core.KindItemPart{{Name: "Disc 1"}, {Name: "Disc 2"}}, "test"); err != nil {
			t.Fatalf("SaveParts: %v", err)
		}
		s.partsState.Store(0)
		partCounts, err = s.CountPartsMany(ctx, []core.ItemRef{trackRef})
		if err != nil || partCounts[trackRef] != 2 {
			t.Fatalf("CountPartsMany(populated) = (%v, %v), want 2", partCounts, err)
		}

		// Container expansion freshness & durable expansion state transitions.
		hasChildren, freshAt, err := s.ChildrenFreshness(ctx, musicLibID, artistRef)
		if err != nil || !hasChildren || freshAt.IsZero() {
			t.Fatalf("ChildrenFreshness(populated) = (%v, %v, %v), want true + non-zero time", hasChildren, freshAt, err)
		}
		cnt, maxFresh, err := s.ChildrenCountAndFreshness(ctx, musicLibID, artistRef)
		if err != nil || cnt != 2 || maxFresh.IsZero() {
			t.Fatalf("ChildrenCountAndFreshness(populated) = (%d, %v, %v), want 2 + non-zero time", cnt, maxFresh, err)
		}
		if err := s.MarkFacetExpansionRunning(ctx, musicPlugin, musicKind, musicLibID, "parent", string(artistRef)); err != nil {
			t.Fatalf("MarkFacetExpansionRunning: %v", err)
		}
		exp, ok, err := s.GetFacetExpansion(ctx, musicPlugin, musicKind, "parent", string(artistRef))
		if err != nil || !ok || exp.State != ExpansionStateRunning {
			t.Fatalf("GetFacetExpansion(running) = (%+v, %v, %v)", exp, ok, err)
		}
		if err := s.FinishFacetExpansion(ctx, musicPlugin, musicKind, musicLibID, "parent", string(artistRef), ExpansionStateDone, 2, ""); err != nil {
			t.Fatalf("FinishFacetExpansion: %v", err)
		}
		exp, ok, err = s.GetFacetExpansion(ctx, musicPlugin, musicKind, "parent", string(artistRef))
		if err != nil || !ok || exp.State != ExpansionStateDone || exp.ItemsAdded != 2 || !exp.IsFresh(s.Now(), time.Hour) {
			t.Fatalf("GetFacetExpansion(done) = (%+v, %v, %v)", exp, ok, err)
		}
	})
}
