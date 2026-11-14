package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/pse/localmedia"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/pse/themes"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestLocalVideoNativeCatalogueResolverAndPlaybackSource(t *testing.T) {
	db := store.OpenTest(t)
	cat, err := catalog.New(db, nil, config.Catalog{ReleaseMode: config.ModeDigital}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cols, err := collections.New(db, cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, catalog: cat, collections: cols}
	rt := pseRuntimeOf(a)
	rt.thOnce.Do(func() { rt.themes, err = themes.New(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}), db, cols) })
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "Mes vacances.mp4"), []byte("local-video-bytes"), 0600)
	l := &localmedia.Library{DB: db, Publish: a.publishLocalMedia, Probe: func(context.Context, *os.File, string) (localmedia.File, error) {
		return localmedia.File{Duration: 90}, nil
	}}
	if err = l.Add(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}), localmedia.Root{Name: "Vidéos personnelles", Path: dir, Kind: "video"}); err != nil {
		t.Fatal(err)
	}
	if err = l.Scan(core.WithCatalogViewer(t.Context(), &core.User{ID: 1})); err != nil {
		t.Fatal(err)
	}
	libs := rt.themes.Libraries()
	if len(libs) != 1 || libs[0].Name != "Vidéos personnelles" {
		t.Fatalf("libraries: %+v", libs)
	}
	if err = l.Scan(core.WithCatalogViewer(t.Context(), &core.User{ID: 1})); err != nil {
		t.Fatal(err)
	}
	if len(rt.themes.Libraries()) != 1 {
		t.Fatal("duplicate library")
	}
	files, _ := l.Files(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}), "video")
	if len(files) != 1 {
		t.Fatal(files)
	}
	key := core.LocalKey(files[0].ID)
	title, err := cat.Get(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}), key)
	denied := core.WithCatalogViewer(t.Context(), &core.User{ID: 2})
	if _, e := cat.StoredTitle(denied, key); e == nil {
		t.Fatal("stored private metadata exposed")
	}
	if _, e := cat.CachedGet(denied, key); e == nil {
		t.Fatal("cached private metadata exposed")
	}
	if title != nil && cat.VisibleForViewer(&core.User{ID: 2}, title) {
		t.Fatal("cached private title passed policy")
	}

	if err != nil || title.IDs.TMDB != 0 || title.Title != "Mes vacances" {
		t.Fatal(title, err)
	}
	old := releasedb.Playable()
	releasedb.OverridePlayable(releasedb.NewPlayable(2, "", false))
	defer releasedb.OverridePlayable(old)
	page, err := cat.Browse(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}), core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, CatalogViewerID: 1}, Limit: 20})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("browse %+v %v", page, err)
	}
	r := resolver.New(nil, nil, db, config.Defaults(t.TempDir()), resolver.Options{})
	res, err := r.Resolve(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}), core.ResolveRequest{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	keys := rt.themes.AllKeys(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}), core.TypeMovie)
	if len(keys) != 1 || keys[0] != key {
		t.Fatalf("local collection membership: %v", keys)
	}
	eng := localmedia.Engine{Library: l}
	src, err := eng.Open(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}), res.Release.ID, res.NZB)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(io.NewSectionReader(src, 0, src.Size()))
	src.Close()
	if err != nil || string(got) != "local-video-bytes" {
		t.Fatal(string(got), err)
	}
	cfg := config.Defaults(t.TempDir())
	pm, err := playback.New(playback.Deps{Resolver: r, Engine: eng, Transcoder: directOnly{}, Catalog: cat, DB: db,
		SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile {
			return config.DefaultQuality().Profiles[0]
		},
		NativeProbe: func(context.Context, core.MediaSource) (*core.MediaInfo, error) {
			return &core.MediaInfo{Container: "mp4", Size: int64(len("local-video-bytes")), Duration: 90 * time.Second, Streams: []core.StreamInfo{{Type: "video", Codec: "h264", Width: 1280, Height: 720}, {Type: "audio", Codec: "aac", Channels: 2, SampleRate: 44100}}}, nil
		},
	}, cfg, playback.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer pm.Close()
	ps, err := pm.Prepare(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}), &core.User{ID: 1, IsOwner: true}, key, core.ClientCaps{ClientID: "local-test", VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, Containers: []string{"mp4"}})
	if err != nil || ps == nil {
		t.Fatalf("native local playback session: %v", err)
	}
	// A readable but empty mount must preserve both the catalogue and collection.
	if err := os.Remove(filepath.Join(dir, "Mes vacances.mp4")); err != nil {
		t.Fatal(err)
	}
	ownerCtx := core.WithCatalogViewer(t.Context(), &core.User{ID: 1})
	if err := l.Scan(ownerCtx); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.Get(ownerCtx, key); err != nil {
		t.Fatal("empty mount removed catalogue entry", err)
	}
	if keys := rt.themes.AllKeys(ownerCtx, core.TypeMovie); len(keys) != 1 || keys[0] != key {
		t.Fatal("empty mount removed collection", keys)
	}
	roots, _ := l.Roots(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}))
	if err = l.Remove(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}), roots[0].ID); err != nil {
		t.Fatal(err)
	}
	cat.LocalMediaChanged()
	if _, err = cat.Get(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}), key); !errors.Is(err, core.ErrNotFound) {
		t.Fatal(err)
	}
	page, err = cat.Browse(core.WithCatalogViewer(t.Context(), &core.User{ID: 1}), core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, CatalogViewerID: 1}, Limit: 20})
	if err != nil || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
}

func TestMatchedLocalFileUsesExistingCanonicalPlayback(t *testing.T) {
	db := store.OpenTest(t)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := core.MovieKey(42)
	title := core.Title{Key: key, IDs: core.IDs{TMDB: 42}, Type: core.TypeMovie, Title: "Un Film", Year: 2020, Released: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := cat.UpsertDetailedTitle(t.Context(), title); err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, catalog: cat}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Un.Film.2020.mkv"), []byte("local"), 0600)
	l := &localmedia.Library{DB: db, Publish: a.publishLocalMedia, Probe: func(context.Context, *os.File, string) (localmedia.File, error) { return localmedia.File{}, nil }}
	owner := core.WithCatalogViewer(t.Context(), &core.User{ID: 1})
	if err := l.Add(owner, localmedia.Root{Name: "Films", Path: dir, Kind: "video"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := l.Scan(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	var stored core.RatingKey
	if err := db.QueryRow("SELECT title_key FROM pse_local_files").Scan(&stored); err != nil || stored != key {
		t.Fatal(stored, err)
	}
	var duplicates int
	if err := db.QueryRow("SELECT COUNT(*) FROM catalog_titles WHERE type='movie'").Scan(&duplicates); err != nil || duplicates != 1 {
		t.Fatal(duplicates, err)
	}
	r := resolver.New(nil, nil, db, config.Defaults(t.TempDir()), resolver.Options{})
	if res, err := r.Resolve(owner, core.ResolveRequest{Title: &title}); err != nil || res.Title != key {
		t.Fatal(res, err)
	}
	if _, err := r.Resolve(core.WithCatalogViewer(t.Context(), &core.User{ID: 2}), core.ResolveRequest{Title: &title}); err == nil {
		t.Fatal("private local release exposed")
	}
}
