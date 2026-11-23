package mediabrowser

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/conform/openapi"
	"github.com/braedonsaunders/ether/internal/conform/openapi/spectest"
	"github.com/braedonsaunders/ether/internal/diagnostics"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type memAPIKey struct {
	info    frontend.APIKeyInfo
	hash    string
	revoked bool
}

type memAPIKeyIdentity struct {
	frontendtest.Identity
	mu     sync.Mutex
	owner  *frontend.Viewer
	member *frontend.Viewer
	nextID int64
	keys   []*memAPIKey
	now    func() time.Time
}

func (m *memAPIKeyIdentity) Resolve(_ context.Context, token string) (*frontend.Viewer, error) {
	switch token {
	case "owner-grant":
		u := *m.owner
		return &u, nil
	case "owner-grant-2":
		u := *m.owner
		return &u, nil
	case "member-grant":
		u := *m.member
		return &u, nil
	default:
		return nil, frontend.ErrUnauthorized
	}
}

func (m *memAPIKeyIdentity) ResolveAPIKey(_ context.Context, key string) (*frontend.Viewer, frontend.APIKeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sum := sha256.Sum256([]byte(key))
	h := hex.EncodeToString(sum[:])
	for _, k := range m.keys {
		if !k.revoked && k.hash == h {
			k.info.LastUsed = m.now()
			u := *m.owner
			u.Delegated = false
			return &u, k.info, nil
		}
	}
	return nil, frontend.APIKeyInfo{}, frontend.ErrUnauthorized
}

func (m *memAPIKeyIdentity) ListAPIKeys(_ context.Context) ([]frontend.APIKeyInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []frontend.APIKeyInfo{}
	for _, k := range m.keys {
		if !k.revoked {
			out = append(out, k.info)
		}
	}
	return out, nil
}

func (m *memAPIKeyIdentity) CreateAPIKey(_ context.Context, appName string) (frontend.APIKeyInfo, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	secret := "ek1_" + base64.RawURLEncoding.EncodeToString(raw[:])
	sum := sha256.Sum256([]byte(secret))
	m.nextID++
	info := frontend.APIKeyInfo{
		ID:        m.nextID,
		Handle:    fmt.Sprintf("hidden-%016x", m.nextID),
		AppName:   appName,
		CreatedAt: m.now(),
	}
	m.keys = append(m.keys, &memAPIKey{info: info, hash: hex.EncodeToString(sum[:])})
	return info, secret, nil
}

func (m *memAPIKeyIdentity) RevokeAPIKey(_ context.Context, keyOrHandle string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sum := sha256.Sum256([]byte(keyOrHandle))
	h := hex.EncodeToString(sum[:])
	for _, k := range m.keys {
		if !k.revoked && (k.hash == h || k.info.Handle == keyOrHandle) {
			k.revoked = true
			return k.info.ID, nil
		}
	}
	return 0, nil
}

type memViewerAdmin struct {
	mu        sync.Mutex
	nextID    int64
	viewers   map[int64]*frontend.ManagedViewer
	passwords map[int64]string
}

func newMemViewerAdmin(owner, member, plexViewer *frontend.Viewer) *memViewerAdmin {
	m := &memViewerAdmin{
		nextID:    10,
		viewers:   map[int64]*frontend.ManagedViewer{},
		passwords: map[int64]string{},
	}
	m.viewers[owner.ID] = &frontend.ManagedViewer{User: *owner, Active: true, OwnerEnabled: true, Origin: "owner", HasLocalPassword: true}
	m.passwords[owner.ID] = "owner-password-123"
	if member != nil {
		m.viewers[member.ID] = &frontend.ManagedViewer{User: *member, Active: true, OwnerEnabled: true, Origin: "local", HasLocalPassword: true}
		m.passwords[member.ID] = "member-password-123"
	}
	if plexViewer != nil {
		m.viewers[plexViewer.ID] = &frontend.ManagedViewer{User: *plexViewer, Active: true, OwnerEnabled: true, HasPlex: true, Origin: "friend"}
	}
	return m
}

func (m *memViewerAdmin) ListViewers(context.Context) ([]frontend.ManagedViewer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]frontend.ManagedViewer, 0, len(m.viewers))
	for _, v := range m.viewers {
		cp := *v
		cp.User.AllowedLibraries = append([]frontend.LibraryID(nil), v.User.AllowedLibraries...)
		out = append(out, cp)
	}
	return out, nil
}

func (m *memViewerAdmin) GetViewer(_ context.Context, id int64) (*frontend.ManagedViewer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[id]
	if !ok {
		return nil, frontend.ErrNotFound
	}
	cp := *v
	cp.User.AllowedLibraries = append([]frontend.LibraryID(nil), v.User.AllowedLibraries...)
	return &cp, nil
}

func (m *memViewerAdmin) CreateLocalViewer(_ context.Context, username, password string) (*frontend.ManagedViewer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if password != "" && len(password) < 12 {
		return nil, frontend.ErrPasswordTooShort
	}
	for _, existing := range m.viewers {
		if strings.EqualFold(existing.User.Username, username) {
			return nil, frontend.ErrViewerExists
		}
	}
	m.nextID++
	id := m.nextID
	mv := &frontend.ManagedViewer{
		User:             frontend.User{ID: id, Username: username, Title: username},
		Active:           true,
		OwnerEnabled:     true,
		Origin:           "local",
		HasLocalPassword: password != "",
	}
	m.viewers[id] = mv
	if password != "" {
		m.passwords[id] = password
	}
	cp := *mv
	return &cp, nil
}

func (m *memViewerAdmin) UpdateViewerPolicy(_ context.Context, id int64, update frontend.ViewerPolicyUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[id]
	if !ok {
		return frontend.ErrNotFound
	}
	if update.Paused != nil {
		if v.User.IsOwner && *update.Paused {
			return frontend.ErrViewerProtected
		}
		v.OwnerEnabled = !*update.Paused
		v.Active = !*update.Paused
	}
	if update.AllowedLibraries != nil {
		v.User.AllowedLibraries = append([]frontend.LibraryID(nil), (*update.AllowedLibraries)...)
	}
	if update.Hidden != nil {
		v.User.Hidden = *update.Hidden
	}
	return nil
}

func (m *memViewerAdmin) SetViewerPassword(_ context.Context, id int64, password string, reset bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[id]
	if !ok {
		return frontend.ErrNotFound
	}
	if reset && password == "" {
		if v.User.IsOwner {
			return frontend.ErrViewerProtected
		}
		delete(m.passwords, id)
		v.HasLocalPassword = false
		return nil
	}
	if len(password) < 12 {
		return frontend.ErrPasswordTooShort
	}
	m.passwords[id] = password
	v.HasLocalPassword = true
	return nil
}

func (m *memViewerAdmin) VerifyViewerPassword(_ context.Context, id int64, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.passwords[id] == "" || m.passwords[id] != password {
		return frontend.ErrUnauthorized
	}
	return nil
}

func (m *memViewerAdmin) RemoveViewer(_ context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[id]
	if !ok {
		return frontend.ErrNotFound
	}
	if v.User.IsOwner {
		return frontend.ErrViewerProtected
	}
	if v.HasPlex {
		return frontend.ErrViewerPlexManaged
	}
	delete(m.viewers, id)
	delete(m.passwords, id)
	return nil
}

func mountAdminTestInstance(t *testing.T, protocol frontend.Protocol, logBuf *bytes.Buffer, rec *diagnostics.Recorder) (frontend.Instance, *memAPIKeyIdentity, *memViewerAdmin) {
	t.Helper()
	now := time.Date(2026, 11, 22, 19, 0, 0, 0, time.UTC)
	owner := &frontend.Viewer{ID: 1, Username: "owner", IsOwner: true}
	member := &frontend.Viewer{ID: 2, Username: "member", AllowedLibraries: []frontend.LibraryID{1}}
	plexViewer := &frontend.Viewer{ID: 3, Username: "plex_friend", PlexID: 9001}
	ident := &memAPIKeyIdentity{owner: owner, member: member, now: func() time.Time { return now }}
	admin := newMemViewerAdmin(owner, member, plexViewer)
	exp := frontendtest.Experience{
		ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
			return []frontend.View{
				{ID: "1", LibraryID: 1, Name: "Movies", Type: "movie"},
				{ID: "2", LibraryID: 2, Name: "Shows", Type: "show"},
			}, nil
		},
	}
	var logger *slog.Logger
	if logBuf != nil {
		logger = slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	host := frontend.Host{
		Identity:    ident,
		ViewerAdmin: admin,
		Experience:  exp,
		Support:     rec,
		Log:         logger,
		Now:         func() time.Time { return now },
		Config:      func() frontend.Settings { return frontend.Settings{"publicViewerPicker": true} },
	}
	var (
		inst frontend.Instance
		err  error
	)
	if protocol == "emby" {
		inst, err = (Emby{MachineID: "test-emby"}).Mount(context.Background(), host)
	} else {
		inst, err = (Jellyfin{MachineID: "test-jf"}).Mount(context.Background(), host)
	}
	if err != nil {
		t.Fatal(err)
	}
	return inst, ident, admin
}

func TestAPIKeysShowOnceAuthHeadersAndImmediateRevoke(t *testing.T) {
	rec := diagnostics.New(diagnostics.Deps{})
	capture, err := rec.Start("owner", diagnostics.StartIn{})
	if err != nil {
		t.Fatal(err)
	}
	inst, _, _ := mountAdminTestInstance(t, "jellyfin", nil, rec)
	doc := spectest.Document(t, "jellyfin")

	// Non-owner cannot list, create, or revoke API keys.
	for _, tc := range []struct{ method, path string }{
		{"GET", "/Auth/Keys"},
		{"POST", "/Auth/Keys?App=Wizarr"},
		{"DELETE", "/Auth/Keys/hidden-0000000000000001"},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("X-Emby-Token", "member-grant")
		inst.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("member %s %s: got %d, want 403", tc.method, tc.path, w.Code)
		}
	}

	// Owner creates key -> 204.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/Auth/Keys?App=Wizarr", nil)
	r.Header.Set("X-Emby-Token", "owner-grant")
	inst.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("POST /Auth/Keys: %d %s", w.Code, w.Body.String())
	}

	// Another owner session does NOT see the one-time plaintext key.
	wOther := httptest.NewRecorder()
	rOther := httptest.NewRequest("GET", "/Auth/Keys", nil)
	rOther.Header.Set("X-Emby-Token", "owner-grant-2")
	inst.Handler().ServeHTTP(wOther, rOther)
	var otherList struct {
		Items []struct {
			AccessToken string
			AppName     string
		}
	}
	if wOther.Code != 200 || json.Unmarshal(wOther.Body.Bytes(), &otherList) != nil || len(otherList.Items) != 1 {
		t.Fatalf("other session GET /Auth/Keys: %d %s", wOther.Code, wOther.Body.String())
	}
	if frontend.IsAPIKeyCredential(otherList.Items[0].AccessToken) {
		t.Fatal("other session must not receive creator's one-time plaintext key")
	}

	// Creating session receives plaintext ONCE, matching Jellyfin 12.4 OpenAPI.
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest("GET", "/Auth/Keys", nil)
	r1.Header.Set("X-Emby-Token", "owner-grant")
	inst.Handler().ServeHTTP(w1, r1)
	if w1.Code != 200 {
		t.Fatalf("GET /Auth/Keys: %d %s", w1.Code, w1.Body.String())
	}
	if doc != nil {
		if err := doc.Validate("GET", "/Auth/Keys", w1.Code, w1.Header().Get("Content-Type"), w1.Body.Bytes()); err != nil {
			t.Fatalf("GET /Auth/Keys OpenAPI mismatch: %v\n%s", err, w1.Body.String())
		}
	}
	var list1 struct {
		Items []struct {
			AccessToken string
			AppName     string
		}
	}
	if err := json.Unmarshal(w1.Body.Bytes(), &list1); err != nil || len(list1.Items) != 1 {
		t.Fatalf("decode keys: %v %s", err, w1.Body.String())
	}
	apiKey := list1.Items[0].AccessToken
	if !frontend.IsAPIKeyCredential(apiKey) || list1.Items[0].AppName != "Wizarr" {
		t.Fatalf("expected revealed ek1_ key, got %+v", list1.Items[0])
	}

	// Second GET /Auth/Keys by the same session masks the key as a non-secret handle.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/Auth/Keys", nil)
	r2.Header.Set("X-Emby-Token", "owner-grant")
	inst.Handler().ServeHTTP(w2, r2)
	var list2 struct {
		Items []struct{ AccessToken string }
	}
	if w2.Code != 200 || json.Unmarshal(w2.Body.Bytes(), &list2) != nil || len(list2.Items) != 1 {
		t.Fatalf("second GET /Auth/Keys: %d %s", w2.Code, w2.Body.String())
	}
	handle := list2.Items[0].AccessToken
	if frontend.IsAPIKeyCredential(handle) || !strings.HasPrefix(handle, "hidden-") {
		t.Fatalf("second GET /Auth/Keys must mask secret, got %q", handle)
	}

	// Key authenticates via all four MediaBrowser token transports without DeviceId.
	for _, tc := range []struct {
		name   string
		target string
		header string
		value  string
	}{
		{"X-Emby-Token", "/System/Info", "X-Emby-Token", apiKey},
		{"X-MediaBrowser-Token", "/System/Info", "X-MediaBrowser-Token", apiKey},
		{"Authorization", "/System/Info", "Authorization", `MediaBrowser Token="` + apiKey + `"`},
		{"api_key", "/System/Info?api_key=" + apiKey, "", ""},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", tc.target, nil)
		if tc.header != "" {
			r.Header.Set(tc.header, tc.value)
		}
		inst.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("auth via %s failed: %d %s", tc.name, w.Code, w.Body.String())
		}
	}

	// Revoking by handle (as Jellyfin Web does after reload) takes effect immediately.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest("DELETE", "/Auth/Keys/"+handle, nil)
	rDel.Header.Set("X-Emby-Token", "owner-grant")
	inst.Handler().ServeHTTP(wDel, rDel)
	if wDel.Code != http.StatusNoContent {
		t.Fatalf("DELETE /Auth/Keys/{handle}: %d %s", wDel.Code, wDel.Body.String())
	}
	wAfter := httptest.NewRecorder()
	rAfter := httptest.NewRequest("GET", "/System/Info", nil)
	rAfter.Header.Set("X-Emby-Token", apiKey)
	inst.Handler().ServeHTTP(wAfter, rAfter)
	if wAfter.Code != http.StatusUnauthorized {
		t.Fatalf("revoked api key should fail immediately, got %d", wAfter.Code)
	}

	// Support capture must never record API keys.
	if _, err := rec.Finish("owner", capture.ID, diagnostics.FinishIn{SetFlags: true, IncludeTitles: true}); err != nil {
		t.Fatal(err)
	}
	blob, err := rec.Archive("owner", capture.ID)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		if strings.Contains(string(data), apiKey) {
			t.Fatalf("API key leaked into support bundle file %s", f.Name)
		}
	}
}

func TestWizarrInviteFlowReplayJellyfinAndEmby(t *testing.T) {
	for _, protocol := range []frontend.Protocol{"jellyfin", "emby"} {
		t.Run(string(protocol), func(t *testing.T) {
			var logBuf bytes.Buffer
			inst, _, admin := mountAdminTestInstance(t, protocol, &logBuf, nil)
			var doc *openapi.Document
			if protocol == "jellyfin" {
				doc = spectest.Document(t, "jellyfin")
			}

			// Owner mints an API key for Wizarr.
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/Auth/Keys?App=Wizarr", nil)
			r.Header.Set("X-Emby-Token", "owner-grant")
			inst.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusNoContent {
				t.Fatalf("create key: %d", w.Code)
			}
			w = httptest.NewRecorder()
			r = httptest.NewRequest("GET", "/Auth/Keys", nil)
			r.Header.Set("X-Emby-Token", "owner-grant")
			inst.Handler().ServeHTTP(w, r)
			var keysResp struct {
				Items []struct {
					AccessToken string
					AppName     string
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &keysResp); err != nil || len(keysResp.Items) != 1 {
				t.Fatalf("list keys: %v %s", err, w.Body.String())
			}
			apiKey := keysResp.Items[0].AccessToken

			wizarrCall := func(method, path, body string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				if body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				// Wizarr sends Authorization: MediaBrowser Token="..." without DeviceId.
				req.Header.Set("Authorization", `MediaBrowser Token="`+apiKey+`"`)
				rec := httptest.NewRecorder()
				inst.Handler().ServeHTTP(rec, req)
				return rec
			}

			// 1. Wizarr checks /System/Info.
			if w := wizarrCall("GET", "/System/Info", ""); w.Code != 200 {
				t.Fatalf("GET /System/Info: %d", w.Code)
			}

			// 2. Wizarr creates the invited user.
			// Jellyfin Wizarr sends Name+Password; Emby Wizarr sends Name only and sets Password next.
			createBody := `{"Name":"invited","Password":"strong-password-123"}`
			if protocol == "emby" {
				createBody = `{"Name":"invited"}`
			}
			wCreate := wizarrCall("POST", "/Users/New", createBody)
			if wCreate.Code != 200 {
				t.Fatalf("POST /Users/New: %d %s", wCreate.Code, wCreate.Body.String())
			}
			if doc != nil {
				if err := doc.Validate("POST", "/Users/New", wCreate.Code, wCreate.Header().Get("Content-Type"), wCreate.Body.Bytes()); err != nil {
					t.Fatalf("POST /Users/New OpenAPI: %v\n%s", err, wCreate.Body.String())
				}
			}
			var created struct {
				Id     string
				Name   string
				Policy map[string]any
			}
			if err := json.Unmarshal(wCreate.Body.Bytes(), &created); err != nil || created.Id == "" || created.Name != "invited" {
				t.Fatalf("created user: %v %s", err, wCreate.Body.String())
			}
			if protocol == "emby" {
				wPw := wizarrCall("POST", "/Users/"+created.Id+"/Password", `{"CurrentPw":"","NewPw":"strong-password-123","ResetPassword":false}`)
				if wPw.Code != http.StatusNoContent {
					t.Fatalf("Emby POST /Users/{id}/Password: %d %s", wPw.Code, wPw.Body.String())
				}
			}

			// 3. Wizarr lists libraries via /Library/MediaFolders and matches /Library/VirtualFolders.
			wMF := wizarrCall("GET", "/Library/MediaFolders", "")
			if wMF.Code != 200 {
				t.Fatalf("GET /Library/MediaFolders: %d %s", wMF.Code, wMF.Body.String())
			}
			if doc != nil {
				if err := doc.Validate("GET", "/Library/MediaFolders", wMF.Code, wMF.Header().Get("Content-Type"), wMF.Body.Bytes()); err != nil {
					t.Fatalf("GET /Library/MediaFolders OpenAPI: %v\n%s", err, wMF.Body.String())
				}
			}
			var mf struct {
				Items []struct {
					Id             string
					Guid           string
					Name           string
					Type           string
					CollectionType string
				}
			}
			if err := json.Unmarshal(wMF.Body.Bytes(), &mf); err != nil || len(mf.Items) != 2 {
				t.Fatalf("MediaFolders: %v %s", err, wMF.Body.String())
			}
			moviesID := mf.Items[0].Id
			if mf.Items[0].Type != "CollectionFolder" {
				t.Fatalf("Jellyseerr requires Type=CollectionFolder, got %+v", mf.Items[0])
			}
			if protocol == "emby" && mf.Items[0].Guid != moviesID {
				t.Fatalf("Wizarr Emby expects Guid matching Id, got %+v", mf.Items[0])
			}
			wVF := wizarrCall("GET", "/Library/VirtualFolders", "")
			var vf []struct {
				ItemId string
				Name   string
			}
			if wVF.Code != 200 || json.Unmarshal(wVF.Body.Bytes(), &vf) != nil || len(vf) != 2 || vf[0].ItemId != moviesID {
				t.Fatalf("VirtualFolders ItemId must match MediaFolders Id: vf=%+v mf=%+v", vf, mf.Items)
			}

			// 4. Wizarr GETs the user, modifies Policy (EnabledFolders + IsHidden + unsupported fields), and POSTs Policy.
			wGet := wizarrCall("GET", "/Users/"+created.Id, "")
			if wGet.Code != 200 {
				t.Fatalf("GET /Users/{id}: %d %s", wGet.Code, wGet.Body.String())
			}
			if doc != nil {
				if err := doc.Validate("GET", "/Users/{userId}", wGet.Code, wGet.Header().Get("Content-Type"), wGet.Body.Bytes()); err != nil {
					t.Fatalf("GET /Users/{id} OpenAPI: %v", err)
				}
			}
			var fetched struct {
				Policy map[string]any
			}
			if err := json.Unmarshal(wGet.Body.Bytes(), &fetched); err != nil {
				t.Fatal(err)
			}
			fetched.Policy["EnableAllFolders"] = false
			fetched.Policy["EnabledFolders"] = []string{moviesID}
			fetched.Policy["IsHidden"] = true
			fetched.Policy["EnableContentDownloading"] = true // unsupported: ignored + logged once
			policyJSON, _ := json.Marshal(fetched.Policy)
			if wPol := wizarrCall("POST", "/Users/"+created.Id+"/Policy", string(policyJSON)); wPol.Code != http.StatusNoContent {
				t.Fatalf("POST /Users/{id}/Policy: %d %s", wPol.Code, wPol.Body.String())
			}
			// Second POST with same unsupported field must NOT log a second warning.
			if wPol2 := wizarrCall("POST", "/Users/"+created.Id+"/Policy", string(policyJSON)); wPol2.Code != http.StatusNoContent {
				t.Fatalf("second POST /Users/{id}/Policy: %d", wPol2.Code)
			}
			if count := strings.Count(logBuf.String(), "EnableContentDownloading"); count != 1 {
				t.Fatalf("unsupported policy field should be logged once, got %d in:\n%s", count, logBuf.String())
			}

			// Verify stored policy via GET /Users/{id}.
			wAfter := wizarrCall("GET", "/Users/"+created.Id, "")
			var after struct {
				Policy map[string]any
			}
			if wAfter.Code != 200 || json.Unmarshal(wAfter.Body.Bytes(), &after) != nil {
				t.Fatalf("GET /Users/{id} after policy: %d %s", wAfter.Code, wAfter.Body.String())
			}
			if after.Policy["EnableAllFolders"] != false || after.Policy["IsHidden"] != true {
				t.Fatalf("policy not persisted: %+v", after.Policy)
			}
			folders, _ := after.Policy["EnabledFolders"].([]any)
			if len(folders) != 1 || folders[0] != moviesID {
				t.Fatalf("EnabledFolders mismatch: %+v want [%s]", folders, moviesID)
			}

			// 5. Pause user via IsDisabled: true, confirm visible as disabled in GET /Users.
			if wPause := wizarrCall("POST", "/Users/"+created.Id+"/Policy", `{"IsDisabled":true}`); wPause.Code != http.StatusNoContent {
				t.Fatalf("pause user: %d %s", wPause.Code, wPause.Body.String())
			}
			wUsers := wizarrCall("GET", "/Users", "")
			if wUsers.Code != 200 {
				t.Fatalf("GET /Users: %d %s", wUsers.Code, wUsers.Body.String())
			}
			if doc != nil {
				if err := doc.Validate("GET", "/Users", wUsers.Code, wUsers.Header().Get("Content-Type"), wUsers.Body.Bytes()); err != nil {
					t.Fatalf("GET /Users OpenAPI: %v", err)
				}
			}
			var allUsers []struct {
				Id     string
				Policy map[string]any
			}
			if err := json.Unmarshal(wUsers.Body.Bytes(), &allUsers); err != nil {
				t.Fatal(err)
			}
			foundPaused := false
			for _, u := range allUsers {
				if u.Id == created.Id {
					foundPaused = u.Policy["IsDisabled"] == true
				}
			}
			if !foundPaused {
				t.Fatalf("paused user missing or not IsDisabled=true in GET /Users: %+v", allUsers)
			}

			// 6. Update password via POST /Users/Password?userId= (Jellyfin 12.4) / /Users/{id}/Password.
			pwPath := "/Users/" + created.Id + "/Password"
			if protocol == "jellyfin" {
				pwPath = "/Users/Password?userId=" + created.Id
			}
			if wPw := wizarrCall("POST", pwPath, `{"NewPw":"rotated-password-456"}`); wPw.Code != http.StatusNoContent {
				t.Fatalf("rotate password: %d %s", wPw.Code, wPw.Body.String())
			}
			createdIDNum, _ := ids.ParseGUID(ids.User, created.Id)
			if err := admin.VerifyViewerPassword(context.Background(), createdIDNum, "rotated-password-456"); err != nil {
				t.Fatalf("rotated password did not persist: %v", err)
			}

			// 7. Delete user (DELETE /Users/{id} on Jellyfin, POST /Users/{id}/Delete on Emby).
			delMethod, delPath := "DELETE", "/Users/"+created.Id
			if protocol == "emby" {
				delMethod, delPath = "POST", "/Users/"+created.Id+"/Delete"
			}
			if wDel := wizarrCall(delMethod, delPath, ""); wDel.Code != http.StatusNoContent {
				t.Fatalf("delete user: %d %s", wDel.Code, wDel.Body.String())
			}
			if wGone := wizarrCall("GET", "/Users/"+created.Id, ""); wGone.Code != http.StatusNotFound {
				t.Fatalf("deleted user should 404, got %d", wGone.Code)
			}
		})
	}
}

func TestUserAdminSecurityRefusals(t *testing.T) {
	inst, _, _ := mountAdminTestInstance(t, "jellyfin", nil, nil)
	ownerGUID, _ := ids.GUID(ids.User, 1)
	memberGUID, _ := ids.GUID(ids.User, 2)
	plexGUID, _ := ids.GUID(ids.User, 3)

	call := func(token, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-Emby-Token", token)
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		return w
	}

	// IsAdministrator: true refused for non-owner.
	if w := call("owner-grant", "POST", "/Users/"+memberGUID+"/Policy", `{"IsAdministrator":true}`); w.Code != http.StatusForbidden {
		t.Fatalf("elevating member should 403, got %d %s", w.Code, w.Body.String())
	}
	// Owner cannot be demoted or disabled.
	if w := call("owner-grant", "POST", "/Users/"+ownerGUID+"/Policy", `{"IsAdministrator":false}`); w.Code != http.StatusForbidden {
		t.Fatalf("demoting owner should 403, got %d", w.Code)
	}
	if w := call("owner-grant", "POST", "/Users/"+ownerGUID+"/Policy", `{"IsDisabled":true}`); w.Code != http.StatusForbidden {
		t.Fatalf("disabling owner should 403, got %d", w.Code)
	}
	// Owner cannot be deleted; Plex-managed viewer deletion refused (409).
	if w := call("owner-grant", "DELETE", "/Users/"+ownerGUID, ""); w.Code != http.StatusForbidden {
		t.Fatalf("deleting owner should 403, got %d", w.Code)
	}
	if w := call("owner-grant", "DELETE", "/Users/"+plexGUID, ""); w.Code != http.StatusConflict {
		t.Fatalf("deleting plex viewer should 409, got %d", w.Code)
	}
	// ResetPassword with empty NewPw refused for owner, allowed for non-owner.
	if w := call("owner-grant", "POST", "/Users/"+ownerGUID+"/Password", `{"ResetPassword":true}`); w.Code != http.StatusForbidden {
		t.Fatalf("clearing owner password should 403, got %d", w.Code)
	}
	if w := call("owner-grant", "POST", "/Users/"+memberGUID+"/Password", `{"ResetPassword":true}`); w.Code != http.StatusNoContent {
		t.Fatalf("resetting member password should 204, got %d %s", w.Code, w.Body.String())
	}
	// Non-owner cannot use any admin endpoint.
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/Users", ""},
		{"GET", "/Users/" + ownerGUID, ""},
		{"POST", "/Users/New", `{"Name":"hack","Password":"strong-password-123"}`},
		{"POST", "/Users/" + ownerGUID + "/Policy", `{"IsHidden":true}`},
		{"POST", "/Users/" + ownerGUID + "/Password", `{"NewPw":"strong-password-123"}`},
		{"DELETE", "/Users/" + memberGUID, ""},
		{"GET", "/Library/MediaFolders", ""},
	} {
		if w := call("member-grant", tc.method, tc.path, tc.body); w.Code != http.StatusForbidden {
			t.Fatalf("member %s %s: got %d, want 403", tc.method, tc.path, w.Code)
		}
	}
}
