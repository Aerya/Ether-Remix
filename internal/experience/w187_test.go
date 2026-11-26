package experience

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestPluginContinueTitlesLocalized(t *testing.T) {
	cases := []struct {
		media core.MediaClass
		lang  string
		want  string
	}{
		{core.MediaClassVideo, "", "Continue Watching"},
		{core.MediaClassVideo, "en-US", "Continue Watching"},
		{core.MediaClassVideo, "fr-FR,fr;q=0.9,en;q=0.8", "Continuer à regarder"},
		{core.MediaClassBook, "en", "Continue Reading"},
		{core.MediaClassBook, "es-MX,es;q=0.9", "Seguir leyendo"},
		{core.MediaClassBook, "de;q=0.5, fr-CA;q=0.9", "Continuer à lire"},
		{core.MediaClassAudiobook, "en", "Continue Listening"},
		{core.MediaClassPodcast, "pt_BR", "Continuar ouvindo"},
		{core.MediaClassAudiobook, "zh-Hant-TW", "繼續收聽"},
		{core.MediaClassAudiobook, "zh-Hans-CN", "继续收听"},
		{core.MediaClassPodcast, "ja", "再生を再開"},
		{core.MediaClassBook, "xx-ZZ", "Continue Reading"},
	}
	for _, tc := range cases {
		if got := pluginContinueTitle(tc.media, tc.lang); got != tc.want {
			t.Errorf("pluginContinueTitle(%q, %q) = %q, want %q", tc.media, tc.lang, got, tc.want)
		}
	}
}

type w187InProgressState struct {
	serviceState
	inProgress []core.WatchStatus
}

func (s w187InProgressState) InProgress(context.Context, int64, int) ([]core.WatchStatus, error) {
	return append([]core.WatchStatus(nil), s.inProgress...), nil
}

func (s w187InProgressState) GetMany(_ context.Context, _ int64, keys []core.RatingKey) (map[core.RatingKey]core.WatchStatus, error) {
	out := make(map[core.RatingKey]core.WatchStatus, len(s.inProgress))
	for _, ws := range s.inProgress {
		out[ws.Key] = ws
	}
	return out, nil
}

type w187FakePluginLibs struct {
	fakePluginLibraries
	items        map[core.RatingKey]PluginItemView
	browse       []core.Title
	pres         map[core.LibraryID]PluginPresentation
	media        core.MediaClass
	skipContinue map[core.RatingKey]bool
	lastBrowseQ  frontend.ItemQuery
	lastLevel    string
}

func (f *w187FakePluginLibs) Presentation(_ context.Context, lib core.Library) (PluginPresentation, bool) {
	p, ok := f.pres[lib.ID]
	if !ok {
		return PluginPresentation{}, false
	}
	p.Media = f.media
	return p, true
}

func (f *w187FakePluginLibs) ItemsMany(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]PluginItemView, error) {
	out := make(map[core.RatingKey]PluginItemView, len(keys))
	for _, k := range keys {
		if piv, ok := f.items[k]; ok {
			if f.skipContinue[k] {
				piv.SkipContinue = true
			}
			out[k] = piv
		}
	}
	return out, nil
}

func (f *w187FakePluginLibs) Browse(_ context.Context, _ core.Library, q frontend.ItemQuery, _ string, _ int) (PluginBrowsePage, error) {
	f.lastBrowseQ = q
	return PluginBrowsePage{Items: append([]core.Title(nil), f.browse...), Total: len(f.browse)}, nil
}

func (f *w187FakePluginLibs) FacetBuckets(_ context.Context, _ core.Library, _ string) ([]frontend.FacetBucket, error) {
	return nil, nil
}

func (f *w187FakePluginLibs) FacetCollections(_ context.Context, _ core.Library, _, _, _ string, _, _ int) (frontend.FacetCollectionPage, error) {
	return frontend.FacetCollectionPage{}, nil
}

func (f *w187FakePluginLibs) FacetPeople(_ context.Context, _ core.Library, _, _, _ string, _, _ int) (PluginFacetPeoplePage, error) {
	return PluginFacetPeoplePage{}, nil
}

func (f *w187FakePluginLibs) FacetsAtLevel(_ context.Context, _ core.Library, level, _ string) ([]core.FacetValue, error) {
	f.lastLevel = level
	return []core.FacetValue{{ID: "g1", Key: "Jazz", Title: "Jazz", Count: 3}}, nil
}

func (f *w187FakePluginLibs) FacetBucketsAtLevel(_ context.Context, _ core.Library, level, _ string) ([]frontend.FacetBucket, error) {
	f.lastLevel = level
	return []frontend.FacetBucket{{ID: "Jazz", Label: "Jazz", Count: 3}}, nil
}

func (f *w187FakePluginLibs) FacetCollectionsAtLevel(_ context.Context, _ core.Library, level, _, _, _ string, _, _ int) (frontend.FacetCollectionPage, error) {
	f.lastLevel = level
	return frontend.FacetCollectionPage{Items: []frontend.FacetCollection{{ID: "Jazz", Name: "Jazz", ItemCount: 3}}}, nil
}

func (f *w187FakePluginLibs) FacetPeopleAtLevel(_ context.Context, _ core.Library, level, _, _, _ string, _, _ int) (PluginFacetPeoplePage, error) {
	f.lastLevel = level
	return PluginFacetPeoplePage{Items: []PluginFacetPerson{{PersonID: core.PluginPersonID("Miles Davis"), Name: "Miles Davis", Count: 2}}}, nil
}

func TestPluginContinueExclusionLocalizationAndLevelDelegation(t *testing.T) {
	ctx := context.Background()
	owner := &core.Viewer{ID: 1, IsOwner: true}
	lib := core.Library{
		ID: 55, Key: "lib55", Name: "Library 55",
		Source: core.LibraryPlugin, PluginID: "p.test", KindKey: "items",
		Present: core.Presentations{
			Vela: &core.VelaPresentation{Type: "movies"},
		},
	}
	item1 := core.Title{Key: core.PluginKey(101), LibraryID: 55, Type: core.TypeMovie, Title: "Item 1"}
	item2 := core.Title{Key: core.PluginKey(102), LibraryID: 55, Type: core.TypeMovie, Title: "Item 2"}

	var histTF core.TabFilter
	if err := json.Unmarshal([]byte(`{"match":"all","rules":[{"field":"viewer","op":"is","value":{"history":true}}]}`), &histTF); err != nil {
		t.Fatal(err)
	}

	fake := &w187FakePluginLibs{
		fakePluginLibraries: fakePluginLibraries{
			libs: []core.Library{lib},
		},
		items: map[core.RatingKey]PluginItemView{
			item1.Key: {Library: lib, Title: item1, Playable: true},
			item2.Key: {Library: lib, Title: item2, Playable: true},
		},
		browse: []core.Title{item1, item2},
		pres: map[core.LibraryID]PluginPresentation{
			55: {
				Facets: []frontend.ViewFacet{
					{ID: "genre", Label: "Genre", Type: "strings", Multi: true},
					{ID: "composer", Label: "Composer", Type: "people", Multi: true},
				},
				Shelves: []PluginShelf{
					{Key: "recent-albums", Label: "Jump back in", Level: "album", Sort: core.SortLastViewed, Direction: "desc", TabFilter: &histTF},
				},
			},
		},
		skipContinue: map[core.RatingKey]bool{},
	}

	svc, err := NewService(ServiceDeps{
		Active: func(_ context.Context, id int64) (*core.User, error) {
			if id == 1 {
				return &core.User{ID: 1, IsOwner: true}, nil
			}
			return nil, core.ErrNotFound
		},
		Get:      func(context.Context, *core.User, core.RatingKey) (*core.Title, error) { return nil, core.ErrNotFound },
		Children: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) { return nil, nil },
		Visibility: Visibility{
			GetTitle:   func(context.Context, core.RatingKey) (*core.Title, error) { return nil, core.ErrNotFound },
			SectionFor: func(core.MediaType) (int, bool) { return 1, true },
		},
		Search:          Search{Catalog: func(context.Context, string, []core.MediaType, int) ([]core.Title, error) { return nil, nil }},
		PluginLibraries: fake,
		State: w187InProgressState{inProgress: []core.WatchStatus{
			{Key: item1.Key, ViewOffset: 60_000},
			{Key: item2.Key, ViewOffset: 90_000},
		}},
		Rows:     &RowsService{},
		Protocol: "vela",
		LibraryPresentation: func(p core.Presentations, fallback core.MediaType) (core.MediaType, string, bool) {
			if p.Vela == nil {
				return "", "", false
			}
			return core.TypeMovie, "movies", true
		},
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// 1. Music media class excludes Continue shelf completely, while still
	// evaluating declared shelves with Level + TabFilter + SortLastViewed.
	fake.media = core.MediaClassMusic
	rows, err := svc.PluginLibraryRows(ctx, owner, core.Device{Language: "fr"}, "55", "")
	if err != nil {
		t.Fatalf("PluginLibraryRows(music): %v", err)
	}
	for _, r := range rows {
		if r.Purpose == frontend.RowContinue {
			t.Fatalf("music library must not include Continue row, got %+v", r)
		}
	}
	if fake.lastBrowseQ.Level != "album" || fake.lastBrowseQ.Sort != core.SortLastViewed || len(fake.lastBrowseQ.Filter.TabRules) == 0 {
		t.Fatalf("shelf browse query = %+v, want Level=album Sort=lastViewed TabRules!=nil", fake.lastBrowseQ)
	}

	// 2. Book media class shows localized "Continue Reading", while filtering
	// out individual track-role items marked SkipContinue.
	fake.media = core.MediaClassBook
	fake.skipContinue[item2.Key] = true
	rows, err = svc.PluginLibraryRows(ctx, owner, core.Device{Language: "fr-FR"}, "55", "")
	if err != nil {
		t.Fatalf("PluginLibraryRows(book): %v", err)
	}
	if len(rows) == 0 || rows[0].Purpose != frontend.RowContinue {
		t.Fatalf("expected Continue row first for book library, got %+v", rows)
	}
	if rows[0].Title != "Continuer à lire" {
		t.Fatalf("book continue title = %q, want %q", rows[0].Title, "Continuer à lire")
	}
	if len(rows[0].Items) != 1 || rows[0].Items[0].Key != item1.Key {
		t.Fatalf("SkipContinue item must be filtered out, got %+v", rows[0].Items)
	}

	// 3. Audiobook media class shows localized "Continue Listening".
	fake.media = core.MediaClassAudiobook
	rows, err = svc.PluginLibraryRows(ctx, owner, core.Device{Language: "es"}, "55", "")
	if err != nil {
		t.Fatalf("PluginLibraryRows(audiobook): %v", err)
	}
	if rows[0].Title != "Seguir escuchando" {
		t.Fatalf("audiobook continue title = %q, want %q", rows[0].Title, "Seguir escuchando")
	}

	// 4. Level-scoped facet methods delegate level through PluginLevelFacetLibraries.
	if _, err := svc.LibraryFacetBucketsAtLevel(ctx, owner, core.Device{}, "55", "album", "genre"); err != nil || fake.lastLevel != "album" {
		t.Fatalf("LibraryFacetBucketsAtLevel: level=%q err=%v", fake.lastLevel, err)
	}
	if _, err := svc.LibraryFacetCollections(ctx, owner, core.Device{}, "55", "genre", "count", frontend.PageQuery{Level: "track", Limit: 10}); err != nil || fake.lastLevel != "track" {
		t.Fatalf("LibraryFacetCollections(level): level=%q err=%v", fake.lastLevel, err)
	}
	if _, err := svc.LibraryFacetPeople(ctx, owner, core.Device{}, "55", "composer", "name", frontend.PageQuery{Level: "track", Limit: 10}); err != nil || fake.lastLevel != "track" {
		t.Fatalf("LibraryFacetPeople(level): level=%q err=%v", fake.lastLevel, err)
	}
	if _, err := svc.FacetsAtLevel(ctx, owner, core.Device{}, "55", "album", "genre"); err != nil || fake.lastLevel != "album" {
		t.Fatalf("FacetsAtLevel: level=%q err=%v", fake.lastLevel, err)
	}
}
