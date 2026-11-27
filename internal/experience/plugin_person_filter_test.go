package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// A catalogue (TMDB) person is credited on no plugin item. Asking a plugin
// library for one used to drop the person filter and list the whole library:
// every audiobook appeared on an actor's Jellyfin page and a plugin movie
// library joined their filmography. A plugin person still filters normally.
func TestPluginLibraryAnswersEmptyForCataloguePeople(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 1, AllowedLibraries: []core.LibraryID{9}}
	lib := core.Library{
		ID: 9, Key: "plugin:books:audiobooks", Name: "Audiobooks", Source: core.LibraryPlugin,
		PluginID: "books.plugin", KindKey: "audiobooks",
		Present: mediaBrowserPresentation("books", map[string]string{"audiobook": "AudioBook"}),
	}
	book := core.Title{Key: core.PluginKey(1), Type: core.TypeMovie, MBItemType: "AudioBook", Title: "Some Audiobook"}
	pl := &spyMusicPluginLibraries{
		fakePluginLibraries: fakePluginLibraries{libs: []core.Library{lib}},
		browsePage:          PluginBrowsePage{Items: []core.Title{book}, Total: 1},
	}
	svc := &Service{d: ServiceDeps{
		Active:          func(context.Context, int64) (*core.User, error) { return viewer, nil },
		PluginLibraries: pl,
		Protocol:        "jellyfin",
		LibraryPresentation: func(p core.Presentations, mt core.MediaType) (core.MediaType, string, bool) {
			if p.MediaBrowser != nil {
				return mt, p.MediaBrowser.CollectionType, true
			}
			return mt, "", false
		},
	}}

	actor := frontend.ItemQuery{UnservedTypes: []string{"audiobook"}, Recursive: true, Limit: 10}
	actor.Filter.Person = 1234567 // a TMDB person id
	page, err := svc.LibraryItems(ctx, viewer, core.Device{}, "9", actor)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("a TMDB actor got plugin items: %+v", page)
	}
	if len(pl.browseCalls) != 0 {
		t.Fatalf("plugin library was browsed for a TMDB person: %+v", pl.browseCalls)
	}

	// Plex asks the same through a credit filter.
	plexActor := frontend.ItemQuery{UnservedTypes: []string{"audiobook"}, Recursive: true, Limit: 10}
	plexActor.Filter.Credits = []core.CreditFilter{{People: []int64{1234567}, Kind: "actor"}}
	page, err = svc.LibraryItems(ctx, viewer, core.Device{}, "9", plexActor)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(pl.browseCalls) != 0 {
		t.Fatalf("a TMDB actor's credit filter listed plugin items: page=%+v calls=%+v", page, pl.browseCalls)
	}

	author := actor
	author.Filter.Person = core.PluginPersonID("Some Author")
	page, err = svc.LibraryItems(ctx, viewer, core.Device{}, "9", author)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(pl.browseCalls) != 1 || pl.browseCalls[0].Filter.Person != author.Filter.Person {
		t.Fatalf("a plugin person lost their filter: page=%+v calls=%+v", page, pl.browseCalls)
	}
}
