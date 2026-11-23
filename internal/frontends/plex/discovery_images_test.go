package plex

import (
	"bytes"
	"context"
	"github.com/braedonsaunders/ether/internal/artwork"
	"github.com/braedonsaunders/ether/internal/config"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/discovery"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"github.com/braedonsaunders/ether/internal/overlays"
)

type discoveryImageSource struct{ calls int }

func (s *discoveryImageSource) Get(_ context.Context, path string, _ int) ([]byte, string, error) {
	s.calls++
	return []byte(path), "image/jpeg", nil
}

var (
	pmsThumbPathRe = regexp.MustCompile(`^/library/metadata/[^/?#]+/thumb/[^/?#]+$`)
	pmsArtPathRe   = regexp.MustCompile(`^/library/metadata/[^/?#]+/art/[^/?#]+$`)
	pmsLogoPathRe  = regexp.MustCompile(`^/library/metadata/[^/?#]+/clearLogo/[^/?#]+$`)
)

func TestPhotoTranscodeDiscoveryArtwork(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	ctx := context.Background()
	images := &discoveryImageSource{}
	ov := overlays.New(discoveryDB(t), overlays.Options{Images: images})
	if err := ov.SaveSettings(ctx, overlays.Settings{Enabled: true, Localize: true}); err != nil {
		t.Fatal(err)
	}
	title := fightClub
	title.LogoPath = "/en-logo.png"
	f.catalog.titles[title.Key] = title
	if err := ov.SaveArtwork(ctx, title.Key, overlays.Artwork{
		Backdrops: []overlays.Variant{{Path: "/alt-backdrop.jpg"}},
	}); err != nil {
		t.Fatal(err)
	}
	f.srv.setDiscovery((&discovery.Features{Overlays: ov, Localize: func(_ context.Context, title *core.Title, lang string) (*core.Title, error) {
		localized := *title
		localized.PosterPath = "/" + lang + "-poster.jpg"
		localized.BackdropPath = "/" + lang + "-backdrop.jpg"
		localized.LogoPath = "/" + lang + "-logo.jpg"
		return &localized, nil
	}}).Frontend())
	pr := &Request{User: &ownerUser, Language: "fr-CA"}
	md := f.srv.metadata(ctx, pr, &title, renderOpts{})
	if !pmsThumbPathRe.MatchString(md.Thumb) {
		t.Fatalf("thumb not PMS-shaped: %q", md.Thumb)
	}
	if !pmsArtPathRe.MatchString(md.Art) {
		t.Fatalf("art not PMS-shaped: %q", md.Art)
	}
	var logoURL string
	for _, img := range md.Image {
		if img.Type == "clearLogo" {
			logoURL = img.URL
		}
	}
	if !pmsLogoPathRe.MatchString(logoURL) {
		t.Fatalf("clearLogo not PMS-shaped: %q", logoURL)
	}
	// Each path encodes a discovery token and has no query string.
	cases := []struct {
		name string
		path string
		want string
	}{
		{"poster", md.Thumb, "/fr-CA-poster.jpg"},
		{"backdrop", md.Art, "/alt-backdrop.jpg"},
		{"logo", logoURL, "/fr-CA-logo.jpg"},
		{"legacy-poster", "/library/metadata/" + title.Key.String() + "/discoveryPoster/v1?lang=fr-CA", "/fr-CA-poster.jpg"},
		{"legacy-backdrop", "/library/metadata/" + title.Key.String() + "/discoveryPoster/v1?kind=backdrop&lang=fr-CA", "/alt-backdrop.jpg"},
		{"legacy-logo", "/library/metadata/" + title.Key.String() + "/discoveryPoster/v1?kind=logo&lang=fr-CA", "/fr-CA-logo.jpg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			direct := f.request(t, http.MethodGet, tc.path, nil)
			if direct.Code != http.StatusOK || direct.Body.String() != tc.want {
				t.Fatalf("direct artwork %s: %d %q (want %q)", tc.path, direct.Code, direct.Body.String(), tc.want)
			}
			for _, prefix := range []string{"", "http://127.0.0.1:32400"} {
				target := "/photo/:/transcode?width=300&height=450&url=" + url.QueryEscape(prefix+tc.path)
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					r := f.request(t, method, target, nil)
					if r.Code != http.StatusOK || r.Header().Get("Content-Type") != "image/jpeg" {
						t.Fatalf("%s wrapped artwork: %d %q", method, r.Code, r.Body.String())
					}
					if method == http.MethodGet && r.Body.String() != tc.want || method == http.MethodHead && r.Body.Len() != 0 {
						t.Fatalf("%s wrapped body: %q", method, r.Body.String())
					}
					assertPassiveArtwork(t, r)
				}
				r := f.request(t, http.MethodGet, target, map[string]string{"If-None-Match": direct.Header().Get("ETag")})
				if r.Code != http.StatusNotModified || r.Body.Len() != 0 {
					t.Fatalf("conditional artwork: %d %q", r.Code, r.Body.String())
				}
			}
		})
	}
	// Changing the poster version rotates both the URL token and the ETag.
	firstResp := f.request(t, http.MethodGet, md.Thumb, nil)
	if err := ov.SaveArtwork(ctx, title.Key, overlays.Artwork{
		PinnedPoster: "/pinned-poster.jpg",
		Backdrops:    []overlays.Variant{{Path: "/alt-backdrop.jpg"}},
	}); err != nil {
		t.Fatal(err)
	}
	updated := f.srv.metadata(ctx, pr, &title, renderOpts{})
	if updated.Thumb == md.Thumb || !pmsThumbPathRe.MatchString(updated.Thumb) {
		t.Fatalf("version change did not rotate PMS thumb token: before=%q after=%q", md.Thumb, updated.Thumb)
	}
	secondResp := f.request(t, http.MethodGet, updated.Thumb, map[string]string{"If-None-Match": firstResp.Header().Get("ETag")})
	if secondResp.Code != http.StatusOK || secondResp.Body.String() != "/pinned-poster.jpg" || secondResp.Header().Get("ETag") == firstResp.Header().Get("ETag") {
		t.Fatalf("version change did not rotate ETag: code=%d body=%q etag=%q old=%q", secondResp.Code, secondResp.Body.String(), secondResp.Header().Get("ETag"), firstResp.Header().Get("ETag"))
	}
	// Different languages (including empty) emit distinct PMS tokens.
	emptyLang := f.srv.metadata(ctx, &Request{User: &ownerUser}, &title, renderOpts{})
	enLang := f.srv.metadata(ctx, &Request{User: &ownerUser, Language: "en"}, &title, renderOpts{})
	if !pmsThumbPathRe.MatchString(emptyLang.Thumb) || emptyLang.Thumb == enLang.Thumb || enLang.Thumb == updated.Thumb {
		t.Fatalf("language tokens collided: empty=%q en=%q frCA=%q", emptyLang.Thumb, enLang.Thumb, updated.Thumb)
	}
	// The nested URL cannot supply credentials or bypass viewer restrictions.
	f.srv.testAuth().users["kid"] = core.User{ID: 30, MaxContentRating: "PG"}
	for _, target := range []string{md.Thumb, "/photo/:/transcode?url=" + url.QueryEscape(md.Thumb+"?X-Plex-Token="+ownerToken)} {
		for token, want := range map[string]int{"kid": http.StatusNotFound, "invalid": http.StatusUnauthorized} {
			before := images.calls
			r := f.request(t, http.MethodGet, target, map[string]string{"X-Plex-Token": token})
			if r.Code != want || images.calls != before {
				t.Fatalf("target %q viewer %q: status=%d image fetches=%d", target, token, r.Code, images.calls-before)
			}
		}
	}
	r := f.request(t, http.MethodGet, "/photo/:/transcode?url="+url.QueryEscape("https://untrusted.invalid"+md.Thumb), nil)
	if r.Code != http.StatusForbidden {
		t.Fatalf("foreign host: %d", r.Code)
	}
}

func TestPhotoTranscodePosterProvider(t *testing.T) {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 400, 600)), nil)
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Query().Get("type") != "movie" {
			t.Error("wrong type")
		}
		w.Write(buf.Bytes())
	}))
	defer upstream.Close()
	cfg := config.Posters{Enabled: true, Source: "postersplus", BaseURL: upstream.URL, APIKey: "never-visible"}
	images := artwork.NewImages(artwork.ImagesOptions{Posters: func() config.Posters { return cfg }})
	f := newFixture()
	defer f.srv.Close()
	ov := overlays.New(discoveryDB(t), overlays.Options{Images: images})
	f.srv.setDiscovery((&discovery.Features{Overlays: ov}).Frontend())
	pr := &Request{User: &ownerUser}
	md := f.srv.metadata(context.Background(), pr, &fightClub, renderOpts{})
	if !pmsThumbPathRe.MatchString(md.Thumb) || !strings.Contains(md.Thumb, "/thumb/"+discoveryArtworkTokenPrefix) || strings.Contains(md.Thumb, "never-visible") {
		t.Fatal(md.Thumb)
	}
	target := "/photo/:/transcode?width=200&url=" + url.QueryEscape(md.Thumb)
	r := f.request(t, http.MethodGet, target, nil)
	if r.Code != 200 || hits != 1 {
		t.Fatalf("status %d hits %d", r.Code, hits)
	}
	decoded, _, err := image.DecodeConfig(bytes.NewReader(r.Body.Bytes()))
	if err != nil || decoded.Width != 200 {
		t.Fatal(decoded, err)
	}
	for _, product := range []string{"Plex for Android (Mobile)", "Plex for Android (TV)"} {
		target := "/photo/:/transcode?width=200&format=png&url=" + url.QueryEscape("https://previous-connection.invalid"+md.Thumb)
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			r := f.request(t, method, target, map[string]string{"X-Plex-Product": product})
			if r.Code != 200 || r.Header().Get("Content-Type") != "image/png" || r.Header().Get("Location") != "" {
				t.Fatalf("%s provider artwork: %d %s", product, r.Code, r.Header())
			}
			if method == http.MethodGet {
				if _, kind, err := image.DecodeConfig(bytes.NewReader(r.Body.Bytes())); err != nil || kind != "png" {
					t.Fatalf("provider output: %s %v", kind, err)
				}
			} else if r.Body.Len() != 0 {
				t.Fatal("HEAD body")
			}
		}
	}
	old := md.Thumb
	cfg.APIKey = "changed"
	md = f.srv.metadata(context.Background(), pr, &fightClub, renderOpts{})
	if md.Thumb == old || !pmsThumbPathRe.MatchString(md.Thumb) {
		t.Fatal("client cache version unchanged")
	}
	cfg.Enabled = false
	md = f.srv.metadata(context.Background(), pr, &fightClub, renderOpts{})
	if strings.Contains(md.Thumb, discoveryArtworkTokenPrefix) || strings.Contains(md.Thumb, "discoveryPoster") {
		t.Fatal("default not restored")
	}
}

func TestPosterGenerationRollsCoverPosterAndEpisodes(t *testing.T) {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 400, 600)), nil)
	tmdbBuf := buf.Bytes()
	var providerBuf bytes.Buffer
	_ = jpeg.Encode(&providerBuf, image.NewRGBA(image.Rect(0, 0, 300, 450)), nil)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(providerBuf.Bytes())
	}))
	defer upstream.Close()
	tmdbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(tmdbBuf)
	}))
	defer tmdbSrv.Close()

	cfg := config.Posters{Version: 1, Source: "tmdb"}
	images := artwork.NewImages(artwork.ImagesOptions{Posters: func() config.Posters { return cfg }})
	f := newFixture()
	defer f.srv.Close()
	f.srv.setImageURL(func(path, size string) string { return tmdbSrv.URL + "/" + size + path })
	ov := overlays.New(discoveryDB(t), overlays.Options{Images: images})
	f.srv.setDiscovery((&discovery.Features{Overlays: ov}).Frontend())
	pr := &Request{User: &ownerUser}

	coverPosterOf := func(md model.Metadata) string {
		for _, img := range md.Image {
			if img.Type == "coverPoster" {
				return img.URL
			}
		}
		return ""
	}

	// 0. Baseline before enabling any poster source (overlays off).
	baseMovie := f.srv.metadata(context.Background(), pr, &fightClub, renderOpts{})
	baseCover := coverPosterOf(baseMovie)
	if baseCover == "" || baseCover != baseMovie.Thumb {
		t.Fatalf("baseline coverPoster=%q thumb=%q", baseCover, baseMovie.Thumb)
	}

	// 1. Enable poster provider with overlays OFF: both Thumb and Image[coverPoster]
	// must move to the discovery token URL (Plex for iOS renders coverPoster),
	// and episodes must use the show's discovery poster for grandparentThumb/coverPoster.
	cfg.Generation = 1
	cfg.Source = "postersplus"
	cfg.Accounts.PostersPlus = config.PosterAccount{Enabled: true, BaseURL: upstream.URL}
	enabledMovie := f.srv.metadata(context.Background(), pr, &fightClub, renderOpts{})
	enabledCover := coverPosterOf(enabledMovie)
	if !strings.Contains(enabledMovie.Thumb, "/thumb/"+discoveryArtworkTokenPrefix) || enabledCover != enabledMovie.Thumb {
		t.Fatalf("enabled overlays-off movie: thumb=%q coverPoster=%q", enabledMovie.Thumb, enabledCover)
	}
	ep := f.srv.metadata(context.Background(), pr, &bbE1, renderOpts{})
	epCover := coverPosterOf(ep)
	if !strings.HasPrefix(ep.GrandparentThumb, "/library/metadata/"+breakingBad.Key.String()+"/thumb/"+discoveryArtworkTokenPrefix) || epCover != ep.GrandparentThumb {
		t.Fatalf("enabled episode show poster: grandparent=%q coverPoster=%q", ep.GrandparentThumb, epCover)
	}
	// Episode's own still stays on its own key, never rewritten to a show poster.
	if strings.Contains(ep.Thumb, discoveryArtworkTokenPrefix) {
		t.Fatalf("episode still rewritten: %q", ep.Thumb)
	}

	// 2. Re-saving same settings within the 6-hour window rolls Generation -> new token URLs.
	cfg.Generation = 2
	resavedMovie := f.srv.metadata(context.Background(), pr, &fightClub, renderOpts{})
	if resavedMovie.Thumb == enabledMovie.Thumb || coverPosterOf(resavedMovie) != resavedMovie.Thumb {
		t.Fatalf("resave did not roll URL: before=%q after=%q cover=%q", enabledMovie.Thumb, resavedMovie.Thumb, coverPosterOf(resavedMovie))
	}

	// 3. Disabling rolls Generation -> restores default TMDB poster at a NEW URL
	// (never reverting to the pre-enable cached URL), still valid through /photo/:/transcode.
	cfg.Generation = 3
	cfg.Source = "tmdb"
	cfg.Accounts.PostersPlus.Enabled = false
	disabledMovie := f.srv.metadata(context.Background(), pr, &fightClub, renderOpts{})
	disabledCover := coverPosterOf(disabledMovie)
	if strings.Contains(disabledMovie.Thumb, discoveryArtworkTokenPrefix) || disabledMovie.Thumb == baseMovie.Thumb || disabledCover != disabledMovie.Thumb {
		t.Fatalf("disabled movie URL: base=%q disabled=%q cover=%q", baseMovie.Thumb, disabledMovie.Thumb, disabledCover)
	}
	r := f.request(t, http.MethodGet, "/photo/:/transcode?width=200&url="+url.QueryEscape(disabledCover), nil)
	if r.Code != http.StatusOK {
		t.Fatalf("transcode disabled rolled URL %q: %d", disabledCover, r.Code)
	}

	// 4. Re-enabling the same provider within the same 6-hour bucket rolls to a fresh URL.
	cfg.Generation = 4
	cfg.Source = "postersplus"
	cfg.Accounts.PostersPlus.Enabled = true
	reenabledMovie := f.srv.metadata(context.Background(), pr, &fightClub, renderOpts{})
	if reenabledMovie.Thumb == enabledMovie.Thumb || reenabledMovie.Thumb == resavedMovie.Thumb || coverPosterOf(reenabledMovie) != reenabledMovie.Thumb {
		t.Fatalf("reenable collided with prior URL: g1=%q g2=%q g4=%q cover=%q", enabledMovie.Thumb, resavedMovie.Thumb, reenabledMovie.Thumb, coverPosterOf(reenabledMovie))
	}
}
