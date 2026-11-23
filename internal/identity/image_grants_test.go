package identity_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/authhdr"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
)

type imageFixture struct{ calls int }

type scopedArtworkFixture struct {
	frontendtest.Artwork
	calls     int
	request   frontend.ArtworkRequest
	image     frontend.Image
	transform frontend.ArtworkTransform
}

func (f *scopedArtworkFixture) List(context.Context, frontend.ArtworkRequest) ([]frontend.Image, bool, error) {
	return nil, false, nil
}

func (f *scopedArtworkFixture) Read(_ context.Context, request frontend.ArtworkRequest, image frontend.Image, transform frontend.ArtworkTransform) ([]byte, string, error) {
	f.calls++
	f.request, f.image, f.transform = request, image, transform
	return []byte("scoped"), "image/png", nil
}

func (i *imageFixture) Get(context.Context, string, int) ([]byte, string, error) {
	i.calls++
	return []byte("image"), "image/jpeg", nil
}

func TestImageGrantRevalidatesSessionMembershipAndArtwork(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0),(2,456,0,0)`); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100000, 0)
	clock := func() time.Time { return now }
	grants, err := identity.NewGrants(db, members{db}, time.Hour, clock)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := grants.Issue(ctx, 1, nil, "jellyfin", core.Device{ID: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	otherViewerCred, err := grants.Issue(ctx, 2, nil, "jellyfin", core.Device{ID: "phone"})
	if err != nil {
		t.Fatal(err)
	}
	image := frontend.Image{Kind: "person", Path: "opaque-portrait", Tag: "v1", Subject: "tmdb:287"}
	authoritative := false
	allowed := true
	experience := frontendtest.Experience{ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
		if !allowed || req.Viewer == nil || req.Viewer.ID != 1 {
			return frontend.ItemDetail{}, core.ErrUnauthorized
		}
		return frontend.ItemDetail{ImagesAuthoritative: authoritative, Images: []frontend.Image{image}}, nil
	}}
	source := &imageFixture{}
	makeService := func(surface frontend.Protocol, key string) *identity.ImageGrantService {
		s, err := identity.NewImageGrantService(db, surface, grants, func() frontend.Experience { return experience }, source, time.Minute, clock, identity.ImageGrantOptions{Token: authhdr.MediaBrowserToken, PublicContentTags: true})
		if err != nil {
			t.Fatal(err)
		}
		s.SetImageKey([]byte(key))
		return s
	}
	service := makeService("jellyfin", "server-image-key")
	otherSurface := makeService("emby", "server-image-key")
	wrongKeyService := makeService("jellyfin", "different-image-key")

	request := frontend.ImageGrantRequest{Credential: credential, Device: core.Device{ID: "tv"}, Item: core.MovieKey(1), Image: image}
	if _, err := service.Issue(ctx, request); !errors.Is(err, core.ErrNotAvailable) {
		t.Fatal("non-authoritative issue", err)
	}
	authoritative = true
	wrong := request
	wrong.Image.Path = "/other.jpg"
	if _, err := service.Issue(ctx, wrong); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("unlisted image", err)
	}
	wrong = request
	wrong.Image.Subject = "tmdb:819"
	if _, err := service.Issue(ctx, wrong); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("unlisted subject", err)
	}
	secret, err := service.Issue(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if secret == credential {
		t.Fatal("account credential exposed")
	}
	if strings.Contains(secret, image.Path) {
		t.Fatal("image path exposed in token")
	}
	if raw, err := base64.RawURLEncoding.DecodeString(secret); err != nil || bytes.Contains(raw, []byte(image.Path)) {
		t.Fatalf("raw token exposed image path or failed decode: err=%v", err)
	}

	// Same image gets the exact same URL in repeated responses and across devices/time.
	secondReq := request
	secondReq.Device.ID = "bedroom-tv"
	secretAgain, err := service.Issue(ctx, secondReq)
	if err != nil || secretAgain != secret {
		t.Fatalf("expected identical URL for same image: %q vs %q (err=%v)", secret, secretAgain, err)
	}

	callReq := func(s *identity.ImageGrantService, r *http.Request, token string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.Serve(w, r, token)
		return w
	}
	call := func(s *identity.ImageGrantService, token string) int {
		return callReq(s, httptest.NewRequest("GET", "/?w=300", nil), token).Code
	}

	// Cross-surface and wrong-key / tampered HMAC deny.
	if code := call(otherSurface, secret); code != 404 {
		t.Fatal("cross-surface", code)
	}
	if code := call(wrongKeyService, secret); code != 404 {
		t.Fatal("wrong server key", code)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(secret)
	flippedMAC := append([]byte(nil), raw...)
	flippedMAC[0] ^= 0x01
	if code := call(service, base64.RawURLEncoding.EncodeToString(flippedMAC)); code != 404 {
		t.Fatal("tampered MAC accepted", code)
	}
	flippedBody := append([]byte(nil), raw...)
	flippedBody[len(flippedBody)-1] ^= 0x01
	if code := call(service, base64.RawURLEncoding.EncodeToString(flippedBody)); code != 404 {
		t.Fatal("tampered ciphertext accepted", code)
	}
	if code := call(service, secret[:len(secret)-2]); code != 404 {
		t.Fatal("truncated secret accepted", code)
	}

	// Another viewer's credential cannot open viewer 1's URL, nor issue for a hidden item.
	otherReq := httptest.NewRequest("GET", "/?w=300", nil)
	otherReq.Header.Set("X-Emby-Token", otherViewerCred)
	if code := callReq(service, otherReq, secret).Code; code != 404 {
		t.Fatal("other viewer X-Emby-Token opened viewer 1 URL", code)
	}
	otherBearer := httptest.NewRequest("GET", "/?w=300", nil)
	otherBearer.Header.Set("Authorization", "Bearer "+otherViewerCred)
	if code := callReq(service, otherBearer, secret).Code; code != 404 {
		t.Fatal("other viewer Bearer token opened viewer 1 URL", code)
	}
	if _, err := service.Issue(ctx, frontend.ImageGrantRequest{Credential: otherViewerCred, Item: core.MovieKey(1), Image: image}); !errors.Is(err, core.ErrUnauthorized) {
		t.Fatal("other viewer issued hidden item", err)
	}

	// Valid serve sets immutable Cache-Control + stable ETag and answers 304 on If-None-Match.
	resp := callReq(service, httptest.NewRequest("GET", "/?w=300", nil), secret)
	if resp.Code != 200 {
		t.Fatal("valid grant", resp.Code)
	}
	if got := resp.Header().Get("Cache-Control"); got != "private, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control=%q", got)
	}
	etag := resp.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	callsBefore304 := source.calls
	condReq := httptest.NewRequest("GET", "/?w=300", nil)
	condReq.Header.Set("If-None-Match", etag)
	if condResp := callReq(service, condReq, secret); condResp.Code != 304 || source.calls != callsBefore304 {
		t.Fatalf("If-None-Match status=%d calls=%d wantCalls=%d", condResp.Code, source.calls, callsBefore304)
	}

	// Non-identity metadata changes keep URL and Serve valid.
	image.Width, image.Height, image.BlurHash = 1200, 1800, "00TI:j"
	image.ThumbHash = "AQIDBA=="
	if code := call(service, secret); code != 200 {
		t.Fatal("metadata update revoked stable artwork", code)
	}

	// Title hidden after issue denies.
	allowed = false
	if code := call(service, secret); code != 404 {
		t.Fatal("policy change ignored", code)
	}
	allowed = true

	// Changed image tag denies old URL and produces a new URL on re-issue.
	image.Tag = "v2"
	if code := call(service, secret); code != 404 {
		t.Fatal("artwork change ignored", code)
	}
	updatedReq := request
	updatedReq.Image = image
	secretV2, err := service.Issue(ctx, updatedReq)
	if err != nil || secretV2 == secret {
		t.Fatalf("expected changed image tag to mint a new URL: old=%q new=%q err=%v", secret, secretV2, err)
	}
	if code := call(service, secretV2); code != 200 {
		t.Fatal("updated tag URL failed", code)
	}
	image.Tag = "v1"

	// Changed subject denies.
	image.Subject = "tmdb:819"
	if code := call(service, secret); code != 404 {
		t.Fatal("credit change ignored", code)
	}
	image.Subject = "tmdb:287"

	// Stateless URLs carry no expiry and remain valid indefinitely while viewer & policy allow.
	now = now.Add(365 * 24 * time.Hour)
	if code := call(service, secret); code != 200 {
		t.Fatal("stateless URL expired", code)
	}

	// Deactivated viewer denies immediately.
	if _, err := db.ExecContext(ctx, `UPDATE auth_users SET active=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if code := call(service, secret); code != 404 {
		t.Fatal("membership ignored", code)
	}
}

func TestImageGrantUsesScopedArtworkAndTransforms(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(db, members{db}, time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	device := core.Device{ID: "tv", Language: "en"}
	credential, err := grants.Issue(ctx, 1, nil, "jellyfin", device)
	if err != nil {
		t.Fatal(err)
	}
	image := frontend.Image{Kind: frontend.ImagePoster, Path: "opaque-reference", Tag: "v1"}
	allowed := true
	experience := frontendtest.Experience{ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
		if !allowed {
			return frontend.ItemDetail{}, core.ErrNotFound
		}
		return frontend.ItemDetail{ImagesAuthoritative: true, Images: []frontend.Image{image}}, nil
	}}
	source := &scopedArtworkFixture{}
	s, err := identity.NewArtworkImageGrantService(db, "jellyfin", grants, func() frontend.Experience { return experience }, source, time.Minute, time.Now, identity.ImageGrantOptions{Token: authhdr.MediaBrowserToken, PublicContentTags: true})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := s.Issue(ctx, frontend.ImageGrantRequest{Credential: credential, Device: device, Item: core.MovieKey(42), Image: image})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.Serve(w, httptest.NewRequest("GET", "/?w=300&h=200&format=png&blur=1", nil), secret)
	if w.Code != 200 || w.Body.String() != "scoped" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("response: %d %q", w.Code, w.Body.String())
	}
	if source.calls != 1 || source.request.Viewer.ID != 1 || source.request.Device != (core.Device{Language: "en"}) || source.request.Item != core.MovieKey(42) || source.image != image || source.transform != (frontend.ArtworkTransform{Width: 300, Height: 200, Format: "png", Blur: true, Fit: true}) {
		t.Fatalf("bad source request: %+v", source)
	}
	w = httptest.NewRecorder()
	s.Serve(w, httptest.NewRequest("GET", "/?h=200&format=png", nil), secret)
	if w.Code != 200 || source.transform.Width != 0 || source.transform.Height != 200 {
		t.Fatalf("height-only request: %+v", source.transform)
	}
	allowed = false
	w = httptest.NewRecorder()
	s.Serve(w, httptest.NewRequest("GET", "/", nil), secret)
	if w.Code != 404 || source.calls != 2 {
		t.Fatal("hidden artwork reached source", w.Code, source.calls)
	}
}

type publicArtworkExperience struct {
	frontendtest.Experience
	visible bool
	image   frontend.Image
}

func (e publicArtworkExperience) PublicArtwork(_ context.Context, _ core.Device, item core.RatingKey) ([]frontend.Image, *core.Viewer, bool, error) {
	if !e.visible || item != core.MovieKey(42) {
		return nil, nil, false, nil
	}
	return []frontend.Image{e.image}, &core.Viewer{ID: 1}, true, nil
}

// ServePublic (#405) writes bytes only for an image the experience
// currently reports as public (visible to every viewer), matched by
// full image identity, and never for person artwork.
func TestServePublicRechecksPublicArtwork(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100000, 0)
	clock := func() time.Time { return now }
	grants, err := identity.NewGrants(db, members{db}, time.Hour, clock)
	if err != nil {
		t.Fatal(err)
	}
	poster := frontend.Image{Kind: frontend.ImagePoster, Path: "/poster", Tag: "v1"}
	experience := &publicArtworkExperience{visible: true, image: poster}
	source := &imageFixture{}
	s, err := identity.NewImageGrantService(db, "jellyfin", grants, func() frontend.Experience { return experience }, source, time.Minute, clock, identity.ImageGrantOptions{Token: authhdr.MediaBrowserToken, PublicContentTags: true})
	if err != nil {
		t.Fatal(err)
	}
	serve := func(item core.RatingKey, img frontend.Image) int {
		w := httptest.NewRecorder()
		s.ServePublic(w, httptest.NewRequest("GET", "/Items/x/Images/Primary", nil), core.Device{}, item, img)
		return w.Code
	}
	if code := serve(core.MovieKey(42), poster); code != 200 || source.calls != 1 {
		t.Fatalf("public poster: %d calls=%d", code, source.calls)
	}
	other := poster
	other.Path = "/other-poster"
	if code := serve(core.MovieKey(42), other); code != 404 || source.calls != 1 {
		t.Fatalf("unlisted image served: %d calls=%d", code, source.calls)
	}
	if code := serve(core.MovieKey(42), frontend.Image{Kind: frontend.ImagePerson, Path: "/poster", Tag: "v1"}); code != 404 {
		t.Fatalf("person image served publicly: %d", code)
	}
	experience.visible = false
	if code := serve(core.MovieKey(42), poster); code != 404 || source.calls != 1 {
		t.Fatalf("hidden-from-someone poster served: %d calls=%d", code, source.calls)
	}
}

// A signed page grant survives elapsed time and a fresh service with the same server image key.
func TestPublicContentTagSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100000, 0)
	clock := func() time.Time { return now }
	grants, err := identity.NewGrants(db, members{db}, time.Hour, clock)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := grants.Issue(ctx, 1, nil, "jellyfin", core.Device{})
	if err != nil {
		t.Fatal(err)
	}
	poster := frontend.Image{Kind: frontend.ImagePoster, Path: "/poster", Tag: "v1"}
	exp := &publicArtworkExperience{visible: true, image: poster}
	exp.Experience = frontendtest.Experience{ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
		if !exp.visible {
			return frontend.ItemDetail{}, core.ErrUnauthorized
		}
		return frontend.ItemDetail{Images: []frontend.Image{exp.image}, ImagesAuthoritative: true}, nil
	}}
	source := &imageFixture{}
	fresh := func() *identity.ImageGrantService {
		s, err := identity.NewImageGrantService(db, "jellyfin", grants, func() frontend.Experience { return exp }, source, time.Minute, clock, identity.ImageGrantOptions{Token: authhdr.MediaBrowserToken, PublicContentTags: true})
		if err != nil {
			t.Fatal(err)
		}
		s.SetImageKey([]byte("persistent-server-key"))
		return s
	}
	page, err := fresh().SignPage(ctx, frontend.ImagePageRequest{Credential: credential, Titles: []core.Title{{Key: core.MovieKey(42)}}})
	if err != nil || len(page) != 1 || len(page[0].Images) != 1 {
		t.Fatalf("page: %+v %v", page, err)
	}
	tag := page[0].Images[0].Secret
	now = now.Add(24 * time.Hour)
	service := fresh()
	serve := func(secret string) int {
		w := httptest.NewRecorder()
		service.Serve(w, httptest.NewRequest("GET", "/Items/"+ids.RatingKeyGUID(core.MovieKey(42))+"/Images/Primary", nil), secret)
		return w.Code
	}
	if code := serve(tag); code != 200 {
		t.Fatalf("cached tag after restart: %d", code)
	}
	exp.image.Tag = "v2"
	if code := serve(tag); code != 404 {
		t.Fatalf("changed content: %d", code)
	}
	exp.image = poster
	exp.visible = false
	if code := serve(tag); code != 404 {
		t.Fatalf("restricted content: %d", code)
	}
}
