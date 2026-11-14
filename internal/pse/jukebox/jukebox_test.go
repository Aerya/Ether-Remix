package jukebox

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/store"
)

type fakeCols struct {
	specs []core.CollectionSpec
	fail  int
}

func (f *fakeCols) ListSpecs(context.Context) ([]core.CollectionSpec, error) {
	return append([]core.CollectionSpec(nil), f.specs...), nil
}
func (f *fakeCols) CreateSpec(_ context.Context, sp core.CollectionSpec) (*core.CollectionSpec, error) {
	if f.fail > 0 && len(f.specs)+1 == f.fail {
		return nil, fmt.Errorf("test failure")
	}
	sp.ID = fmt.Sprint(len(f.specs) + 1)
	f.specs = append(f.specs, sp)
	return &sp, nil
}
func setup(t *testing.T) (*Service, *fakeCols, *libraries.Service) {
	t.Helper()
	ctx := context.Background()
	db, err := store.OpenPath(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	libs, err := libraries.New(ctx, db, libraries.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cols := &fakeCols{}
	return New(cols, libs), cols, libs
}
func TestQuickSetupConcurrentAndPreservesEdits(t *testing.T) {
	s, cols, libs := setup(t)
	ctx := context.Background()
	req := Request{Lists: starters, Libraries: true}
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.Apply(ctx, req)
			if err != nil || len(out.Collections) != 4 || len(out.Libraries) != 4 {
				t.Errorf("apply: %+v %v", out, err)
			}
		}()
	}
	wg.Wait()
	if len(cols.specs) != 4 {
		t.Fatalf("duplicate collections: %d", len(cols.specs))
	}
	all, err := libs.List(ctx)
	if err != nil || len(all) != 6 {
		t.Fatalf("libraries: %+v %v", all, err)
	}
	for i, sp := range cols.specs {
		if sp.Sort != "released" || !sp.SortDesc || sp.Refresh != "daily" || sp.ReleaseMode != "released" || !sp.Targets.CollectionsTab || !sp.Targets.HomeRow || !sp.Targets.SectionRow || !sp.Enabled {
			t.Fatalf("bad defaults: %+v", sp)
		}
		lib := all[i+2]
		if lib.CollectionID != int64(i+1) || lib.MediaType != core.MediaType(sp.MediaType) || lib.Present.Plex == nil || lib.Present.MediaBrowser == nil || lib.Present.Silo == nil || lib.Present.Plex.Base != sp.MediaType {
			t.Fatalf("bad publication: %+v", lib)
		}
		expected := "movies"
		if sp.MediaType == "show" {
			expected = "tvshows"
		}
		if lib.Present.MediaBrowser.CollectionType != expected || lib.Present.Silo.Type != expected {
			t.Fatalf("bad native type: %+v", lib.Present)
		}
	}
	cols.specs[0].Name = "Mon nom"
	cols.specs[0].Sort = "rating"
	cols.specs[0].Enabled = false
	if err := libs.Rename(ctx, all[2].ID, "Ma bibliothèque"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ctx, req); err != nil {
		t.Fatal(err)
	}
	got, _ := libs.Get(ctx, all[2].ID)
	if got.Name != "Ma bibliothèque" || cols.specs[0].Name != "Mon nom" || cols.specs[0].Sort != "rating" || cols.specs[0].Enabled {
		t.Fatal("user edits overwritten")
	}
}
func TestRetryAndCollectionOnly(t *testing.T) {
	s, cols, libs := setup(t)
	ctx := context.Background()
	cols.fail = 2
	out, err := s.Apply(ctx, Request{Lists: starters, Libraries: true})
	if err == nil || len(out.Collections) != 1 || len(out.Libraries) != 1 {
		t.Fatalf("partial failure: %+v %v", out, err)
	}
	cols.fail = 0
	out, err = s.Apply(ctx, Request{Lists: starters, Libraries: false})
	if err != nil || len(cols.specs) != 4 || len(out.Libraries) != 0 {
		t.Fatalf("resume: %+v %v", out, err)
	}
	all, _ := libs.List(ctx)
	if len(all) != 3 {
		t.Fatalf("collection-only created libraries: %v", all)
	}
	out, err = s.Apply(ctx, Request{Lists: starters, Libraries: true})
	if err != nil || len(out.Libraries) != 4 || len(cols.specs) != 4 {
		t.Fatalf("promote: %+v %v", out, err)
	}
	all, _ = libs.List(ctx)
	if len(all) != 6 {
		t.Fatal("retry duplicated library")
	}
}
func TestInvalidBatchDoesNotWrite(t *testing.T) {
	s, cols, _ := setup(t)
	for _, bad := range []List{{"../other", "bad", "movie"}, {"ok", "bad", "episode"}, {"ok", "", "show"}} {
		if _, err := s.Apply(context.Background(), Request{Lists: append(append([]List{}, starters...), bad), Libraries: true}); err == nil {
			t.Fatal("accepted invalid list")
		}
		if len(cols.specs) != 0 {
			t.Fatal("partial write before validation")
		}
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{broken`)))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestCatalogParsesOnlyJukeboxLists(t *testing.T) {
	body := `<a href="/lists/jukebox8786/films"><img alt="poster"></a><a href="/lists/jukebox8786/films">Documentaire (Films)</a>
 <a href="https://mdblist.com/lists/jukebox8786/series/">Documentaire (Séries TV)</a>
 <a href="/lists/jukebox8786/mixed">Films &amp; Séries</a><a href="/lists/jukebox8786/anime">Anime</a>
 <a href="/lists/other/bad">Bad</a><a href="https://evil.example/lists/jukebox8786/evil">Evil</a>
 <a href="//evil.example/lists/jukebox8786/evil">Evil</a><a href="/lists/jukebox8786/films/json">JSON</a>`
	got, err := ParseCatalog(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	want := []List{{"anime", "Anime", "mixed"}, {"films", "Documentaire (Films)", "movie"}, {"series", "Documentaire (Séries TV)", "show"}, {"mixed", "Films & Séries", "mixed"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if _, err := ParseCatalog(strings.NewReader(`<html>login</html>`)); err == nil {
		t.Fatal("accepted empty catalog")
	}
}
func TestPrivateOrChangedSourcesAreNotReused(t *testing.T) {
	s, cols, _ := setup(t)
	ctx := context.Background()
	_, err := s.Apply(ctx, Request{Lists: starters[:1]})
	if err != nil {
		t.Fatal(err)
	}
	cols.specs[0].OwnerUserID = 12
	_, err = s.Apply(ctx, Request{Lists: starters[:1]})
	if err != nil {
		t.Fatal(err)
	}
	cols.specs[1].Sources[0].URL = Profile + "another"
	_, err = s.Apply(ctx, Request{Lists: starters[:1]})
	if err != nil || len(cols.specs) != 3 {
		t.Fatalf("reused private or changed source: %v %v", cols.specs, err)
	}
}
