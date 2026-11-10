package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/pse/seerrcatalog"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestSeerrCatalogImportKeepsHiddenTitlesHidden(t *testing.T) {
	ctx := t.Context()
	db := store.OpenTest(t)
	now := time.Now().UTC()
	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	a := &App{catalog: cat}
	titles := []core.Title{
		{Key: core.MovieKey(1), Type: core.TypeMovie, IDs: core.IDs{TMDB: 1}, Title: "Released", Released: now.Add(-24 * time.Hour)},
		{Key: core.MovieKey(2), Type: core.TypeMovie, IDs: core.IDs{TMDB: 2}, Title: "Upcoming", Released: now.Add(24 * time.Hour)},
		{Key: core.MovieKey(3), Type: core.TypeMovie, IDs: core.IDs{TMDB: 3}, Title: "Adult", Released: now.Add(-24 * time.Hour), Adult: true},
	}
	observations := []seerrcatalog.Snapshot{}
	for _, title := range titles {
		if err := cat.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
		observations = append(observations, seerrcatalog.Snapshot{Schema: seerrcatalog.Schema, Media: seerrcatalog.Media{ID: title.IDs.TMDB, TMDB: title.IDs.TMDB, Type: "movie"}, Available: true, Confirmed: true, Status: "available", Checked: now, Expires: now.Add(26 * time.Hour)})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"schema": seerrcatalog.Schema, "revision": 1, "items": observations, "nextCursor": nil})
	}))
	defer srv.Close()
	restore := pseenv.Replace(map[string]string{"PSE_SEERRCATALOG": "on", "PSE_SEERRCATALOG_URL": srv.URL, "PSE_SEERRCATALOG_TOKEN": strings.Repeat("a", 64)})
	defer restore()
	service := seerrcatalog.New(db, srv.Client())
	service.Ensure = a.ensureSeerrCatalogMetadata
	if err := service.Sync(ctx); err != nil {
		t.Fatalf("a hidden title blocked the whole import: %v", err)
	}
	if service.Status().Items != 3 {
		t.Fatalf("incomplete snapshot: %+v", service.Status())
	}
	if !seerrcatalog.Trusted(ctx, db, core.MovieKey(1)) {
		t.Fatal("released title availability was not imported")
	}
	for _, key := range []core.RatingKey{core.MovieKey(2), core.MovieKey(3)} {
		if _, err := cat.Get(ctx, key); !errors.Is(err, core.ErrNotFound) {
			t.Fatalf("hidden title became visible: %v", err)
		}
	}
	if err := a.ensureSeerrCatalogMetadata(ctx, core.MovieKey(999)); err == nil {
		t.Fatal("missing metadata was accepted")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.ensureSeerrCatalogMetadata(ctx, core.MovieKey(999)); err == nil {
		t.Fatal("database error was ignored")
	}
}
