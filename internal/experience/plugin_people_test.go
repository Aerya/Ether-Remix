package experience

import (
	"context"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type fakePluginPeopleLibraries struct {
	fakePluginLibraries
	items      map[core.RatingKey]core.Title
	searchHits map[string][]core.Title
	people     []PluginPersonMatch
}

func (f *fakePluginPeopleLibraries) RootMediaType(context.Context, core.Library) core.MediaType {
	return core.TypeMovie
}

func (f *fakePluginPeopleLibraries) Item(_ context.Context, key core.RatingKey) (PluginItemView, error) {
	t, ok := f.items[key]
	if !ok {
		return PluginItemView{}, core.ErrNotFound
	}
	return PluginItemView{
		Title:   t,
		Library: f.libs[0],
	}, nil
}

func (f *fakePluginPeopleLibraries) Search(_ context.Context, _ core.Library, query string, _ frontend.ItemQuery, _ int) ([]core.Title, error) {
	return append([]core.Title(nil), f.searchHits[strings.ToLower(strings.TrimSpace(query))]...), nil
}

func (f *fakePluginPeopleLibraries) SearchPeople(_ context.Context, _ core.Library, query string, _ frontend.ItemQuery, _, _ int) ([]PluginPersonMatch, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []PluginPersonMatch
	for _, m := range f.people {
		if q == "" || strings.Contains(strings.ToLower(m.Name), q) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakePluginPeopleLibraries) PersonByID(_ context.Context, _ core.Library, personID int64, _ int) (PluginPersonMatch, bool, error) {
	for _, m := range f.people {
		if m.PersonID == personID {
			return m, true, nil
		}
	}
	return PluginPersonMatch{}, false, nil
}

func (f *fakePluginPeopleLibraries) FindPersonID(_ context.Context, _ core.Library, match func(personID int64, name string) bool) (int64, bool, error) {
	for _, m := range f.people {
		if match(m.PersonID, m.Name) {
			return m.PersonID, true, nil
		}
	}
	return 0, false, nil
}

func TestSearchPluginPeopleAndTitleRanking(t *testing.T) {
	ctx := context.Background()
	personID := core.PluginPersonID("Elena Vance")
	beaconPersonID := core.PluginPersonID("Beacon Mercer")

	titleMatch := core.Title{
		Key:         core.PluginKey(10),
		LibraryID:   5,
		Type:        core.TypeMovie,
		Title:       "Beacon",
		SearchScore: 1.0,
	}
	peopleTitle1 := core.Title{
		Key:       core.PluginKey(11),
		LibraryID: 5,
		Type:      core.TypeMovie,
		Title:     "Northern Drift",
		Cast:      []core.Person{{TMDB: personID, Name: "Elena Vance", ThumbPath: "https://portraits.example.test/elena.png"}},
	}
	peopleTitle2 := core.Title{
		Key:       core.PluginKey(12),
		LibraryID: 5,
		Type:      core.TypeMovie,
		Title:     "Southern Tide",
		Cast: []core.Person{
			{TMDB: personID, Name: "Elena Vance", ThumbPath: "https://portraits.example.test/elena.png"},
			{TMDB: beaconPersonID, Name: "Beacon Mercer"},
		},
	}

	plug := &fakePluginPeopleLibraries{
		fakePluginLibraries: fakePluginLibraries{
			libs: []core.Library{{
				ID:        5,
				Key:       "plugin:community.archive:clip",
				Name:      "Studio Archive",
				Source:    core.LibraryPlugin,
				PluginID:  "community.archive",
				KindKey:   "clip",
				MediaType: core.TypeMovie,
			}},
		},
		items: map[core.RatingKey]core.Title{
			titleMatch.Key:   titleMatch,
			peopleTitle1.Key: peopleTitle1,
			peopleTitle2.Key: peopleTitle2,
		},
		searchHits: map[string][]core.Title{
			"beacon": {titleMatch},
		},
		people: []PluginPersonMatch{
			{
				PersonID:    personID,
				Name:        "Elena Vance",
				CreditCount: 2,
				ArtworkItem: peopleTitle1.Key,
				HasThumb:    true,
				Roles:       map[string]int{"Actor": 2},
				Titles:      []core.Title{peopleTitle1, peopleTitle2},
			},
			{
				PersonID:    beaconPersonID,
				Name:        "Beacon Mercer",
				CreditCount: 1,
				ArtworkItem: peopleTitle2.Key,
				Titles:      []core.Title{peopleTitle2},
			},
		},
	}

	svc, cat := peopleFixture()
	cat.titles = map[core.RatingKey]core.Title{}
	svc.d.PluginLibraries = plug
	svc.d.LibraryPresentation = func(_ core.Presentations, mt core.MediaType) (core.MediaType, string, bool) {
		return mt, "movies", true
	}
	svc.d.Search = Search{
		Visibility: svc.d.Visibility,
		Catalog: func(context.Context, string, []core.MediaType, int) ([]core.Title, error) {
			return nil, nil
		},
	}
	viewer := &core.User{ID: 1}
	device := core.Device{ID: "tv"}

	// 1. Pluggable SearchPluginPeople returns both the matched person and all of their titles.
	peopleMatches, matchedTitles, err := svc.SearchPluginPeople(ctx, viewer, "Elena Vance", frontend.ItemQuery{Limit: 10}, 10)
	if err != nil {
		t.Fatalf("SearchPluginPeople: %v", err)
	}
	if len(peopleMatches) != 1 || peopleMatches[0].Name != "Elena Vance" || peopleMatches[0].TMDB != personID {
		t.Fatalf("unexpected SearchPluginPeople people: %+v", peopleMatches)
	}
	if len(matchedTitles) != 2 {
		t.Fatalf("expected both of Elena Vance's titles from SearchPluginPeople, got %+v", matchedTitles)
	}

	// 2. Experience.Search for cast member name brings up all of that cast member's titles.
	searchPerson, err := svc.Search(ctx, viewer, device, "Elena Vance", frontend.ItemQuery{Limit: 10})
	if err != nil {
		t.Fatalf("Search Elena Vance: %v", err)
	}
	if len(searchPerson.Page.Items) != 2 {
		t.Fatalf("expected 2 titles for Elena Vance, got %+v", searchPerson.Page.Items)
	}

	// 3. Experience.Search for "Beacon" ranks title match ("Beacon") ahead of cast member match ("Southern Tide").
	searchBeacon, err := svc.Search(ctx, viewer, device, "Beacon", frontend.ItemQuery{Limit: 10})
	if err != nil {
		t.Fatalf("Search Beacon: %v", err)
	}
	if len(searchBeacon.Page.Items) != 2 {
		t.Fatalf("expected 2 titles for Beacon (title + cast member match), got %+v", searchBeacon.Page.Items)
	}
	if searchBeacon.Page.Items[0].Title != "Beacon" || searchBeacon.Page.Items[1].Title != "Southern Tide" {
		t.Fatalf("expected title match first, got [%q, %q]", searchBeacon.Page.Items[0].Title, searchBeacon.Page.Items[1].Title)
	}

	// 4. People and Person resolve plugin cast cleanly.
	peopleList, err := svc.People(ctx, viewer, device, frontend.PeopleQuery{Query: "Elena", Limit: 10})
	if err != nil || len(peopleList) != 1 || peopleList[0].Name != "Elena Vance" {
		t.Fatalf("People(Elena) = (%+v, %v)", peopleList, err)
	}
	detail, err := svc.Person(ctx, viewer, device, peopleList[0].ID, frontend.PersonReadOptions{})
	if err != nil || detail.Name != "Elena Vance" || detail.TMDB != personID {
		t.Fatalf("Person(%s) = (%+v, %v)", peopleList[0].ID, detail, err)
	}
}
