package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/artwork"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/discovery"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/overlays"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type storedArtwork map[core.RatingKey]core.Title

func (s storedArtwork) StoredTitle(_ context.Context, k core.RatingKey) (*core.Title, error) {
	t, ok := s[k]
	if !ok {
		return nil, core.ErrNotFound
	}
	return &t, nil
}

type artworkPolicy struct{ viewer *core.User }

func (p *artworkPolicy) UserPolicy(*core.User) *core.User         { return p.viewer }
func (p *artworkPolicy) Allowed(_ *core.User, t *core.Title) bool { return !t.Adult }

type imageTransport func(*http.Request) (*http.Response, error)

func (f imageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestListChecksVisibilityOnce(t *testing.T) {
	ctx := context.Background()
	key := core.MovieKey(550)
	catalog := storedArtwork{key: {Key: key, Type: core.TypeMovie, PosterPath: "/poster550.jpg", BackdropPath: "/back.jpg", LogoPath: "/logo.jpg", Released: time.Unix(1, 0)}}
	viewer := &core.User{ID: 7, IsOwner: true}
	var checks int
	source := Source{
		Catalog: catalog,
		Visibility: experience.Visibility{
			Policy: &artworkPolicy{viewer: viewer},
			FilterAvailable: func(context.Context, []core.Title) []core.Title {
				checks++
				return []core.Title{{}}
			},
		},
		Variants: func(*core.Title, string) (*tmdb.Images, bool) { return nil, false },
	}
	if _, _, err := source.List(ctx, frontend.ArtworkRequest{Viewer: viewer, Item: key}); err != nil {
		t.Fatal(err)
	}
	if checks != 1 {
		t.Fatalf("visibility checks %d, want 1", checks)
	}
}

func TestNeutralArtworkColdSelectionRevocationAndReferences(t *testing.T) {
	ctx := context.Background()
	key := core.MovieKey(550)
	catalog := storedArtwork{key: {Key: key, Type: core.TypeMovie, PosterPath: "/poster550.jpg", Released: time.Unix(1, 0)}}
	viewer := &core.User{ID: 7, IsOwner: true}
	policy := &artworkPolicy{viewer: viewer}
	var fetched []string
	images := NewRemoteCache(1024, &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
		fetched = append(fetched, r.URL.Path)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/jpeg"}}, Body: io.NopCloser(strings.NewReader("poster-bytes"))}, nil
	})})
	source := Source{Catalog: catalog, Visibility: experience.Visibility{Policy: policy}, Images: images, ImageURL: func(path, size string) string { return "https://fixture.invalid/" + size + path }, Variants: func(*core.Title, string) (*tmdb.Images, bool) { return nil, false }}
	req := frontend.ArtworkRequest{Viewer: viewer, Item: key}
	list, complete, err := source.List(ctx, req)
	if err != nil || !complete || len(list) == 0 {
		t.Fatalf("cold selection: %+v %v %v", list, complete, err)
	}
	if len(fetched) != 0 {
		t.Fatal("enumeration fetched bytes")
	}
	poster := list[0]
	if strings.Contains(poster.Path, "poster550") {
		t.Fatal("opaque path leaked source")
	}
	got, ct, err := source.Read(ctx, req, poster, frontend.ArtworkTransform{Width: 2000})
	if err != nil || ct != "image/jpeg" || !bytes.Equal(got, []byte("poster-bytes")) || fetched[0] != "/original/poster550.jpg" {
		t.Fatalf("read: %s %q %v %+v", ct, got, err, fetched)
	}
	title := catalog[key]
	title.PosterPath = "/new.jpg"
	catalog[key] = title
	if _, _, err := source.Read(ctx, req, poster, frontend.ArtworkTransform{}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("stale reference: %v", err)
	}
	title.Adult = true
	catalog[key] = title
	if _, _, err := source.List(ctx, req); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("hidden title: %v", err)
	}
	policy.viewer = nil
	if _, _, err := source.Read(ctx, req, poster, frontend.ArtworkTransform{}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatalf("revoked viewer: %v", err)
	}
	if len(fetched) != 1 {
		t.Fatal("rejected request touched remote images")
	}
}

type refreshingArtwork struct {
	storedArtwork
	fresh core.Title
}

func (s refreshingArtwork) RefreshStoredTitle(context.Context, core.RatingKey) (*core.Title, error) {
	title := s.fresh
	return &title, nil
}

func TestStalePortraitCacheFetchesTheCurrentThumb(t *testing.T) {
	ctx := context.Background()
	key := core.MovieKey(550)
	stale := core.Title{Key: key, Type: core.TypeMovie, Released: time.Unix(1, 0)}
	fresh := stale
	fresh.Cast = []core.Person{{Name: "Actor", TMDB: 42, ThumbPath: "/actor.jpg"}}
	viewer := &core.User{ID: 7, IsOwner: true}
	var fetched []string
	images := NewRemoteCache(1024, &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
		fetched = append(fetched, r.URL.Path)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/jpeg"}}, Body: io.NopCloser(strings.NewReader("portrait-bytes"))}, nil
	})})
	source := Source{Catalog: refreshingArtwork{storedArtwork: storedArtwork{key: stale}, fresh: fresh}, Visibility: experience.Visibility{Policy: &artworkPolicy{viewer: viewer}}, Images: images, ImageURL: func(path, size string) string { return "https://fixture.invalid/" + size + path }}
	req := frontend.ArtworkRequest{Viewer: viewer, Item: key}
	listed, err := source.PersonImages(ctx, req, fresh)
	if err != nil || len(listed) != 1 {
		t.Fatalf("descriptor: %+v %v", listed, err)
	}
	got, ct, err := source.Read(ctx, req, listed[0], frontend.ArtworkTransform{})
	if err != nil || ct != "image/jpeg" || !bytes.Equal(got, []byte("portrait-bytes")) || len(fetched) != 1 || !strings.Contains(fetched[0], "actor.jpg") {
		t.Fatalf("on-demand portrait: %s %q %v %+v", ct, got, err, fetched)
	}
}

func TestCrewPortraitHashIsReadyOnList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := store.OpenTest(t)
	img := image.NewNRGBA(image.Rect(0, 0, 8, 6))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	key := core.MovieKey(10378)
	title := core.Title{Key: key, Type: core.TypeMovie, Released: time.Unix(1, 0), Crew: []core.Person{{Name: "Producer", Role: "Producer", TMDB: 4, ThumbPath: "/crew.jpg"}}}
	viewer := &core.User{ID: 7, IsOwner: true}
	var fetched []string
	images := NewRemoteCache(1024, &http.Client{Transport: imageTransport(func(r *http.Request) (*http.Response, error) {
		fetched = append(fetched, r.URL.String())
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/png"}}, Body: io.NopCloser(bytes.NewReader(encoded.Bytes()))}, nil
	})})
	source := Source{Catalog: refreshingArtwork{storedArtwork: storedArtwork{key: title}, fresh: title}, Visibility: experience.Visibility{Policy: &artworkPolicy{viewer: viewer}}, Images: images, ImageURL: func(path, size string) string { return "https://fixture.invalid/" + size + path }, Now: time.Now}
	queue := NewMetadataQueue(ctx, db, func(_ context.Context, id int64) (*core.User, error) { return &core.User{ID: id}, nil }, source.Read)
	source.Metadata = queue.Get
	req := frontend.ArtworkRequest{Viewer: viewer, Item: key}
	listed, _, err := source.List(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	saw := false
	for _, item := range listed {
		if item.Kind == frontend.ImagePerson {
			saw = true
			if item.ThumbHash != "" {
				t.Fatal("crew portrait hash blocked the list")
			}
		}
	}
	if !saw {
		t.Fatal("crew portrait missing")
	}
	deadline := time.After(2 * time.Second)
	for {
		again, _, err := source.List(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range again {
			if item.Kind == frontend.ImagePerson && item.ThumbHash != "" {
				if len(fetched) == 0 {
					t.Fatal("hash stored without reading the portrait")
				}
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("crew portrait hash was not filled fetched=%v", fetched)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestNeutralArtworkParentLookupIsStoredOnly(t *testing.T) {
	show := core.ShowKey(1)
	episode := core.EpisodeKey(1, 1, 1)
	catalog := storedArtwork{show: {Key: show, Type: core.TypeShow, PosterPath: "/show.jpg"}, episode: {Key: episode, Type: core.TypeEpisode, ShowKey: show, Released: time.Unix(1, 0)}}
	source := Source{Catalog: catalog}
	req := frontend.ArtworkRequest{Viewer: &core.User{ID: 1}, Item: episode}
	images, _, err := source.List(context.Background(), req)
	if err != nil || len(images) == 0 {
		t.Fatalf("stored parent fallback: %v", err)
	}
	delete(catalog, show)
	if _, _, err := source.List(context.Background(), req); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("missing inherited parent policy: %v", err)
	}
}

func TestPluginLandscapeCardThumbAndBackdropFallback(t *testing.T) {
	ctx := frontend.WithArtworkKinds(context.Background(), map[string]bool{
		frontend.ImagePoster:   true,
		frontend.ImageBackdrop: true,
		frontend.ImageStill:    true,
		frontend.ImageThumb:    true,
	})
	viewer := &core.User{ID: 1, IsOwner: true}
	kThumb := core.PluginKey(101)
	kBackdrop := core.PluginKey(102)
	src := Source{
		Catalog: storedArtwork{},
		PluginItem: func(_ context.Context, _ *core.User, _ core.Device, k core.RatingKey) (*core.Title, []frontend.Image, error) {
			switch k {
			case kThumb:
				title := core.Title{
					Key:           kThumb,
					Type:          core.TypeMovie,
					LandscapeCard: true,
					PosterPath:    "https://cdn.example/thumb-copied-to-poster.jpg",
					ThumbPath:     "https://cdn.example/thumb.jpg",
				}
				return &title, []frontend.Image{{Kind: frontend.ImageThumb, Path: "https://cdn.example/thumb.jpg", Width: 1280, Height: 720}}, nil
			case kBackdrop:
				title := core.Title{
					Key:           kBackdrop,
					Type:          core.TypeEpisode,
					LandscapeCard: true,
					BackdropPath:  "https://cdn.example/backdrop.jpg",
				}
				return &title, []frontend.Image{{Kind: frontend.ImageBackdrop, Path: "https://cdn.example/backdrop.jpg", Width: 1920, Height: 1080}}, nil
			default:
				return nil, nil, core.ErrNotFound
			}
		},
	}

	imgs1, ok, err := src.List(ctx, frontend.ArtworkRequest{Viewer: viewer, Item: kThumb})
	if err != nil || !ok || len(imgs1) != 1 || imgs1[0].Kind != frontend.ImageThumb {
		t.Fatalf("thumb-only landscape item = %+v (ok=%v err=%v), want single ImageThumb", imgs1, ok, err)
	}

	imgs2, ok, err := src.List(ctx, frontend.ArtworkRequest{Viewer: viewer, Item: kBackdrop})
	if err != nil || !ok {
		t.Fatalf("backdrop landscape item err=%v ok=%v", err, ok)
	}
	hasThumb := false
	for _, im := range imgs2 {
		if im.Kind == frontend.ImageThumb {
			hasThumb = true
		}
	}
	if !hasThumb {
		t.Fatalf("expected ImageThumb synthesized from backdrop on landscape item, got %+v", imgs2)
	}
}

func TestPosterGenerationRollsSharedArtworkTags(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	key := core.MovieKey(550)
	viewer := &core.User{ID: 7, IsOwner: true}
	cfg := config.Posters{Version: 1, Source: "tmdb"}
	imgs := artwork.NewImages(artwork.ImagesOptions{Posters: func() config.Posters { return cfg }})
	ov := overlays.New(db, overlays.Options{Images: imgs})
	src := Source{
		PosterGen:  func() uint64 { return cfg.Generation },
		Catalog:    storedArtwork{key: {Key: key, Type: core.TypeMovie, PosterPath: "/poster550.jpg", BackdropPath: "/back550.jpg", Released: time.Unix(1, 0)}},
		Visibility: experience.Visibility{Policy: &artworkPolicy{viewer: viewer}},
		Discovery:  (&discovery.Features{Overlays: ov}).Frontend(),
	}

	tags := func() (poster, backdrop string) {
		list, _, err := src.List(ctx, frontend.ArtworkRequest{Viewer: viewer, Item: key})
		if err != nil {
			t.Fatal(err)
		}
		for _, im := range list {
			if im.Kind == frontend.ImagePoster && poster == "" {
				poster = im.Tag
			}
			if im.Kind == frontend.ImageBackdrop && backdrop == "" {
				backdrop = im.Tag
			}
		}
		return poster, backdrop
	}

	p0, b0 := tags()
	if p0 == "" || b0 == "" {
		t.Fatalf("missing baseline tags: poster=%q backdrop=%q", p0, b0)
	}

	// Enable provider.
	cfg.Generation = 1
	cfg.Source = "betterposters"
	cfg.Accounts.BetterPosters = config.PosterAccount{Enabled: true}
	p1, b1 := tags()
	if p1 == p0 || b1 != b0 {
		t.Fatalf("enable: p0=%q p1=%q b0=%q b1=%q", p0, p1, b0, b1)
	}

	// Re-save same provider settings.
	cfg.Generation = 2
	p2, _ := tags()
	if p2 == p1 {
		t.Fatalf("resave did not roll poster tag: %q", p2)
	}

	// Disable provider -> must NOT collide with baseline p0 or enabled p1/p2.
	cfg.Generation = 3
	cfg.Source = "tmdb"
	cfg.Accounts.BetterPosters.Enabled = false
	p3, _ := tags()
	if p3 == p0 || p3 == p1 || p3 == p2 {
		t.Fatalf("disable collided: p0=%q p1=%q p2=%q p3=%q", p0, p1, p2, p3)
	}

	// Re-enable same provider within the same 6-hour bucket -> fresh tag.
	cfg.Generation = 4
	cfg.Source = "betterposters"
	cfg.Accounts.BetterPosters.Enabled = true
	p4, _ := tags()
	if p4 == p1 || p4 == p2 || p4 == p3 {
		t.Fatalf("reenable collided: p1=%q p2=%q p3=%q p4=%q", p1, p2, p3, p4)
	}
}
