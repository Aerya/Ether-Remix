package app

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
)

func makeTestAvatarPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type plexViewerAvatarRoundTripper struct {
	mu            sync.Mutex
	machineID     string
	ownerThumb    string
	kidThumb      string
	avatars       map[string][]byte
	avatarFetches map[string]int
}

func (f *plexViewerAvatarRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	machineID := f.machineID
	ownerThumb := f.ownerThumb
	kidThumb := f.kidThumb
	f.mu.Unlock()

	tok := r.Header.Get("X-Plex-Token")
	reply := func(status int, ct string, body []byte) *http.Response {
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{ct}},
			Body:       io.NopCloser(bytes.NewReader(body)),
			Request:    r,
		}
	}
	switch {
	case r.URL.Path == "/api/v2/user":
		switch tok {
		case "plex-owner-tok":
			payload, _ := json.Marshal(map[string]any{
				"id": 42, "uuid": "u-owner", "username": "owner", "title": "Owner", "thumb": ownerThumb,
			})
			return reply(http.StatusOK, "application/json", payload), nil
		case "switched-300":
			payload, _ := json.Marshal(map[string]any{
				"id": 300, "uuid": "u-kid", "username": "kid", "title": "Kid", "home": true, "restricted": true, "thumb": kidThumb,
			})
			return reply(http.StatusOK, "application/json", payload), nil
		default:
			return reply(http.StatusUnauthorized, "application/json", []byte(`{}`)), nil
		}
	case r.URL.Path == "/api/v2/resources":
		if tok != "plex-owner-tok" {
			return reply(http.StatusUnauthorized, "application/json", []byte(`{}`)), nil
		}
		payload, _ := json.Marshal([]map[string]any{{
			"name": "Ether", "provides": "server", "clientIdentifier": machineID, "owned": true,
		}})
		return reply(http.StatusOK, "application/json", payload), nil
	case r.Method == http.MethodGet && r.URL.Path == "/api/home/users":
		if tok != "plex-owner-tok" && tok != "server-secret-token" {
			return reply(http.StatusUnauthorized, "application/json", []byte(`{}`)), nil
		}
		body := `<MediaContainer size="2">` +
			`<User id="42" uuid="u-owner" username="owner" title="Owner" thumb="` + ownerThumb + `" protected="0" homeAdmin="1"/>` +
			`<User id="300" uuid="u-kid" username="kid" title="Kid" thumb="` + kidThumb + `" protected="0" restricted="1"/>` +
			`</MediaContainer>`
		return reply(http.StatusOK, "application/xml", []byte(body)), nil
	case r.Method == http.MethodPost && r.URL.Path == "/api/home/users/300/switch":
		return reply(http.StatusOK, "application/xml", []byte(`<MediaContainer><User id="300" authenticationToken="switched-300"/></MediaContainer>`)), nil
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/users/") && strings.HasSuffix(r.URL.Path, "/avatar"):
		if tok != "plex-owner-tok" && tok != "server-secret-token" {
			return reply(http.StatusUnauthorized, "application/json", []byte(`{}`)), nil
		}
		key := r.URL.Path + "?" + r.URL.RawQuery
		f.mu.Lock()
		f.avatarFetches[key]++
		body, ok := f.avatars[key]
		f.mu.Unlock()
		if !ok {
			return reply(http.StatusNotFound, "text/plain", []byte("missing")), nil
		}
		return reply(http.StatusOK, "image/png", body), nil
	default:
		return reply(http.StatusNotFound, "application/json", []byte(`{}`)), nil
	}
}

func (f *plexViewerAvatarRoundTripper) fetches(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.avatarFetches[key]
}

func TestPlexViewerAvatarEndToEndSharedCache(t *testing.T) {
	pngV1 := makeTestAvatarPNG(t, 256, 256, color.RGBA{R: 20, G: 120, B: 220, A: 255})
	pngV2 := makeTestAvatarPNG(t, 256, 256, color.RGBA{R: 220, G: 60, B: 40, A: 255})
	pngKid := makeTestAvatarPNG(t, 200, 200, color.RGBA{R: 40, G: 190, B: 90, A: 255})

	fake := &plexViewerAvatarRoundTripper{
		ownerThumb: "https://plex.tv/users/owner/avatar?c=1",
		kidThumb:   "https://plex.tv/users/kid/avatar?c=9",
		avatars: map[string][]byte{
			"/users/owner/avatar?c=1": pngV1,
			"/users/owner/avatar?c=2": pngV2,
			"/users/kid/avatar?c=9":   pngKid,
		},
		avatarFetches: map[string]int{},
	}

	a := setupTestAppConfigured(t, true, Options{
		plextvHTTP: &http.Client{Transport: fake},
	}, func(c *config.Config) {
		c.Plex.Claimed = false
		c.Plex.OwnerPlexID = 0
	})
	fake.mu.Lock()
	fake.machineID = a.cfg.Read().Server.MachineIdentifier
	fake.mu.Unlock()
	ctx := t.Context()

	if err := a.cfg.Update(func(c *config.Config) error {
		c.Plex.Claimed = true
		c.Plex.OwnerPlexID = 42
		c.Plex.OwnerUsername = "owner"
		c.Plex.Token = "server-secret-token"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.users.SetOwner(ctx, plextv.Account{
		ID: 42, UUID: "u-owner", Username: "owner", Title: "Owner",
		Thumb: "https://plex.tv/users/owner/avatar?c=1",
	}, "server-secret-token"); err != nil {
		t.Fatal(err)
	}
	if err := a.users.SyncFriends(ctx, []plextv.Friend{
		{Owned: true, PlexID: 42, Token: "owner-synced-token"},
		{PlexID: 300, UUID: "u-kid", Username: "kid", Title: "Kid", Token: "kid-synced-token", Home: true, Restricted: true, AllLibs: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.frontends.control.listeners.start(ctx, "vela"); err != nil {
		t.Fatal(err)
	}
	if err := a.frontends.control.listeners.start(ctx, "jellyfin"); err != nil {
		t.Fatal(err)
	}

	velaInst, ok := a.frontends.registry.Instance("vela")
	if !ok {
		t.Fatal("vela not mounted")
	}
	jfInst, ok := a.frontends.registry.Instance("jellyfin")
	if !ok {
		t.Fatal("jellyfin not mounted")
	}

	velaReq := func(method, path, body, bearer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "198.51.100.20:54321"
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
			req.Header.Set("X-Vela-Device-Id", "dev-avatar")
		}
		w := httptest.NewRecorder()
		velaInst.Handler().ServeHTTP(w, req)
		return w
	}

	type avatarRef struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Tag   string `json:"tag"`
		Sizes []struct {
			Width  int    `json:"width"`
			Height int    `json:"height"`
			URL    string `json:"url"`
		} `json:"sizes"`
	}

	// 1. Plex sign-in populates Tokens.viewer.avatar.
	w := velaReq(http.MethodPost, "/vela/v0/auth/plex", `{"idempotencyKey":"idem-owner-signin","plexToken":"plex-owner-tok","device":{"id":"dev-avatar","name":"Phone"}}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("plex sign-in: %d %s", w.Code, w.Body.String())
	}
	var signIn struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		Viewer       struct {
			Avatar *avatarRef `json:"avatar"`
		} `json:"viewer"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &signIn); err != nil {
		t.Fatal(err)
	}
	if signIn.Viewer.Avatar == nil || signIn.Viewer.Avatar.Kind != "person" || len(signIn.Viewer.Avatar.Sizes) != 1 {
		t.Fatalf("expected Tokens.viewer.avatar on sign-in, got %+v", signIn.Viewer.Avatar)
	}

	// 2. Refresh also populates Tokens.viewer.avatar.
	wRef := velaReq(http.MethodPost, "/vela/v0/auth/refresh", `{"idempotencyKey":"idem-owner-refresh","refreshToken":"`+signIn.RefreshToken+`","device":{"id":"dev-avatar","name":"Phone"}}`, "")
	if wRef.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", wRef.Code, wRef.Body.String())
	}
	var refreshed struct {
		AccessToken string `json:"accessToken"`
		Viewer      struct {
			Avatar *avatarRef `json:"avatar"`
		} `json:"viewer"`
	}
	if err := json.Unmarshal(wRef.Body.Bytes(), &refreshed); err != nil {
		t.Fatal(err)
	}
	if refreshed.Viewer.Avatar == nil || refreshed.Viewer.Avatar.Tag != signIn.Viewer.Avatar.Tag {
		t.Fatalf("expected matching avatar on refresh: got %+v want tag %q", refreshed.Viewer.Avatar, signIn.Viewer.Avatar.Tag)
	}

	// 3. GET /vela/v0/bootstrap returns viewer.avatar; plain GET without Authorization serves PNG and resizes ?w=64.
	wBoot1 := velaReq(http.MethodGet, "/vela/v0/bootstrap", "", refreshed.AccessToken)
	if wBoot1.Code != http.StatusOK {
		t.Fatalf("bootstrap 1: %d %s", wBoot1.Code, wBoot1.Body.String())
	}
	etag1 := wBoot1.Header().Get("ETag")
	var boot1 struct {
		Viewer struct {
			Avatar *avatarRef `json:"avatar"`
		} `json:"viewer"`
	}
	if err := json.Unmarshal(wBoot1.Body.Bytes(), &boot1); err != nil {
		t.Fatal(err)
	}
	if boot1.Viewer.Avatar == nil || len(boot1.Viewer.Avatar.Sizes) != 1 {
		t.Fatalf("bootstrap missing viewer.avatar: %s", wBoot1.Body.String())
	}
	avURL1 := boot1.Viewer.Avatar.Sizes[0].URL

	// Original size via plain GET (no Authorization header).
	img1 := velaReq(http.MethodGet, avURL1, "", "")
	if img1.Code != http.StatusOK || img1.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("GET %s: %d ct=%q", avURL1, img1.Code, img1.Header().Get("Content-Type"))
	}
	cfg1, _, err := image.DecodeConfig(bytes.NewReader(img1.Body.Bytes()))
	if err != nil || cfg1.Width != 256 || cfg1.Height != 256 {
		t.Fatalf("decoded original avatar = %dx%d err=%v, want 256x256", cfg1.Width, cfg1.Height, err)
	}

	// Resized via ?w=64 without Authorization header — hits shared image cache, no second upstream fetch.
	resizedURL := strings.Replace(avURL1, "?w=0", "?w=64", 1)
	imgResized := velaReq(http.MethodGet, resizedURL, "", "")
	if imgResized.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", resizedURL, imgResized.Code, imgResized.Body.String())
	}
	cfgSmall, _, err := image.DecodeConfig(bytes.NewReader(imgResized.Body.Bytes()))
	if err != nil || cfgSmall.Width != 64 || cfgSmall.Height != 64 {
		t.Fatalf("decoded resized avatar = %dx%d err=%v, want 64x64", cfgSmall.Width, cfgSmall.Height, err)
	}

	// 4. Jellyfin UserDto.PrimaryImageTag + /Users/{userId}/Images/Primary share the exact same cached source.
	owner, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	jfGrants, err := identity.NewGrants(a.db, a.users, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	jfToken, err := jfGrants.Issue(ctx, owner.ID, nil, "jellyfin", core.Device{ID: "jf-dev"})
	if err != nil {
		t.Fatal(err)
	}
	meReq := httptest.NewRequest(http.MethodGet, "/Users/Me", nil)
	meReq.Header.Set("Authorization", `MediaBrowser Token="`+jfToken+`", DeviceId="jf-dev"`)
	meRec := httptest.NewRecorder()
	jfInst.Handler().ServeHTTP(meRec, meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("jellyfin /Users/Me: %d %s", meRec.Code, meRec.Body.String())
	}
	var jfUser struct {
		ID              string `json:"Id"`
		PrimaryImageTag string `json:"PrimaryImageTag"`
	}
	if err := json.Unmarshal(meRec.Body.Bytes(), &jfUser); err != nil {
		t.Fatal(err)
	}
	if jfUser.PrimaryImageTag == "" {
		t.Fatalf("expected Jellyfin UserDto.PrimaryImageTag for Plex-linked viewer, got %s", meRec.Body.String())
	}

	// Plain GET with ?tag=... and ?maxWidth=96 (no Authorization header) reuses the cached avatar.
	jfImgReq := httptest.NewRequest(http.MethodGet, "/Users/"+jfUser.ID+"/Images/Primary?tag="+jfUser.PrimaryImageTag+"&maxWidth=96", nil)
	jfImgRec := httptest.NewRecorder()
	jfInst.Handler().ServeHTTP(jfImgRec, jfImgReq)
	if jfImgRec.Code != http.StatusOK {
		t.Fatalf("jellyfin user primary image: %d %s", jfImgRec.Code, jfImgRec.Body.String())
	}
	cfgJF, _, err := image.DecodeConfig(bytes.NewReader(jfImgRec.Body.Bytes()))
	if err != nil || cfgJF.Width != 96 || cfgJF.Height != 96 {
		t.Fatalf("decoded jellyfin avatar = %dx%d err=%v, want 96x96", cfgJF.Width, cfgJF.Height, err)
	}
	// Wrong user or wrong route must reject the avatar grant.
	otherUserID, _ := ids.GUID(ids.User, owner.ID+999)
	wrongUser := httptest.NewRecorder()
	jfInst.Handler().ServeHTTP(wrongUser, httptest.NewRequest(http.MethodGet, "/Users/"+otherUserID+"/Images/Primary?tag="+jfUser.PrimaryImageTag, nil))
	if wrongUser.Code != http.StatusNotFound {
		t.Fatalf("mismatched userId got %d, want 404", wrongUser.Code)
	}
	wrongRoute := httptest.NewRecorder()
	jfInst.Handler().ServeHTTP(wrongRoute, httptest.NewRequest(http.MethodGet, "/Items/"+jfUser.ID+"/Images/Primary?tag="+jfUser.PrimaryImageTag, nil))
	if wrongRoute.Code != http.StatusNotFound {
		t.Fatalf("item route with viewer avatar tag got %d, want 404", wrongRoute.Code)
	}

	if got := fake.fetches("/users/owner/avatar?c=1"); got != 1 {
		t.Fatalf("owner avatar v1 upstream fetches = %d, want 1 across Vela + resize + Jellyfin", got)
	}

	// 5. Plex Home profile sign-in populates the kid's thumb from plex.tv, shares the cache with the picker,
	//    and returns viewer.avatar on Tokens.viewer and bootstrap.
	selRec := velaReq(http.MethodPost, "/vela/v0/auth/plex", `{"idempotencyKey":"idem-kid-select","plexToken":"plex-owner-tok","selectProfile":true,"device":{"id":"dev-kid","name":"TV"}}`, "")
	if selRec.Code != http.StatusOK {
		t.Fatalf("selectProfile: %d %s", selRec.Code, selRec.Body.String())
	}
	var sel struct {
		SelectionToken string `json:"selectionToken"`
		Profiles       []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			AvatarURL string `json:"avatarUrl"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(selRec.Body.Bytes(), &sel); err != nil {
		t.Fatal(err)
	}
	var kidID, kidPickerURL string
	for _, p := range sel.Profiles {
		if p.Name == "Kid" {
			kidID, kidPickerURL = p.ID, p.AvatarURL
		}
	}
	if kidID == "" || kidPickerURL == "" {
		t.Fatalf("expected Kid profile with picker avatarUrl, got %+v", sel.Profiles)
	}
	// Load picker avatar first — fetches kid avatar once into shared cache.
	if wPicker := velaReq(http.MethodGet, kidPickerURL, "", ""); wPicker.Code != http.StatusOK {
		t.Fatalf("picker avatar: %d", wPicker.Code)
	}
	// Complete Plex Home sign-in as Kid.
	kidSignInRec := velaReq(http.MethodPost, "/vela/v0/auth/plex/profile", `{"idempotencyKey":"idem-kid-complete","selectionToken":"`+sel.SelectionToken+`","profileId":"`+kidID+`","device":{"id":"dev-kid","name":"TV"}}`, "")
	if kidSignInRec.Code != http.StatusOK {
		t.Fatalf("kid profile complete: %d %s", kidSignInRec.Code, kidSignInRec.Body.String())
	}
	var kidTok struct {
		AccessToken string `json:"accessToken"`
		Viewer      struct {
			Avatar *avatarRef `json:"avatar"`
		} `json:"viewer"`
	}
	if err := json.Unmarshal(kidSignInRec.Body.Bytes(), &kidTok); err != nil {
		t.Fatal(err)
	}
	if kidTok.Viewer.Avatar == nil || len(kidTok.Viewer.Avatar.Sizes) != 1 {
		t.Fatalf("expected kid Tokens.viewer.avatar after Plex Home sign-in, got %+v", kidTok.Viewer.Avatar)
	}
	// Fetch Kid's bootstrap avatar — hits shared cache warmed by picker (still 1 upstream fetch!).
	if wKidImg := velaReq(http.MethodGet, kidTok.Viewer.Avatar.Sizes[0].URL, "", ""); wKidImg.Code != http.StatusOK {
		t.Fatalf("kid viewer avatar: %d", wKidImg.Code)
	}
	if got := fake.fetches("/users/kid/avatar?c=9"); got != 1 {
		t.Fatalf("kid avatar upstream fetches = %d, want 1 shared between picker and viewer avatar", got)
	}

	// 6. Changing the owner's Plex avatar changes tag, invalidates bootstrap ETag (200 not 304),
	//    and revokes the old avatar URL.
	fake.mu.Lock()
	fake.ownerThumb = "https://plex.tv/users/owner/avatar?c=2"
	fake.mu.Unlock()
	if err := a.users.SyncPlexThumbs(ctx, map[int64]string{42: "https://plex.tv/users/owner/avatar?c=2"}); err != nil {
		t.Fatal(err)
	}

	boot2Req := httptest.NewRequest(http.MethodGet, "/vela/v0/bootstrap", nil)
	boot2Req.RemoteAddr = "198.51.100.20:54321"
	boot2Req.Header.Set("Authorization", "Bearer "+refreshed.AccessToken)
	boot2Req.Header.Set("X-Vela-Device-Id", "dev-avatar")
	boot2Req.Header.Set("If-None-Match", etag1)
	wBoot2 := httptest.NewRecorder()
	velaInst.Handler().ServeHTTP(wBoot2, boot2Req)
	if wBoot2.Code != http.StatusOK {
		t.Fatalf("bootstrap after Plex avatar change: got %d, want 200 (not stale 304)", wBoot2.Code)
	}
	if wBoot2.Header().Get("ETag") == etag1 {
		t.Fatalf("bootstrap ETag did not change after Plex avatar update: %q", etag1)
	}
	var boot2 struct {
		Viewer struct {
			Avatar *avatarRef `json:"avatar"`
		} `json:"viewer"`
	}
	if err := json.Unmarshal(wBoot2.Body.Bytes(), &boot2); err != nil {
		t.Fatal(err)
	}
	if boot2.Viewer.Avatar == nil || boot2.Viewer.Avatar.Tag == boot1.Viewer.Avatar.Tag {
		t.Fatalf("expected new avatar tag after Plex change: old=%+v new=%+v", boot1.Viewer.Avatar, boot2.Viewer.Avatar)
	}

	// Old avatar URL is rejected once the viewer's avatar changed.
	if wOld := velaReq(http.MethodGet, avURL1, "", ""); wOld.Code != http.StatusNotFound {
		t.Fatalf("old avatar URL after change: got %d, want 404", wOld.Code)
	}
	// New avatar URL serves v2 bytes and fetches upstream once.
	wNew := velaReq(http.MethodGet, boot2.Viewer.Avatar.Sizes[0].URL, "", "")
	if wNew.Code != http.StatusOK {
		t.Fatalf("new avatar URL: %d %s", wNew.Code, wNew.Body.String())
	}
	if got := fake.fetches("/users/owner/avatar?c=2"); got != 1 {
		t.Fatalf("owner avatar v2 upstream fetches = %d, want 1", got)
	}
}
