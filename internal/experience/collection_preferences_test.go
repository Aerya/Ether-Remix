package experience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type completeCollections struct {
	core.Collections
	lists map[core.MediaType][]core.Collection
	items map[core.RatingKey][]core.Title
	err   error
}

func (completeCollections) Get(context.Context, *core.User, core.RatingKey) (*core.Collection, error) {
	return nil, core.ErrNotFound
}
func (f completeCollections) ListComplete(_ context.Context, _ *core.User, typ core.MediaType) ([]core.Collection, error) {
	return f.lists[typ], f.err
}
func (f completeCollections) Items(_ context.Context, _ *core.User, k core.RatingKey, offset, limit int) (core.Page, error) {
	items := f.items[k]
	if offset >= len(items) {
		return core.Page{Total: len(items)}, nil
	}
	return core.Page{Items: items[offset:min(offset+limit, len(items))], Total: len(items)}, nil
}
func TestCollectionsFilterBeforePaginationAndCount(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 1, AllowedLibraries: []core.LibraryID{1}}
	a, b, c := core.CollectionKey(1), core.CollectionKey(2), core.CollectionKey(3)
	hidden := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Adult: true}
	allowed := core.Title{Key: core.MovieKey(2), Type: core.TypeMovie}
	src := completeCollections{lists: map[core.MediaType][]core.Collection{core.TypeMovie: {{Key: a, Type: core.TypeMovie}, {Key: b, Type: core.TypeMovie}, {Key: c, Type: core.TypeMovie}}, core.TypeShow: {{Key: core.SmartKey(4), Type: core.TypeShow}}}, items: map[core.RatingKey][]core.Title{a: {hidden}, b: {hidden, allowed}, c: {allowed}}}
	s := &Service{d: ServiceDeps{Active: func(_ context.Context, id int64) (*core.User, error) {
		if id != 1 {
			return nil, core.ErrUnauthorized
		}
		return viewer, nil
	}, Libraries: []Library{{ID: 1, Type: core.TypeMovie}, {ID: 2, Type: core.TypeShow}}, Visibility: Visibility{SectionFor: func(typ core.MediaType) (int, bool) {
		if typ == core.TypeShow {
			return 2, true
		}
		return 1, true
	}}, Collections: src}}
	page, err := s.Collections(ctx, viewer, core.Device{}, frontend.CollectionQuery{Limit: 1})
	if err != nil || page.Total != 2 || len(page.Items) != 1 || page.Items[0].Key != b || page.Items[0].ChildCount != 1 {
		t.Fatalf("filtered page %+v %v", page, err)
	}
	page, err = s.Collections(ctx, viewer, core.Device{}, frontend.CollectionQuery{Offset: 1, Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Key != c {
		t.Fatalf("second page %+v %v", page, err)
	}
	src.err = errors.New("incomplete")
	s.d.Collections = src
	if _, err = s.Collections(ctx, viewer, core.Device{}, frontend.CollectionQuery{}); err == nil {
		t.Fatal("partial collection list accepted")
	}
	if _, err = s.Collections(ctx, &core.User{ID: 2}, core.Device{}, frontend.CollectionQuery{}); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("inactive viewer", err)
	}
}
func TestNotInterestedPagingAndSingleEntry(t *testing.T) {
	ctx := context.Background()
	v := &core.User{ID: 1}
	a := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie}
	b := core.Title{Key: core.MovieKey(2), Type: core.TypeMovie, Adult: true}
	added := time.Unix(123, 0)
	s := &Service{d: ServiceDeps{Active: func(context.Context, int64) (*core.User, error) { return v, nil }, Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }}, Get: func(_ context.Context, _ *core.User, k core.RatingKey) (*core.Title, error) {
		switch k {
		case a.Key:
			return &a, nil
		case b.Key:
			return &b, nil
		}
		return nil, core.ErrNotFound
	}, NotInterestedSource: func(context.Context, *core.User) ([]frontend.NotInterestedEntry, error) {
		return []frontend.NotInterestedEntry{{Key: b.Key, AddedAt: added}, {Key: a.Key, AddedAt: added}}, nil
	}}}
	page, err := s.NotInterested(ctx, v, core.Device{}, frontend.PageQuery{Limit: 1})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Key != a.Key || !page.Items[0].AddedAt.Equal(added) {
		t.Fatal(page, err)
	}
	item, err := s.NotInterestedItem(ctx, v, core.Device{}, a.Key)
	if err != nil || !item.AddedAt.Equal(added) {
		t.Fatal(item, err)
	}
	if _, err = s.NotInterestedItem(ctx, v, core.Device{}, b.Key); !errors.Is(err, core.ErrNotFound) {
		t.Fatal("hidden item", err)
	}
}

func TestListingPageBoundsPreserveAuthorizedTotals(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 1}
	movie := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie}
	var collections []core.Collection
	members := map[core.RatingKey][]core.Title{}
	var exclusions []frontend.NotInterestedEntry
	for i := 1; i <= 610; i++ {
		key := core.CollectionKey(int64(i))
		collections = append(collections, core.Collection{Key: key, Type: core.TypeMovie})
		members[key] = []core.Title{movie}
		exclusions = append(exclusions, frontend.NotInterestedEntry{Key: core.MovieKey(int64(i))})
	}
	src := completeCollections{lists: map[core.MediaType][]core.Collection{core.TypeMovie: collections}, items: members}
	s := &Service{d: ServiceDeps{Active: func(context.Context, int64) (*core.User, error) { return viewer, nil }, Libraries: []Library{{ID: 1, Type: core.TypeMovie}}, Collections: src, Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }}, Get: func(_ context.Context, _ *core.User, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k, Type: core.TypeMovie}, nil
	}, NotInterestedSource: func(context.Context, *core.User) ([]frontend.NotInterestedEntry, error) { return exclusions, nil }}}
	for _, tc := range []struct{ offset, limit, want int }{{0, 0, 50}, {0, -1, 50}, {0, 7, 7}, {0, 500, 500}, {0, 1000, 500}, {600, 500, 10}, {610, 500, 0}, {0, int(^uint(0) >> 1), 500}} {
		c, err := s.Collections(ctx, viewer, core.Device{}, frontend.CollectionQuery{Offset: tc.offset, Limit: tc.limit})
		if err != nil || c.Total != 610 || len(c.Items) != tc.want {
			t.Fatalf("collections %+v => total=%d len=%d err=%v", tc, c.Total, len(c.Items), err)
		}
		n, err := s.NotInterested(ctx, viewer, core.Device{}, frontend.PageQuery{Offset: tc.offset, Limit: tc.limit})
		if err != nil || n.Total != 610 || len(n.Items) != tc.want {
			t.Fatalf("not interested %+v => total=%d len=%d err=%v", tc, n.Total, len(n.Items), err)
		}
	}
}

// existenceCollections deliberately rejects full listings and a second member
// page, proving home/view discovery does neither after finding one member.
type existenceCollections struct {
	core.Collections
	visits, reads *int
}

func (f existenceCollections) ListComplete(context.Context, *core.User, core.MediaType) ([]core.Collection, error) {
	return nil, errors.New("existence must not list complete collections")
}
func (f existenceCollections) FindVisible(_ context.Context, _ *core.User, typ core.MediaType, accept func(core.Collection) (bool, error)) (bool, error) {
	for n := int64(1); n <= 1371; n++ {
		*f.visits++
		ok, err := accept(core.Collection{Key: core.CollectionKey(n), Type: typ})
		if ok || err != nil {
			return ok, err
		}
	}
	return false, nil
}
func (f existenceCollections) Items(_ context.Context, _ *core.User, _ core.RatingKey, offset, limit int) (core.Page, error) {
	*f.reads++
	if offset != 0 || limit > 32 {
		return core.Page{}, errors.New("unbounded existence read")
	}
	return core.Page{Items: []core.Title{{Key: core.MovieKey(1), Type: core.TypeMovie}}, Total: 10000}, nil
}
func TestCollectionsExistenceStopsEarly(t *testing.T) {
	v := &core.User{ID: 1, IsOwner: true}
	visits, reads := 0, 0
	s := &Service{d: ServiceDeps{
		Active:      func(context.Context, int64) (*core.User, error) { return v, nil },
		Libraries:   []Library{{ID: 1, Type: core.TypeMovie}},
		Visibility:  Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }},
		Collections: existenceCollections{visits: &visits, reads: &reads},
	}}
	page, err := s.Collections(t.Context(), v, core.Device{}, frontend.CollectionQuery{ExistsOnly: true, Limit: 1})
	if err != nil || page.Total != 1 || visits != 1 || reads != 1 || len(page.Items) != 0 {
		t.Fatalf("page %+v err %v visits %d reads %d", page, err, visits, reads)
	}
}

type navigationSource struct{ completeCollections }

func (f navigationSource) ListNavigation(ctx context.Context, u *core.User, typ core.MediaType) ([]core.Collection, error) {
	return f.completeCollections.ListComplete(ctx, u, typ)
}
func (f navigationSource) ListComplete(context.Context, *core.User, core.MediaType) ([]core.Collection, error) {
	panic("navigation must not enumerate all collections")
}
func TestNavigationFiltersBeforePagination(t *testing.T) {
	viewer := &core.User{ID: 1, AllowedLibraries: []core.LibraryID{1}}
	hidden := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Adult: true}
	allowed := core.Title{Key: core.MovieKey(2), Type: core.TypeMovie}
	a, b, c := core.SmartKey(1), core.SmartKey(2), core.SmartKey(3)
	src := navigationSource{completeCollections{lists: map[core.MediaType][]core.Collection{core.TypeMovie: {
		{Key: a, Type: core.TypeMovie, NavigationGroup: "studios", NavigationOrder: 0},
		{Key: b, Type: core.TypeMovie, NavigationGroup: "genres", NavigationOrder: 2},
		{Key: c, Type: core.TypeMovie, NavigationGroup: "studios", NavigationOrder: 1},
	}}, items: map[core.RatingKey][]core.Title{a: {hidden}, b: {allowed}, c: {allowed}}}}
	s := &Service{d: ServiceDeps{Active: func(context.Context, int64) (*core.User, error) { return viewer, nil }, Libraries: []Library{{ID: 1, Type: core.TypeMovie}}, Collections: src, Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }}}}
	page, err := s.Collections(context.Background(), viewer, core.Device{}, frontend.CollectionQuery{NavigationOnly: true, NavigationGroup: "studios", Limit: 1})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Key != c {
		t.Fatal(page, err)
	}
}

type pageFirstCollectionsFake struct {
	core.Collections
	owned  map[core.MediaType][]core.Collection
	items  map[core.RatingKey][]core.Title
	hasFr  bool
	called string
}

func (f *pageFirstCollectionsFake) ListComplete(context.Context, *core.User, core.MediaType) ([]core.Collection, error) {
	f.called = "ListComplete"
	return nil, errors.New("page-first collection listing must not call ListComplete")
}
func (f *pageFirstCollectionsFake) ListOwnedComplete(_ context.Context, _ *core.User, typ core.MediaType) ([]core.Collection, bool, error) {
	return f.owned[typ], f.hasFr && typ == core.TypeMovie, nil
}
func (f *pageFirstCollectionsFake) Items(_ context.Context, _ *core.User, k core.RatingKey, offset, limit int) (core.Page, error) {
	items := f.items[k]
	if offset >= len(items) {
		return core.Page{Total: len(items)}, nil
	}
	return core.Page{Items: items[offset:min(offset+limit, len(items))], Total: len(items)}, nil
}

type stubFranchiseStore struct {
	franchises []core.Collection
	visible    map[int]int
	pageCalls  int
	curCalls   int
}

func (f *stubFranchiseStore) FranchiseTotal(context.Context) (int, error) {
	return len(f.franchises), nil
}
func (f *stubFranchiseStore) FranchisesPage(_ context.Context, offset, limit int) ([]core.Collection, error) {
	f.pageCalls++
	if offset >= len(f.franchises) {
		return nil, nil
	}
	return f.franchises[offset:min(offset+limit, len(f.franchises))], nil
}
func (f *stubFranchiseStore) FranchisesCursor(_ context.Context, afterCount, afterID, limit int) ([]core.Collection, error) {
	f.curCalls++
	var out []core.Collection
	for _, c := range f.franchises {
		id := int(c.Key.TMDB())
		if afterCount > 0 && (c.ChildCount > afterCount || (c.ChildCount == afterCount && id <= afterID)) {
			continue
		}
		out = append(out, c)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
func (f *stubFranchiseStore) FranchisesSearch(ctx context.Context, _ string, afterCount, afterID, limit int) ([]core.Collection, error) {
	return f.FranchisesCursor(ctx, afterCount, afterID, limit)
}
func (f *stubFranchiseStore) FranchiseVisibleCounts(_ context.Context, _ core.Filter, ids []int) (map[int]int, error) {
	out := make(map[int]int, len(ids))
	for _, id := range ids {
		if n := f.visible[id]; n > 0 {
			out[id] = n
		}
	}
	return out, nil
}
func (f *stubFranchiseStore) FranchiseVisibleKeys(_ context.Context, _ core.Filter, ids []int) (map[int][]core.RatingKey, error) {
	out := make(map[int][]core.RatingKey, len(ids))
	for _, id := range ids {
		for n := 0; n < f.visible[id]; n++ {
			out[id] = append(out[id], core.MovieKey(int64(id*100+n)))
		}
	}
	return out, nil
}
func (f *stubFranchiseStore) FranchisesInSmallFilter(context.Context, core.Filter, string, int, int) ([]core.Collection, bool, bool, error) {
	return nil, false, false, nil
}
func (f *stubFranchiseStore) FranchiseSummaryBatch(_ context.Context, ids []int) (map[int]core.Collection, error) {
	out := make(map[int]core.Collection, len(ids))
	for _, c := range f.franchises {
		out[int(c.Key.TMDB())] = c
	}
	return out, nil
}
func (f *stubFranchiseStore) FranchiseFallbackArt(context.Context, core.Filter, []int) (map[int][2]string, error) {
	return nil, nil
}
func (f *stubFranchiseStore) HasActiveViewerRules(int64) bool { return false }

func TestCollectionsPageFirstUsesOwnedAndFranchiseStore(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 1, IsOwner: true}
	ownedKey := core.SmartKey(1)
	cols := &pageFirstCollectionsFake{
		owned: map[core.MediaType][]core.Collection{
			core.TypeMovie: {{Key: ownedKey, Title: "Spotlight", Type: core.TypeMovie}},
		},
		items: map[core.RatingKey][]core.Title{
			ownedKey: {{Key: core.MovieKey(1), Type: core.TypeMovie}},
		},
		hasFr: true,
	}
	frStore := &stubFranchiseStore{
		franchises: []core.Collection{
			{Key: core.CollectionKey(10), Title: "Franchise A", Type: core.TypeMovie, ChildCount: 4},
			{Key: core.CollectionKey(20), Title: "Franchise B", Type: core.TypeMovie, ChildCount: 3},
			{Key: core.CollectionKey(30), Title: "Franchise C", Type: core.TypeMovie, ChildCount: 2},
		},
		visible: map[int]int{10: 4, 20: 0, 30: 2}, // Franchise 20 has no visible released titles
	}
	s := &Service{d: ServiceDeps{
		Active:      func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Libraries:   []Library{{ID: 1, Type: core.TypeMovie}},
		Visibility:  Visibility{SectionFor: func(core.MediaType) (int, bool) { return 1, true }},
		Collections: cols,
		Franchises:  frStore,
	}}

	// Vela library collections path (ViewID set, NoTotal=true): owned comes first,
	// Franchise 20 (0 visible) is skipped, Franchise 30 fills the page.
	page, err := s.Collections(ctx, viewer, core.Device{}, frontend.CollectionQuery{
		ViewID:  "1",
		NoTotal: true,
		Types:   []frontend.MediaType{core.TypeMovie},
		Offset:  0,
		Limit:   3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cols.called != "" {
		t.Fatalf("unexpected fallback call %q", cols.called)
	}
	if len(page.Items) != 3 || page.Items[0].Key != ownedKey || page.Items[1].Key != core.CollectionKey(10) || page.Items[2].Key != core.CollectionKey(30) {
		t.Fatalf("unexpected page items: %+v", page.Items)
	}

	// Jellyfin BoxSets path (no ViewID): offset 1 skips owned and returns franchises.
	boxsets, err := s.Collections(ctx, viewer, core.Device{}, frontend.CollectionQuery{Offset: 1, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(boxsets.Items) != 2 || boxsets.Items[0].Key != core.CollectionKey(10) || boxsets.Items[1].Key != core.CollectionKey(30) {
		t.Fatalf("unexpected boxsets page: %+v", boxsets.Items)
	}

	// With availability gating (GatePolicy + HiddenMovieSeq), page 1 stays on
	// Fast Path 1 (FranchisesPage) and past-the-end offsets short-circuit in
	// O(1) via FranchiseTotal without scanning pages or cursors.
	s.d.PageExclusions = func(context.Context, *core.User, core.MediaType) LibraryPageExclusions {
		return LibraryPageExclusions{
			AvailabilityInSQL: true,
			Availability:      core.Filter{GatePolicy: "release-delay", HiddenMovieSeq: 7},
		}
	}
	frStore.pageCalls, frStore.curCalls = 0, 0
	p1, err := s.Collections(ctx, viewer, core.Device{}, frontend.CollectionQuery{Offset: 1, Limit: 1})
	if err != nil || len(p1.Items) != 1 || p1.Items[0].Key != core.CollectionKey(10) || frStore.pageCalls != 1 || frStore.curCalls != 0 {
		t.Fatalf("gated page 1 did not stay on Fast Path 1: page=%+v pageCalls=%d curCalls=%d err=%v", p1, frStore.pageCalls, frStore.curCalls, err)
	}
	frStore.pageCalls, frStore.curCalls = 0, 0
	deep, err := s.Collections(ctx, viewer, core.Device{}, frontend.CollectionQuery{Offset: 24000, Limit: 50})
	if err != nil || len(deep.Items) != 0 || frStore.pageCalls != 0 || frStore.curCalls != 0 {
		t.Fatalf("deep gated offset walked franchises: page=%+v pageCalls=%d curCalls=%d err=%v", deep, frStore.pageCalls, frStore.curCalls, err)
	}
	// Restricted viewer (Fast Path 3) also short-circuits past-the-end offsets.
	restricted := &core.User{ID: 2, MaxContentRating: "PG-13"}
	s.d.Active = func(context.Context, int64) (*core.User, error) { return restricted, nil }
	deepRestricted, err := s.Collections(ctx, restricted, core.Device{}, frontend.CollectionQuery{Offset: 24000, Limit: 50})
	if err != nil || len(deepRestricted.Items) != 0 || frStore.pageCalls != 0 || frStore.curCalls != 0 {
		t.Fatalf("deep restricted offset walked franchises: page=%+v pageCalls=%d curCalls=%d err=%v", deepRestricted, frStore.pageCalls, frStore.curCalls, err)
	}
}

type smartArtworkCollections struct {
	core.Collections
	byKey     map[core.RatingKey]core.Collection
	itemsCall int
}

func (f *smartArtworkCollections) Get(_ context.Context, _ *core.User, k core.RatingKey) (*core.Collection, error) {
	c, ok := f.byKey[k]
	if !ok {
		return nil, core.ErrNotFound
	}
	return &c, nil
}

func (f *smartArtworkCollections) Items(_ context.Context, _ *core.User, _ core.RatingKey, _, _ int) (core.Page, error) {
	f.itemsCall++
	return core.Page{}, nil
}

func TestPageArtworkSmartCollectionsAvoidsLoadingMembers(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 1}
	k1 := core.SmartKey(11)
	k2 := core.SmartKey(12)
	emptyKey := core.SmartKey(13)
	cols := &smartArtworkCollections{
		byKey: map[core.RatingKey]core.Collection{
			k1:       {Key: k1, Type: core.TypeMovie, Title: "Smart Movies", PosterPath: "/p1.jpg"},
			k2:       {Key: k2, Type: core.TypeShow, Title: "Smart Shows", PosterPath: "/p2.jpg"},
			emptyKey: {Key: emptyKey, Type: core.TypeMovie, Title: "Empty"},
		},
	}
	s := &Service{d: ServiceDeps{
		Active: func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Visibility: Visibility{SectionFor: func(typ core.MediaType) (int, bool) {
			if typ == core.TypeShow {
				return 2, true
			}
			return 1, true
		}},
		Collections: cols,
		Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
			switch q.Filter.CollectionMembers {
			case k1.TMDB(), k2.TMDB():
				return core.Page{Total: 1}, nil
			default:
				return core.Page{Total: 0}, nil
			}
		},
	}}
	got, err := s.PageArtworkSmartCollections(ctx, viewer, []core.RatingKey{k1, k2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[k1].PosterPath != "/p1.jpg" || got[k2].PosterPath != "/p2.jpg" {
		t.Fatalf("smart page = %+v", got)
	}
	if cols.itemsCall != 0 {
		t.Fatalf("expected 0 Collections.Items member loads for visible smart collections, got %d", cols.itemsCall)
	}
	// Empty collection falls back to Collections.Items and is omitted when still empty.
	got, err = s.PageArtworkSmartCollections(ctx, viewer, []core.RatingKey{emptyKey})
	if err != nil || len(got) != 0 || cols.itemsCall == 0 {
		t.Fatalf("empty smart collection = %+v err=%v itemsCall=%d", got, err, cols.itemsCall)
	}
}
