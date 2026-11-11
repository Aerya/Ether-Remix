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
	rt.thOnce.Do(func() { rt.themes, err = themes.New(t.Context(), db, cols) })
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "Mes vacances.mp4"), []byte("local-video-bytes"), 0600)
	l := &localmedia.Library{DB: db, Publish: a.publishLocalMedia, Probe: func(context.Context, *os.File, string) (localmedia.File, error) {
		return localmedia.File{Duration: 90}, nil
	}}
	if err = l.Add(t.Context(), localmedia.Root{Name: "Vidéos personnelles", Path: dir, Kind: "video"}); err != nil {
		t.Fatal(err)
	}
	if err = l.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	libs := rt.themes.Libraries()
	if len(libs) != 1 || libs[0].Name != "Vidéos personnelles" {
		t.Fatalf("libraries: %+v", libs)
	}
	if err = l.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(rt.themes.Libraries()) != 1 {
		t.Fatal("duplicate library")
	}
	files, _ := l.Files(t.Context(), "video")
	if len(files) != 1 {
		t.Fatal(files)
	}
	key := core.LocalKey(files[0].ID)
	title, err := cat.Get(t.Context(), key)
	if err != nil || title.IDs.TMDB != 0 || title.Title != "Mes vacances" {
		t.Fatal(title, err)
	}
	old := releasedb.Playable()
	releasedb.OverridePlayable(releasedb.NewPlayable(2, "", false))
	defer releasedb.OverridePlayable(old)
	page, err := cat.Browse(t.Context(), core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 20})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("browse %+v %v", page, err)
	}
	r := resolver.New(nil, nil, db, config.Defaults(t.TempDir()), resolver.Options{})
	res, err := r.Resolve(t.Context(), core.ResolveRequest{Title: title})
	if err != nil {
		t.Fatal(err)
	}
	keys := rt.themes.AllKeys(t.Context(), core.TypeMovie)
	if len(keys) != 1 || keys[0] != key {
		t.Fatalf("local collection membership: %v", keys)
	}
	eng := localmedia.Engine{Library: l}
	src, err := eng.Open(t.Context(), res.Release.ID, res.NZB)
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
	ps, err := pm.Prepare(t.Context(), &core.User{ID: 1, IsOwner: true}, key, core.ClientCaps{ClientID: "local-test", VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, Containers: []string{"mp4"}})
	if err != nil || ps == nil {
		t.Fatalf("native local playback session: %v", err)
	}
	roots, _ := l.Roots(t.Context())
	if err = l.Remove(t.Context(), roots[0].ID); err != nil {
		t.Fatal(err)
	}
	cat.LocalMediaChanged()
	if _, err = cat.Get(t.Context(), key); !errors.Is(err, core.ErrNotFound) {
		t.Fatal(err)
	}
	page, err = cat.Browse(t.Context(), core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 20})
	if err != nil || len(page.Items) != 0 {
		t.Fatal(page, err)
	}
}
