package vela

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/store"
)

type scopedTestIdentity struct {
	frontendtest.Identity
}

func (scopedTestIdentity) GrantScope(_ context.Context, token string, d frontend.Device) (string, error) {
	return token + ":" + d.ID, nil
}

func (scopedTestIdentity) GrantScopeCurrent(_ context.Context, _ string, _ frontend.Device) (*frontend.Viewer, error) {
	return &frontend.Viewer{ID: 1, Username: "owner"}, nil
}

func TestVelaSignInMethodSwitchesAndRevision(t *testing.T) {
	db := store.OpenTest(t)
	cfg := config.Config{Frontends: map[string]config.Frontend{}}

	id := scopedTestIdentity{Identity: frontendtest.Identity{
		AuthenticationFeaturesFunc: func() frontend.AuthenticationFeatures {
			return frontend.AuthenticationFeatures{
				Password:          true,
				PlexAccountSignIn: true,
				QuickConnect:      true,
			}
		},
		ResolveDeviceFunc: func(_ context.Context, cred string, _ frontend.Device) (*frontend.Viewer, time.Time, error) {
			if cred == "tok-viewer" {
				return &frontend.Viewer{ID: 7, Username: "alice"}, time.Now().Add(time.Hour), nil
			}
			return nil, time.Time{}, frontend.ErrUnauthorized
		},
		SignInFunc: func(context.Context, string, string, frontend.Device) (string, error) {
			return "tok-viewer", nil
		},
		SignInWithPlexFunc: func(context.Context, string, frontend.Device) (string, error) {
			return "tok-viewer", nil
		},
		InitiateFunc: func(context.Context, frontend.Device) (string, string, error) {
			return "ABC123", "sec-1", nil
		},
		DetailsFunc: func(context.Context, string) (frontend.QuickConnectDetails, error) {
			return frontend.QuickConnectDetails{Code: "ABC123", Secret: "sec-1", ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
		},
	}}

	fe := Frontend{DB: db, ReplayKey: make([]byte, 32)}
	mounted, err := fe.Mount(t.Context(), frontend.Host{
		Identity:        id,
		Experience:      frontendtest.Experience{},
		Sessions:        frontendtest.Sessions{},
		HostConfig:      func() config.Config { return cfg },
		ViewerPortalURL: func(context.Context, *http.Request) (string, error) { return "https://ether.example/ether/me", nil },
		ServerIdentity:  frontend.ServerIdentity{MachineID: "m1", FriendlyName: "Ether", Version: "0.3.17"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mounted.Close(context.Background()) })
	h := mounted.Handler()

	getCaps := func(authed bool) (map[string]any, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/vela/v0/capabilities", nil)
		if authed {
			req.Header.Set("Authorization", "Bearer tok-viewer")
			req.Header.Set("X-Vela-Device-Id", "dev-1")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /capabilities = %d: %s", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out, w.Header().Get("ETag")
	}
	getBoot := func() (map[string]any, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/vela/v0/bootstrap", nil)
		req.Header.Set("Authorization", "Bearer tok-viewer")
		req.Header.Set("X-Vela-Device-Id", "dev-1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /bootstrap = %d: %s", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out, w.Header().Get("ETag")
	}
	postAuth := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/vela/v0/auth/"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	featAvail := func(caps map[string]any, key string) (bool, bool) {
		f, ok := caps["features"].(map[string]any)[key].(map[string]any)
		if !ok {
			return false, false
		}
		v, _ := f["available"].(bool)
		return v, true
	}

	// Default (unset): all three sign-in methods enabled; unauthenticated caps omit quickConnectApproval.
	caps0, etag0 := getCaps(false)
	if pw, _ := featAvail(caps0, "passwordSignIn"); !pw {
		t.Fatalf("default passwordSignIn = false")
	}
	if qc, _ := featAvail(caps0, "quickConnect"); !qc {
		t.Fatalf("default quickConnect = false")
	}
	if dg, _ := featAvail(caps0, "deviceGrant"); !dg {
		t.Fatalf("default deviceGrant = false")
	}
	if caps0["plexAccountSignIn"] != true {
		t.Fatalf("default plexAccountSignIn = %v, want true", caps0["plexAccountSignIn"])
	}
	if _, present := featAvail(caps0, "quickConnectApproval"); present {
		t.Fatalf("unauthenticated capabilities must not advertise quickConnectApproval")
	}
	if caps0["revision"] != float64(0) {
		t.Fatalf("default revision = %v, want 0", caps0["revision"])
	}
	boot0, bootETag0 := getBoot()
	bootCaps0 := boot0["capabilities"].(map[string]any)
	if avail, present := featAvail(bootCaps0, "quickConnectApproval"); !present || !avail {
		t.Fatalf("authenticated bootstrap quickConnectApproval = (%v, %v), want (true, true)", avail, present)
	}

	// Disable password sign-in only + bump capabilities revision.
	cfg.Frontends["vela"] = config.Frontend{
		CapabilitiesRevision: 1,
		Settings:             map[string]any{"passwordSignIn": false},
	}
	caps1, etag1 := getCaps(false)
	if etag1 == etag0 {
		t.Fatal("capabilities ETag did not change after passwordSignIn toggle")
	}
	if caps1["revision"] != float64(1) {
		t.Fatalf("revision = %v, want 1", caps1["revision"])
	}
	if pw, _ := featAvail(caps1, "passwordSignIn"); pw {
		t.Fatal("passwordSignIn should be false")
	}
	if caps1["plexAccountSignIn"] != true {
		t.Fatal("plexAccountSignIn should remain true")
	}
	if qc, _ := featAvail(caps1, "quickConnect"); !qc {
		t.Fatal("quickConnect should remain true")
	}
	if w := postAuth("sign-in", `{"username":"alice","password":"pw","idempotencyKey":"pw-1","device":{"id":"d1","name":"D","platform":"web","appVersion":"0.2.0"}}`); w.Code != http.StatusForbidden {
		t.Fatalf("disabled password sign-in status = %d, want 403: %s", w.Code, w.Body.String())
	}
	boot1, bootETag1 := getBoot()
	if bootETag1 == bootETag0 {
		t.Fatal("bootstrap ETag did not change after capabilities revision bump")
	}
	if pw, _ := featAvail(boot1["capabilities"].(map[string]any), "passwordSignIn"); pw {
		t.Fatal("bootstrap capabilities.passwordSignIn should be false")
	}

	// Disable Plex sign-in only.
	cfg.Frontends["vela"] = config.Frontend{
		CapabilitiesRevision: 2,
		Settings:             map[string]any{"plexSignIn": false},
	}
	caps2, _ := getCaps(false)
	if _, present := caps2["plexAccountSignIn"]; present {
		t.Fatalf("plexAccountSignIn should be omitted when plexSignIn=false")
	}
	if w := postAuth("plex", `{"plexToken":"ptok","idempotencyKey":"px-1","device":{"id":"d1","name":"D","platform":"web","appVersion":"0.2.0"}}`); w.Code != http.StatusForbidden {
		t.Fatalf("disabled plex sign-in status = %d, want 403: %s", w.Code, w.Body.String())
	}

	// Disable Code (quickConnect) sign-in.
	cfg.Frontends["vela"] = config.Frontend{
		CapabilitiesRevision: 3,
		Settings:             map[string]any{"quickConnect": false},
	}
	caps3, _ := getCaps(true)
	if qc, _ := featAvail(caps3, "quickConnect"); qc {
		t.Fatal("quickConnect should be false")
	}
	if dg, _ := featAvail(caps3, "deviceGrant"); dg {
		t.Fatal("deviceGrant should be false when quickConnect=false")
	}
	if qa, present := featAvail(caps3, "quickConnectApproval"); !present || qa {
		t.Fatalf("authenticated quickConnectApproval when disabled = (%v, %v), want (false, true)", qa, present)
	}
	if w := postAuth("quick-connect", `{"idempotencyKey":"qc-1","device":{"id":"d1","name":"D","platform":"web","appVersion":"0.2.0"}}`); w.Code != http.StatusForbidden {
		t.Fatalf("disabled quick-connect status = %d, want 403: %s", w.Code, w.Body.String())
	}

	// Disable all three viewer methods.
	cfg.Frontends["vela"] = config.Frontend{
		CapabilitiesRevision: 4,
		Settings: map[string]any{
			"passwordSignIn": false,
			"plexSignIn":     false,
			"quickConnect":   false,
		},
	}
	caps4, _ := getCaps(false)
	if pw, _ := featAvail(caps4, "passwordSignIn"); pw {
		t.Fatal("all-off passwordSignIn should be false")
	}
	if _, present := caps4["plexAccountSignIn"]; present {
		t.Fatal("all-off plexAccountSignIn should be omitted")
	}
	if qc, _ := featAvail(caps4, "quickConnect"); qc {
		t.Fatal("all-off quickConnect should be false")
	}

	// Re-enabling all three with a higher revision still yields a fresh ETag vs revision 0.
	cfg.Frontends["vela"] = config.Frontend{
		CapabilitiesRevision: 5,
		Settings: map[string]any{
			"passwordSignIn": true,
			"plexSignIn":     true,
			"quickConnect":   true,
		},
	}
	caps5, etag5 := getCaps(false)
	if etag5 == etag0 || caps5["revision"] != float64(5) {
		t.Fatalf("expected revision 5 and fresh ETag after re-enable: rev=%v etag0=%q etag5=%q", caps5["revision"], etag0, etag5)
	}
}

func TestVelaQuickConnectApproveAuthIdempotencyAndErrors(t *testing.T) {
	db := store.OpenTest(t)
	cfg := config.Config{Frontends: map[string]config.Frontend{}}

	var approveCalls int
	var lastViewerID int64
	var lastCode string
	var approveErr error

	id := scopedTestIdentity{Identity: frontendtest.Identity{
		AuthenticationFeaturesFunc: func() frontend.AuthenticationFeatures {
			return frontend.AuthenticationFeatures{Password: true, QuickConnect: true}
		},
		ResolveDeviceFunc: func(_ context.Context, cred string, _ frontend.Device) (*frontend.Viewer, time.Time, error) {
			switch cred {
			case "tok-owner":
				return &frontend.Viewer{ID: 1, Username: "owner", IsOwner: true}, time.Now().Add(time.Hour), nil
			case "tok-viewer":
				return &frontend.Viewer{ID: 2, Username: "alice", IsOwner: false}, time.Now().Add(time.Hour), nil
			case "tok-delegated":
				return &frontend.Viewer{ID: 3, Username: "delegated", Delegated: true}, time.Now().Add(time.Hour), nil
			default:
				return nil, time.Time{}, frontend.ErrUnauthorized
			}
		},
		ApproveFunc: func(_ context.Context, viewer *frontend.Viewer, code string) error {
			approveCalls++
			lastViewerID = viewer.ID
			lastCode = code
			return approveErr
		},
	}}

	fe := Frontend{DB: db, ReplayKey: make([]byte, 32)}
	mounted, err := fe.Mount(t.Context(), frontend.Host{
		Identity:   id,
		Experience: frontendtest.Experience{},
		Sessions:   frontendtest.Sessions{},
		HostConfig: func() config.Config { return cfg },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mounted.Close(context.Background()) })
	h := mounted.Handler()

	postApprove := func(token, deviceID, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/vela/v0/auth/quick-connect/approve", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if deviceID != "" {
			req.Header.Set("X-Vela-Device-Id", deviceID)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	// 1. Unauthenticated caller -> 401 auth/unauthorized (no approve call).
	if w := postApprove("", "", `{"code":"ABCD12","idempotencyKey":"unauth-1"}`); w.Code != http.StatusUnauthorized || approveCalls != 0 {
		t.Fatalf("unauthenticated approve: code=%d calls=%d body=%s", w.Code, approveCalls, w.Body.String())
	}

	// 2. Delegated viewer -> capabilities says available:false and endpoint returns 403.
	delCapReq := httptest.NewRequest(http.MethodGet, "/vela/v0/capabilities", nil)
	delCapReq.Header.Set("Authorization", "Bearer tok-delegated")
	delCapReq.Header.Set("X-Vela-Device-Id", "dev-del")
	delCapRec := httptest.NewRecorder()
	h.ServeHTTP(delCapRec, delCapReq)
	var delCaps map[string]any
	_ = json.Unmarshal(delCapRec.Body.Bytes(), &delCaps)
	if delCaps["features"].(map[string]any)["quickConnectApproval"].(map[string]any)["available"] != false {
		t.Fatalf("delegated viewer should not get quickConnectApproval: %#v", delCaps["features"])
	}
	if w := postApprove("tok-delegated", "dev-del", `{"code":"ABCD12","idempotencyKey":"del-1"}`); w.Code != http.StatusForbidden || approveCalls != 0 {
		t.Fatalf("delegated approve: code=%d calls=%d body=%s", w.Code, approveCalls, w.Body.String())
	}

	// 3. Regular active viewer (allowed by portal rules) approves + idempotent replay + conflict.
	first := postApprove("tok-viewer", "dev-alice", `{"code":"  AB-1234  ","idempotencyKey":"idem-alice-1"}`)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"accepted":true`) {
		t.Fatalf("viewer approve failed: %d %s", first.Code, first.Body.String())
	}
	if approveCalls != 1 || lastViewerID != 2 || lastCode != "AB-1234" {
		t.Fatalf("approve call mismatch: calls=%d viewer=%d code=%q", approveCalls, lastViewerID, lastCode)
	}
	replay := postApprove("tok-viewer", "dev-alice", `{"code":"  AB-1234  ","idempotencyKey":"idem-alice-1"}`)
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() || approveCalls != 1 {
		t.Fatalf("idempotent replay: code=%d calls=%d body=%s", replay.Code, approveCalls, replay.Body.String())
	}
	conflict := postApprove("tok-viewer", "dev-alice", `{"code":"ZZ-9999","idempotencyKey":"idem-alice-1"}`)
	if conflict.Code != http.StatusConflict || approveCalls != 1 {
		t.Fatalf("idempotency conflict: code=%d calls=%d body=%s", conflict.Code, approveCalls, conflict.Body.String())
	}

	// 4. Owner can also approve using the same idempotencyKey without colliding with alice's grant scope.
	ownerRes := postApprove("tok-owner", "dev-owner", `{"code":"OWN-777","idempotencyKey":"idem-alice-1"}`)
	if ownerRes.Code != http.StatusOK || approveCalls != 2 || lastViewerID != 1 {
		t.Fatalf("owner approve: code=%d calls=%d viewer=%d body=%s", ownerRes.Code, approveCalls, lastViewerID, ownerRes.Body.String())
	}

	// 5. Empty code -> 400 request/invalid_code (never 401).
	emptyRes := postApprove("tok-owner", "dev-owner", `{"code":"   ","idempotencyKey":"empty-1"}`)
	if emptyRes.Code != http.StatusBadRequest || !strings.Contains(emptyRes.Body.String(), `"invalid_code"`) {
		t.Fatalf("empty code: code=%d body=%s", emptyRes.Code, emptyRes.Body.String())
	}

	// 6. Invalid, expired, used codes never return 401 (which would sign out Vela).
	for _, tc := range []struct {
		key       string
		err       error
		wantCode  int
		wantClass string
		wantErr   string
	}{
		{"bad-1", frontend.ErrUnauthorized, http.StatusBadRequest, "request", "invalid_code"},
		{"exp-1", frontend.ErrQuickConnectExpired, http.StatusGone, "conflict", "code_expired"},
		{"used-1", frontend.ErrQuickConnectUsed, http.StatusConflict, "conflict", "code_used"},
	} {
		approveErr = tc.err
		w := postApprove("tok-owner", "dev-owner", `{"code":"CODE99","idempotencyKey":"`+tc.key+`"}`)
		if w.Code != tc.wantCode {
			t.Fatalf("%s: status=%d want=%d body=%s", tc.key, w.Code, tc.wantCode, w.Body.String())
		}
		var env struct {
			Error struct {
				Class string `json:"class"`
				Code  string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Error.Class != tc.wantClass || env.Error.Code != tc.wantErr {
			t.Fatalf("%s: error=%+v want class=%q code=%q", tc.key, env.Error, tc.wantClass, tc.wantErr)
		}
	}

	// 7. Rate limit returns 429 with Retry-After and does not pin the idempotency key.
	approveErr = &frontend.IdentityRateLimitError{RetryAfter: 12 * time.Second}
	rl := postApprove("tok-owner", "dev-owner", `{"code":"RETRY1","idempotencyKey":"rl-key"}`)
	if rl.Code != http.StatusTooManyRequests || rl.Header().Get("Retry-After") != "12" {
		t.Fatalf("rate limit: code=%d retry-after=%q body=%s", rl.Code, rl.Header().Get("Retry-After"), rl.Body.String())
	}
	approveErr = nil
	retryOK := postApprove("tok-owner", "dev-owner", `{"code":"RETRY1","idempotencyKey":"rl-key"}`)
	if retryOK.Code != http.StatusOK {
		t.Fatalf("retry after rate limit failed: %d %s", retryOK.Code, retryOK.Body.String())
	}

	// 8. When owner disables Quick Connect on Vela, approval returns 403 feature_unavailable.
	cfg.Frontends["vela"] = config.Frontend{Settings: map[string]any{"quickConnect": false}}
	offRes := postApprove("tok-owner", "dev-owner", `{"code":"OFF123","idempotencyKey":"off-1"}`)
	if offRes.Code != http.StatusForbidden || !strings.Contains(offRes.Body.String(), `"feature_unavailable"`) {
		t.Fatalf("disabled quickConnect approve: %d %s", offRes.Code, offRes.Body.String())
	}
}
