package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
)

func TestMediaBrowserAPIKeysAndUserAdminEndToEnd(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "owner-plex-token"); err != nil {
		t.Fatal(err)
	}
	owner, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	local, err := identity.NewLocal(a.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Create(ctx, owner.ID, "owner", "owner-password-123"); err != nil {
		t.Fatal(err)
	}
	// Enable publicViewerPicker on Jellyfin so IsHidden is observable on /Users/Public.
	if err := a.frontends.control.PutSettings(ctx, "jellyfin", map[string]any{"port": float64(8096), "publicViewerPicker": true}); err != nil {
		t.Fatal(err)
	}
	jf, err := a.frontends.registry.Mount(ctx, "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	defer a.frontends.registry.Unmount(ctx, "jellyfin")
	emby, err := a.frontends.registry.Mount(ctx, "emby")
	if err != nil {
		t.Fatal(err)
	}
	defer a.frontends.registry.Unmount(ctx, "emby")

	callJF := func(method, path, body, token, deviceID string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			r.Header.Set("X-Emby-Token", token)
		}
		if deviceID != "" {
			r.Header.Set("X-Emby-Device-Id", deviceID)
		}
		w := httptest.NewRecorder()
		jf.Handler().ServeHTTP(w, r)
		return w
	}

	// Owner signs into Jellyfin Web.
	login := callJF("POST", "/Users/AuthenticateByName", `{"Username":"owner","Pw":"owner-password-123"}`, "", "jf-web")
	var signed struct{ AccessToken string }
	if login.Code != 200 || json.Unmarshal(login.Body.Bytes(), &signed) != nil || signed.AccessToken == "" {
		t.Fatalf("owner login: %d %s", login.Code, login.Body.String())
	}

	// Jellyseerr / Jellyfin Web creates an API key and reads it once.
	if w := callJF("POST", "/Auth/Keys?App=Jellyseerr", "", signed.AccessToken, "jf-web"); w.Code != http.StatusNoContent {
		t.Fatalf("POST /Auth/Keys: %d %s", w.Code, w.Body.String())
	}
	wKeys := callJF("GET", "/Auth/Keys", "", signed.AccessToken, "jf-web")
	var keysResp struct {
		Items []struct {
			Id          int64
			AccessToken string
			AppName     string
		}
	}
	if wKeys.Code != 200 || json.Unmarshal(wKeys.Body.Bytes(), &keysResp) != nil || len(keysResp.Items) != 1 {
		t.Fatalf("GET /Auth/Keys: %d %s", wKeys.Code, wKeys.Body.String())
	}
	apiKey := keysResp.Items[0].AccessToken
	if !core.IsAPIKeyCredential(apiKey) {
		t.Fatalf("expected ek1_ key, got %q", apiKey)
	}

	// API key must NOT work on Plex or Emby.
	if _, _, err := a.users.Accept(ctx, apiKey); err == nil {
		t.Fatal("Plex Accept must reject Jellyfin API key")
	}
	if _, err := a.users.DurableOwnerToken(ctx, apiKey); err == nil {
		t.Fatal("DurableOwnerToken must reject Jellyfin API key")
	}
	embyReq := httptest.NewRequest("GET", "/System/Info", nil)
	embyReq.Header.Set("X-Emby-Token", apiKey)
	embyW := httptest.NewRecorder()
	emby.Handler().ServeHTTP(embyW, embyReq)
	if embyW.Code != http.StatusUnauthorized {
		t.Fatalf("Emby must reject Jellyfin API key, got %d", embyW.Code)
	}

	// Use the API key to create a real Ether local viewer, sign them in, hide/pause/resume/re-password/delete them.
	wNew := callJF("POST", "/Users/New", `{"Name":"alice","Password":"alice-password-123"}`, apiKey, "")
	var alice struct {
		Id   string
		Name string
	}
	if wNew.Code != 200 || json.Unmarshal(wNew.Body.Bytes(), &alice) != nil || alice.Id == "" {
		t.Fatalf("POST /Users/New: %d %s", wNew.Code, wNew.Body.String())
	}

	// Alice appears on /Users/Public before IsHidden: true, and can sign in on Jellyfin.
	wPub1 := callJF("GET", "/Users/Public", "", "", "")
	if wPub1.Code != 200 || !strings.Contains(wPub1.Body.String(), `"Name":"alice"`) {
		t.Fatalf("alice missing from /Users/Public: %d %s", wPub1.Code, wPub1.Body.String())
	}
	wAliceLogin := callJF("POST", "/Users/AuthenticateByName", `{"Username":"alice","Pw":"alice-password-123"}`, "", "alice-phone")
	var aliceSession struct{ AccessToken string }
	if wAliceLogin.Code != 200 || json.Unmarshal(wAliceLogin.Body.Bytes(), &aliceSession) != nil || aliceSession.AccessToken == "" {
		t.Fatalf("alice login: %d %s", wAliceLogin.Code, wAliceLogin.Body.String())
	}

	// Restrict Alice to library 1, hide her from /Users/Public, and pause her.
	moviesGUID, _ := ids.GUID(ids.View, 1)
	if w := callJF("POST", "/Users/"+alice.Id+"/Policy", fmt.Sprintf(`{"EnableAllFolders":false,"EnabledFolders":[%q],"IsHidden":true,"IsDisabled":true}`, moviesGUID), apiKey, ""); w.Code != http.StatusNoContent {
		t.Fatalf("update policy: %d %s", w.Code, w.Body.String())
	}
	// Alice is immediately signed out by pause.
	if w := callJF("GET", "/Users/Me", "", aliceSession.AccessToken, "alice-phone"); w.Code != http.StatusUnauthorized {
		t.Fatalf("paused viewer grant must fail immediately, got %d", w.Code)
	}
	// Resume Alice while keeping IsHidden: true; she stays hidden from /Users/Public.
	if w := callJF("POST", "/Users/"+alice.Id+"/Policy", `{"IsDisabled":false}`, apiKey, ""); w.Code != http.StatusNoContent {
		t.Fatalf("resume alice: %d", w.Code)
	}
	wPub2 := callJF("GET", "/Users/Public", "", "", "")
	if wPub2.Code != 200 || strings.Contains(wPub2.Body.String(), `"Name":"alice"`) {
		t.Fatalf("hidden viewer must not appear on /Users/Public: %s", wPub2.Body.String())
	}
	// Change Alice's password and sign in with the new password.
	if w := callJF("POST", "/Users/"+alice.Id+"/Password", `{"NewPw":"alice-new-password-456"}`, apiKey, ""); w.Code != http.StatusNoContent {
		t.Fatalf("change password: %d %s", w.Code, w.Body.String())
	}
	if w := callJF("POST", "/Users/AuthenticateByName", `{"Username":"alice","Pw":"alice-new-password-456"}`, "", "alice-phone"); w.Code != 200 {
		t.Fatalf("alice login with new password: %d %s", w.Code, w.Body.String())
	}
	// Delete Alice via MediaBrowser API.
	if w := callJF("DELETE", "/Users/"+alice.Id, "", apiKey, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete alice: %d %s", w.Code, w.Body.String())
	}

	// Console API lists the key (without secret or handle) and revokes it immediately.
	adminH := a.buildAdmin()
	ownerSess, _, err := a.users.IssueOwnerSession(ctx, "owner-plex-token")
	if err != nil {
		t.Fatal(err)
	}
	adminCall := func(method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.RemoteAddr = "127.0.0.1:4321"
		r.Header.Set("X-Ether-Admin-Token", ownerSess)
		r.Header.Set("X-Ether-Request", "1")
		w := httptest.NewRecorder()
		adminH.ServeHTTP(w, r)
		return w
	}
	wAdminList := adminCall("GET", "/ether/api/frontends/jellyfin/api-keys")
	if wAdminList.Code != 200 || strings.Contains(wAdminList.Body.String(), apiKey) || strings.Contains(wAdminList.Body.String(), "hidden-") {
		t.Fatalf("console list unexpected or leaked secret: %d %s", wAdminList.Code, wAdminList.Body.String())
	}
	var adminKeys []struct {
		ID      int64  `json:"id"`
		AppName string `json:"appName"`
	}
	if err := json.Unmarshal(wAdminList.Body.Bytes(), &adminKeys); err != nil || len(adminKeys) != 1 || adminKeys[0].AppName != "Jellyseerr" {
		t.Fatalf("console keys: %v %s", err, wAdminList.Body.String())
	}
	if wRev := adminCall("DELETE", fmt.Sprintf("/ether/api/frontends/jellyfin/api-keys/%d", adminKeys[0].ID)); wRev.Code != 200 {
		t.Fatalf("console revoke: %d %s", wRev.Code, wRev.Body.String())
	}
	// Console revocation must take effect immediately on Jellyfin.
	if wAfter := callJF("GET", "/System/Info", "", apiKey, ""); wAfter.Code != http.StatusUnauthorized {
		t.Fatalf("console-revoked api key must fail immediately, got %d", wAfter.Code)
	}
}
