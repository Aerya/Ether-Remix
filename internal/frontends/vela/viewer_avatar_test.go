package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/store"
)

type avatarBootstrapExperience struct {
	frontendtest.Experience
}

func (avatarBootstrapExperience) HomeRevision(context.Context, *frontend.Viewer) (frontend.HomeRevision, bool, error) {
	return frontend.HomeRevision{Viewer: "stable", Policy: 1}, true, nil
}

type rotatingAvatarImageGrants struct {
	mu      sync.Mutex
	gen     int
	served  map[string]string
	lastReq *http.Request
}

func (g *rotatingAvatarImageGrants) Issue(_ context.Context, req frontend.ImageGrantRequest) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !strings.HasPrefix(req.Image.Path, frontend.ViewerAvatarPrefix) {
		return "other-" + req.Image.Tag, nil
	}
	secret := "avatar-g" + string(rune('0'+g.gen)) + "-" + req.Image.Tag
	if g.served == nil {
		g.served = map[string]string{}
	}
	g.served[secret] = req.Image.Path
	return secret, nil
}

func (g *rotatingAvatarImageGrants) Revoke(context.Context, string, string) error { return nil }

func (g *rotatingAvatarImageGrants) Serve(w http.ResponseWriter, r *http.Request, secret string) {
	g.mu.Lock()
	_, ok := g.served[secret]
	g.lastReq = r
	g.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("png-bytes"))
}

func TestVelaBootstrapViewerAvatarAndValidator(t *testing.T) {
	db := store.OpenTest(t)

	var viewerMu sync.Mutex
	plexViewer := frontend.Viewer{
		ID:       1,
		PlexID:   42,
		Username: "owner",
		Thumb:    "https://plex.tv/users/owner/avatar?c=1",
	}
	localViewer := frontend.Viewer{
		ID:       2,
		Username: "local",
	}

	resolve := func(token string) (*frontend.Viewer, error) {
		viewerMu.Lock()
		defer viewerMu.Unlock()
		switch token {
		case "plex-access":
			cp := plexViewer
			return &cp, nil
		case "local-access":
			cp := localViewer
			return &cp, nil
		default:
			return nil, frontend.ErrUnauthorized
		}
	}

	ident := plexAuthTestIdentity{
		scope: "avatar",
		Identity: frontendtest.Identity{
			AuthenticationFeaturesFunc: func() frontend.AuthenticationFeatures {
				return frontend.AuthenticationFeatures{Password: true, PlexAccountSignIn: true}
			},
			ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
				return resolve(token)
			},
			ResolveDeviceFunc: func(_ context.Context, token string, _ frontend.Device) (*frontend.Viewer, time.Time, error) {
				v, err := resolve(token)
				return v, time.Now().Add(time.Hour), err
			},
			SignInFunc: func(_ context.Context, username, _ string, _ frontend.Device) (string, error) {
				if username == "owner" {
					return "plex-access", nil
				}
				return "local-access", nil
			},
			SignInWithPlexFunc: func(context.Context, string, frontend.Device) (string, error) {
				return "plex-access", nil
			},
			RefreshFunc: func(context.Context, string, frontend.Device) (string, string, error) {
				return "plex-access", "refresh-2", nil
			},
			IssueRefreshFunc: func(context.Context, string, frontend.Device) (string, error) {
				return "refresh-1", nil
			},
		},
	}

	grants := &rotatingAvatarImageGrants{gen: 1}
	exp := avatarBootstrapExperience{
		Experience: frontendtest.Experience{
			RowsFunc: func(context.Context, frontend.RowRequest) ([]frontend.Row, error) { return nil, nil },
		},
	}

	inst, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(context.Background(), frontend.Host{
		Identity:    ident,
		Experience:  exp,
		Sessions:    frontendtest.Sessions{},
		ImageGrants: grants,
		PageFreshness: func() frontend.PageFreshness {
			return frontend.PageFreshness{}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())

	type artworkSize struct {
		Width  int    `json:"width"`
		Height int    `json:"height"`
		URL    string `json:"url"`
	}
	type artworkRef struct {
		ID    string        `json:"id"`
		Kind  string        `json:"kind"`
		Tag   string        `json:"tag"`
		Sizes []artworkSize `json:"sizes"`
	}

	getBootstrap := func(token, ifNoneMatch string) (*httptest.ResponseRecorder, map[string]any, *artworkRef) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/vela/v0/bootstrap", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Vela-Device-Id", "dev-1")
		if ifNoneMatch != "" {
			req.Header.Set("If-None-Match", ifNoneMatch)
		}
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, req)
		if w.Code == http.StatusNotModified {
			return w, nil, nil
		}
		if w.Code != http.StatusOK {
			t.Fatalf("bootstrap status=%d body=%s", w.Code, w.Body.String())
		}
		var doc map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatalf("decode bootstrap: %v", err)
		}
		viewer, _ := doc["viewer"].(map[string]any)
		rawAvatar, exists := viewer["avatar"]
		if !exists {
			return w, viewer, nil
		}
		b, _ := json.Marshal(rawAvatar)
		var ref artworkRef
		if err := json.Unmarshal(b, &ref); err != nil {
			t.Fatalf("decode viewer.avatar: %v", err)
		}
		return w, viewer, &ref
	}

	// 1. Plex-linked viewer receives V.ArtworkRef on bootstrap.
	w1, _, av1 := getBootstrap("plex-access", "")
	etag1 := w1.Header().Get("ETag")
	if etag1 == "" {
		t.Fatal("missing bootstrap ETag")
	}
	if av1 == nil {
		t.Fatal("expected viewer.avatar on bootstrap for Plex-linked viewer")
	}
	if av1.ID == "" || av1.Kind != frontend.ImagePerson || av1.Tag == "" || len(av1.Sizes) != 1 {
		t.Fatalf("unexpected viewer.avatar shape: %+v", av1)
	}
	u1 := av1.Sizes[0].URL
	if !strings.HasPrefix(u1, "/vela/v0/images/") || strings.HasPrefix(u1, "/vela/v0/images/"+plexAvatarPrefix) {
		t.Fatalf("avatar url must use stateless /vela/v0/images grant, got %q", u1)
	}
	if strings.Contains(u1, "plex.tv") || strings.Contains(av1.Tag, "plex.tv") {
		t.Fatalf("raw plex.tv URL leaked in avatar: %+v", av1)
	}

	// Plain GET without Authorization loads the avatar and forwards ?w=<px>.
	resizedURL := strings.Replace(u1, "?w=0", "?w=64", 1)
	imgReq := httptest.NewRequest(http.MethodGet, resizedURL, nil)
	imgRec := httptest.NewRecorder()
	inst.Handler().ServeHTTP(imgRec, imgReq)
	if imgRec.Code != http.StatusOK || imgRec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("GET %s: status=%d ct=%q", resizedURL, imgRec.Code, imgRec.Header().Get("Content-Type"))
	}
	grants.mu.Lock()
	gotWidth := grants.lastReq.URL.Query().Get("w")
	grants.mu.Unlock()
	if gotWidth != "64" {
		t.Fatalf("expected w=64 forwarded to image grant service, got %q", gotWidth)
	}

	// 2. Unchanged bootstrap answers 304 Not Modified.
	if w := func() *httptest.ResponseRecorder {
		rec, _, _ := getBootstrap("plex-access", etag1)
		return rec
	}(); w.Code != http.StatusNotModified {
		t.Fatalf("unchanged bootstrap with If-None-Match: status=%d, want 304", w.Code)
	}

	// 3. Re-issued avatar URL changes the bootstrap validator (no stale 304).
	grants.mu.Lock()
	grants.gen = 2
	grants.mu.Unlock()
	w2, _, av2 := getBootstrap("plex-access", etag1)
	if w2.Code != http.StatusOK {
		t.Fatalf("re-issued avatar URL must not return 304: status=%d", w2.Code)
	}
	etag2 := w2.Header().Get("ETag")
	if etag2 == "" || etag2 == etag1 {
		t.Fatalf("bootstrap ETag did not change on re-issued avatar URL: old=%q new=%q", etag1, etag2)
	}
	if av2 == nil || av2.Sizes[0].URL == u1 {
		t.Fatalf("expected new avatar URL after grant rotation: old=%q new=%+v", u1, av2)
	}

	// 4. Changing the viewer's Plex avatar changes tag, id, url, and ETag.
	viewerMu.Lock()
	plexViewer.Thumb = "https://plex.tv/users/owner/avatar?c=2"
	viewerMu.Unlock()
	w3, _, av3 := getBootstrap("plex-access", etag2)
	if w3.Code != http.StatusOK {
		t.Fatalf("changed Plex avatar must not return 304: status=%d", w3.Code)
	}
	etag3 := w3.Header().Get("ETag")
	if etag3 == "" || etag3 == etag2 {
		t.Fatalf("bootstrap ETag did not change when Plex avatar changed: old=%q new=%q", etag2, etag3)
	}
	if av3 == nil || av3.Tag == av2.Tag || av3.ID == av2.ID || av3.Sizes[0].URL == av2.Sizes[0].URL {
		t.Fatalf("expected new avatar tag/id/url: old=%+v new=%+v", av2, av3)
	}

	// 5. Tokens.viewer includes avatar on sign-in, Plex sign-in, and refresh.
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{"password", "/vela/v0/auth/sign-in", `{"idempotencyKey":"idem-password","username":"owner","password":"pw","device":{"id":"dev-1","name":"Phone"}}`},
		{"plex", "/vela/v0/auth/plex", `{"idempotencyKey":"idem-plex","plexToken":"ptok","device":{"id":"dev-1","name":"Phone"}}`},
		{"refresh", "/vela/v0/auth/refresh", `{"idempotencyKey":"idem-refresh","refreshToken":"refresh-1","device":{"id":"dev-1","name":"Phone"}}`},
	} {
		t.Run("tokens_"+tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			inst.Handler().ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("%s: status=%d body=%s", tc.path, w.Code, w.Body.String())
			}
			var tok struct {
				Viewer struct {
					Avatar *artworkRef `json:"avatar"`
				} `json:"viewer"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &tok); err != nil {
				t.Fatalf("%s decode: %v", tc.path, err)
			}
			if tok.Viewer.Avatar == nil || tok.Viewer.Avatar.Tag != av3.Tag || tok.Viewer.Avatar.Kind != frontend.ImagePerson {
				t.Fatalf("%s: unexpected Tokens.viewer.avatar=%+v, want tag=%q", tc.path, tok.Viewer.Avatar, av3.Tag)
			}
		})
	}

	// 6. Clearing the Plex avatar omits viewer.avatar and changes ETag.
	viewerMu.Lock()
	plexViewer.Thumb = ""
	viewerMu.Unlock()
	w4, viewer4, av4 := getBootstrap("plex-access", etag3)
	if w4.Code != http.StatusOK || w4.Header().Get("ETag") == etag3 {
		t.Fatalf("clearing avatar must invalidate bootstrap ETag: code=%d etag=%q", w4.Code, w4.Header().Get("ETag"))
	}
	if av4 != nil {
		t.Fatalf("expected avatar omitted when thumb is empty, got %+v", av4)
	}
	if _, exists := viewer4["avatar"]; exists {
		t.Fatalf("avatar key must be omitted from JSON when empty: %+v", viewer4)
	}

	// 7. Unlinked local viewer omits viewer.avatar entirely.
	_, localDoc, localAv := getBootstrap("local-access", "")
	if localAv != nil {
		t.Fatalf("expected no avatar for unlinked local viewer, got %+v", localAv)
	}
	if _, exists := localDoc["avatar"]; exists {
		t.Fatalf("avatar key must be omitted for local viewer: %+v", localDoc)
	}
}
