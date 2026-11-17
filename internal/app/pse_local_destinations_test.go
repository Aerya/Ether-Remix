package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/localmedia"
	"github.com/braedonsaunders/ether/internal/pse/themes"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestLocalDestinationsPersistAndKeepPrivateVideos(t *testing.T) {
	ctx := core.WithCatalogViewer(t.Context(), &core.User{ID: 1})
	db := store.OpenTest(t)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cols, err := collections.New(db, cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, catalog: cat, collections: cols}
	rt := pseRuntimeOf(a)
	rt.thOnce.Do(func() { rt.themes, err = themes.New(ctx, db, cols) })
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "Private holiday.mp4"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	rt.local = &localmedia.Library{DB: db, Publish: a.publishLocalMedia, Probe: func(context.Context, *os.File, string) (localmedia.File, error) {
		return localmedia.File{Duration: 60}, nil
	}}
	if err = rt.local.Add(ctx, localmedia.Root{Name: "Family", Path: dir, Kind: "video"}); err != nil {
		t.Fatal(err)
	}
	if err = rt.local.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	roots, _ := rt.local.Roots(ctx)
	root := roots[0]
	original := rt.themes.Libraries()[0]
	target, err := cols.CreateSpec(ctx, core.CollectionSpec{Name: "Cartoons", MediaType: "movie", Sources: []core.CollectionSource{{Kind: "titles", Keys: []string{core.MovieKey(999).String()}}}, Enabled: true, Refresh: "live", Sort: "title"})
	if err != nil {
		t.Fatal(err)
	}
	edit := func(kind, id string, linked bool) {
		t.Helper()
		if err := rt.local.Configure(ctx, root.ID, func(r localmedia.Root) error {
			return a.editLocalDestination(ctx, r, localDestinationEdit{root.ID, kind, id, linked})
		}); err != nil {
			t.Fatal(err)
		}
	}
	edit("collection", target.ID, true)
	edit("collection", target.ID, true)
	keys := cols.PSEKeys(ctx, target.ID, &core.User{ID: 1})
	if len(keys) != 1 || keys[0].Kind() != core.KindLocal {
		t.Fatalf("unmatched video missing: %v", keys)
	}
	denied := core.WithCatalogViewer(t.Context(), &core.User{ID: 2})
	if keys := cols.PSEKeys(denied, target.ID, &core.User{ID: 2}); len(keys) != 0 {
		t.Fatalf("private video leaked: %v", keys)
	}
	// A restricted first request must not poison the shared build for the owner.
	fresh, err := cols.CreateSpec(denied, core.CollectionSpec{Name: "Restricted first", MediaType: "mixed", Sources: []core.CollectionSource{{Kind: "localFiles", Label: root.ID}}, Enabled: true, Refresh: "live", Sort: "title", LocalOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := cols.PSEKeys(denied, fresh.ID, &core.User{ID: 2}); len(got) != 0 {
		t.Fatalf("restricted first leaked: %v", got)
	}
	if got := cols.PSEKeys(ctx, fresh.ID, &core.User{ID: 1}); len(got) != 1 {
		t.Fatalf("restricted viewer poisoned shared build: %v", got)
	}
	if err := cols.DeleteSpec(ctx, fresh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.localDestinations(denied, root.ID); err == nil {
		t.Fatal("foreign root editable")
	}
	dest, err := rt.themes.Create(ctx, themes.Library{Name: "Animation", Type: themes.TypeMovie, Collections: []string{target.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for _, th := range rt.themes.Themes() {
		if th.Title == "Animation" && th.VisibleTo != nil && !th.VisibleTo(&core.User{ID: 2}) {
			t.Fatal("private folder unexpectedly hid the whole library")
		}
	}
	edit("library", strconv.FormatInt(dest.ID, 10), true)
	edit("library", strconv.FormatInt(original.ID, 10), false)
	if err := rt.local.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if libs := rt.themes.Libraries(); len(libs) != 1 || libs[0].ID != dest.ID {
		t.Fatalf("dedicated library recreated: %+v", libs)
	}
	// Newly indexed files flow into the linked collection without copying IDs.
	if err = os.WriteFile(filepath.Join(dir, "Second holiday.mp4"), []byte("video2"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = rt.local.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = cols.Rebuild(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	if keys := cols.PSEKeys(ctx, target.ID, &core.User{ID: 1}); len(keys) != 2 {
		t.Fatalf("new file missing: %v", keys)
	}
	edit("collection", target.ID, false)
	if keys := cols.PSEKeys(ctx, target.ID, &core.User{ID: 1}); len(keys) != 0 {
		t.Fatalf("unlink retained files: %v", keys)
	}
	edit("library", strconv.FormatInt(dest.ID, 10), false)
	// A remaining unrelated collection is retained.
	if libs := rt.themes.Libraries(); len(libs) != 1 || len(libs[0].Collections) != 1 || libs[0].Collections[0] != target.ID {
		t.Fatalf("other collection lost: %+v", libs)
	}
	options, err := a.localDestinations(ctx, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range options {
		if d.Linked {
			t.Fatalf("stale association: %+v", d)
		}
	}
	if err = rt.local.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rt.themes.Libraries()) != 1 {
		t.Fatal("scan recreated an unlinked library")
	}
}

func TestLocalDestinationsRejectProtectedCollections(t *testing.T) {
	for _, sp := range []core.CollectionSpec{
		{BuiltIn: true}, {OwnerUserID: 2}, {TemplateID: "remix-local-123"}, {ReleaseMode: "upcoming"}, {ReleaseMode: "mixed"},
	} {
		if editableLocalDestination(sp, 1) {
			t.Fatalf("protected collection editable: %+v", sp)
		}
	}
	if !editableLocalDestination(core.CollectionSpec{OwnerUserID: 1}, 1) {
		t.Fatal("owner collection rejected")
	}
}
