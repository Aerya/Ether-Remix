package vela

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

type plexHomeTestIdentity struct {
	frontendtest.PlexHomeIdentity
}

func (plexHomeTestIdentity) GrantScope(_ context.Context, _ string, d frontend.Device) (string, error) {
	return "fam-" + d.ID, nil
}
func (plexHomeTestIdentity) GrantScopeCurrent(_ context.Context, _ string, _ frontend.Device) (*frontend.Viewer, error) {
	return &frontend.Viewer{ID: 1, Username: "owner"}, nil
}

func TestVelaHouseholdProfileSelection(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE vela_replays(key TEXT PRIMARY KEY,digest TEXT NOT NULL,response BLOB,expires INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	owner := &frontend.Viewer{ID: 1, Username: "owner"}
	kid := &frontend.Viewer{ID: 2, Username: "kid"}
	guest := &frontend.Viewer{ID: 3, Username: "guest"}
	profiles := []frontend.HouseholdProfile{
		{ViewerID: 1, PlexID: 42, Name: "Owner", Thumb: "https://plex.tv/users/owner/avatar?c=7", Protected: true},
		{ViewerID: 2, PlexID: 300, Name: "Kid", Thumb: "https://plex.tv/users/kid/avatar?c=9", Protected: true, Restricted: true},
		{ViewerID: 3, PlexID: 301, Name: "Guest", Protected: false},
	}

	var mu sync.Mutex
	revoked := map[string]bool{}
	profilesCalls, signInCalls, completeCalls := 0, 0, 0
	var lastComplete frontend.HouseholdProfileSignIn
	var lastAvatar frontend.HouseholdAvatarRequest
	homeEnabled := false

	var id plexHomeTestIdentity
	id.AuthenticationFeaturesFunc = func() frontend.AuthenticationFeatures {
		return frontend.AuthenticationFeatures{Password: true, PlexAccountSignIn: true, HouseholdProfiles: homeEnabled}
	}
	id.ResolveDeviceFunc = func(_ context.Context, token string, d frontend.Device) (*frontend.Viewer, time.Time, error) {
		mu.Lock()
		defer mu.Unlock()
		if revoked[token] || d.ID != "iphone-1" {
			return nil, time.Time{}, frontend.ErrUnauthorized
		}
		exp := time.Unix(1_900_000_000, 0).UTC()
		switch token {
		case "eg1_owner_grant", "eg1_profile_1":
			return owner, exp, nil
		case "eg1_profile_2":
			return kid, exp, nil
		case "eg1_profile_3":
			return guest, exp, nil
		}
		return nil, time.Time{}, frontend.ErrUnauthorized
	}
	id.ResolveFunc = func(ctx context.Context, token string) (*frontend.Viewer, error) {
		v, _, err := id.ResolveDeviceFunc(ctx, token, frontend.Device{ID: "iphone-1"})
		return v, err
	}
	id.IssueRefreshFunc = func(_ context.Context, access string, d frontend.Device) (string, error) {
		return "er1_refresh_" + d.ID + "_" + access, nil
	}
	id.SignInWithPlexFunc = func(_ context.Context, plexToken string, _ frontend.Device) (string, error) {
		mu.Lock()
		signInCalls++
		mu.Unlock()
		return "eg1_owner_grant", nil
	}
	id.RevokeFunc = func(_ context.Context, token string) error {
		mu.Lock()
		revoked[token] = true
		mu.Unlock()
		return nil
	}
	id.HouseholdProfilesFunc = func(_ context.Context, plexToken string) ([]frontend.HouseholdProfile, error) {
		mu.Lock()
		profilesCalls++
		mu.Unlock()
		switch plexToken {
		case "secret-owner-plex-token":
			return append([]frontend.HouseholdProfile(nil), profiles...), nil
		case "secret-single-plex-token":
			return profiles[:1], nil
		default:
			return nil, frontend.ErrUnauthorized
		}
	}
	id.HouseholdProfilesForViewerFunc = func(_ context.Context, v *frontend.Viewer) ([]frontend.HouseholdProfile, error) {
		if v == nil || (v.ID != 1 && v.ID != 2) {
			return nil, frontend.ErrHouseholdProfileIneligible
		}
		return append([]frontend.HouseholdProfile(nil), profiles...), nil
	}
	id.FetchHouseholdAvatarFunc = func(_ context.Context, req frontend.HouseholdAvatarRequest) ([]byte, string, error) {
		mu.Lock()
		lastAvatar = req
		mu.Unlock()
		if req.Profile.ViewerID == 2 && req.Profile.Thumb == "https://plex.tv/users/kid/avatar?c=9" {
			return []byte("\x89PNG-kid"), "image/png", nil
		}
		return nil, "", frontend.ErrPlexUnreachable
	}
	id.SignInWithHouseholdProfileFunc = func(_ context.Context, req frontend.HouseholdProfileSignIn) (string, error) {
		mu.Lock()
		completeCalls++
		lastComplete = req
		mu.Unlock()
		if req.Profile.Protected && req.PIN != "1234" {
			return "", frontend.ErrHouseholdProfilePIN
		}
		return fmt.Sprintf("eg1_profile_%d", req.Profile.ViewerID), nil
	}

	rawInst, err := (Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(t.Context(), frontend.Host{
		Identity:       id,
		Experience:     frontendtest.Experience{},
		Sessions:       frontendtest.Sessions{},
		ServerIdentity: frontend.ServerIdentity{MachineID: "mach-1", FriendlyName: "Ether", Version: "1.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rawInst.Close(t.Context())
	inst := rawInst.(*instance)
	h := rawInst.Handler()

	const deviceJSON = `{"id":"iphone-1","name":"iPhone","platform":"iOS","appVersion":"0.2.0"}`
	post := func(path, body, access string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if access != "" {
			r.Header.Set("Authorization", "Bearer "+access)
			r.Header.Set("X-Vela-Device-Id", "iphone-1")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	getCaps := func() map[string]any {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/vela/v0/capabilities", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("capabilities %d: %s", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	type selection struct {
		SelectionToken string `json:"selectionToken"`
		ExpiresAt      string `json:"expiresAt"`
		Profiles       []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Protected  bool   `json:"protected"`
			Restricted *bool  `json:"restricted"`
			AvatarURL  string `json:"avatarUrl"`
		} `json:"profiles"`
	}
	startSelection := func(idem string) selection {
		body := fmt.Sprintf(`{"plexToken":"secret-owner-plex-token","selectProfile":true,"idempotencyKey":%q,"device":%s}`, idem, deviceJSON)
		w := post("/vela/v0/auth/plex", body, "")
		if w.Code != http.StatusOK {
			t.Fatalf("start selection %s: %d %s", idem, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "secret-owner-plex-token") {
			t.Fatal("selection response leaked the plex token")
		}
		if strings.Contains(w.Body.String(), "plex.tv/users") {
			t.Fatal("selection response leaked a raw thumb URL")
		}
		var sel selection
		if err := json.Unmarshal(w.Body.Bytes(), &sel); err != nil {
			t.Fatal(err)
		}
		if sel.SelectionToken == "" || len(sel.Profiles) != 3 {
			t.Fatalf("selection = %+v", sel)
		}
		return sel
	}
	complete := func(sel selection, profileID, pin, idem string) *httptest.ResponseRecorder {
		pinJSON := ""
		if pin != "" {
			pinJSON = fmt.Sprintf(`,"pin":%q`, pin)
		}
		body := fmt.Sprintf(`{"selectionToken":%q,"profileId":%q%s,"idempotencyKey":%q,"device":%s}`, sel.SelectionToken, profileID, pinJSON, idem, deviceJSON)
		return post("/vela/v0/auth/plex/profile", body, "")
	}
	viewerName := func(w *httptest.ResponseRecorder) string {
		var out struct {
			AccessToken string `json:"accessToken"`
			Viewer      struct {
				Name string `json:"name"`
			} `json:"viewer"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.AccessToken == "" {
			t.Fatalf("tokens missing accessToken: %s", w.Body.String())
		}
		return out.Viewer.Name
	}
	errCode := func(w *httptest.ResponseRecorder) string {
		var out struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out.Error.Code
	}

	// Capability is advertised only when fully supported.
	if _, ok := getCaps()["plexHomeProfiles"]; ok {
		t.Fatal("plexHomeProfiles advertised while disabled")
	}
	homeEnabled = true
	if got := getCaps()["plexHomeProfiles"]; got != true {
		t.Fatalf("plexHomeProfiles = %v, want true", got)
	}
	if got := getCaps()["plexAccountSignIn"]; got != true {
		t.Fatalf("plexAccountSignIn = %v, want true", got)
	}

	// One eligible profile -> ordinary Tokens, no selection.
	single := post("/vela/v0/auth/plex", `{"plexToken":"secret-single-plex-token","selectProfile":true,"idempotencyKey":"single-1","device":`+deviceJSON+`}`, "")
	if single.Code != http.StatusOK || strings.Contains(single.Body.String(), "selectionToken") || viewerName(single) != "owner" {
		t.Fatalf("one-profile shortcut: %d %s", single.Code, single.Body.String())
	}
	mu.Lock()
	if signInCalls != 1 {
		t.Fatalf("shortcut sign-in calls = %d, want 1", signInCalls)
	}
	mu.Unlock()

	// Multi-profile selection shape + idempotent replay of the start.
	sel := startSelection("multi-1")
	if _, err := time.Parse(time.RFC3339, sel.ExpiresAt); err != nil {
		t.Fatalf("expiresAt %q: %v", sel.ExpiresAt, err)
	}
	wantIDs := []int64{1, 2, 3}
	for i, p := range sel.Profiles {
		got, err := ids.ParseGUID(ids.User, p.ID)
		if err != nil || got != wantIDs[i] || p.Name != profiles[i].Name || p.Protected != profiles[i].Protected {
			t.Fatalf("profile %d = %+v (parsed %d, %v)", i, p, got, err)
		}
	}
	mu.Lock()
	callsBefore := profilesCalls
	mu.Unlock()
	replaySel := startSelection("multi-1")
	if replaySel.SelectionToken != sel.SelectionToken {
		t.Fatal("start replay minted a new selection token")
	}
	mu.Lock()
	if profilesCalls != callsBefore {
		t.Fatal("start replay re-listed profiles")
	}
	mu.Unlock()

	kidID, guestID := sel.Profiles[1].ID, sel.Profiles[2].ID

	// restricted and avatarUrl: kid is restricted with a signed,
	// selection-bound avatar on the images path; the owner has an avatar
	// and no restricted; guest (no thumb) has neither. No raw thumb URL
	// or token appears in any avatarUrl.
	ownerP, kidP, guestP := sel.Profiles[0], sel.Profiles[1], sel.Profiles[2]
	if kidP.Restricted == nil || !*kidP.Restricted {
		t.Fatalf("kid restricted = %v, want true", kidP.Restricted)
	}
	if ownerP.Restricted != nil || guestP.Restricted != nil {
		t.Fatalf("restricted present when false: owner=%v guest=%v", ownerP.Restricted, guestP.Restricted)
	}
	for _, p := range []struct {
		name, url string
	}{{"owner", ownerP.AvatarURL}, {"kid", kidP.AvatarURL}} {
		if !strings.HasPrefix(p.url, "/vela/v0/images/plexavatar-") ||
			strings.Contains(p.url, "plex.tv") || strings.Contains(p.url, "secret-") || strings.Contains(p.url, "avatar?c=") {
			t.Fatalf("%s avatarUrl = %q", p.name, p.url)
		}
	}
	if guestP.AvatarURL != "" {
		t.Fatalf("guest avatarUrl = %q, want omitted (no thumb)", guestP.AvatarURL)
	}
	getAvatar := func(url string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		return w
	}
	// Usable before any session exists; the fetch sees the retained
	// token and thumb server-side only.
	if w := getAvatar(kidP.AvatarURL); w.Code != http.StatusOK || w.Header().Get("Content-Type") != "image/png" || !strings.Contains(w.Body.String(), "PNG-kid") {
		t.Fatalf("kid avatar: %d ct=%q body=%q", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	mu.Lock()
	if lastAvatar.PlexToken != "secret-owner-plex-token" || lastAvatar.Profile.ViewerID != 2 || lastAvatar.Profile.Thumb != "https://plex.tv/users/kid/avatar?c=9" {
		t.Fatalf("avatar fetch not server-side bound: %+v", lastAvatar)
	}
	mu.Unlock()
	// Upstream failure -> 502; tampered capability -> 404.
	if w := getAvatar(ownerP.AvatarURL); w.Code != http.StatusBadGateway || errCode(w) != "avatar_unavailable" {
		t.Fatalf("owner avatar upstream failure: %d %s", w.Code, w.Body.String())
	}
	if w := getAvatar(kidP.AvatarURL + "x"); w.Code != http.StatusNotFound {
		t.Fatalf("tampered avatar: %d %s", w.Code, w.Body.String())
	}
	if w := getAvatar("/vela/v0/images/plexavatar-not-a-capability"); w.Code != http.StatusNotFound {
		t.Fatalf("garbage avatar: %d %s", w.Code, w.Body.String())
	}

	// Ineligible profile (not in the selection) -> 403, selection survives.
	strangerID, _ := ids.GUID(ids.User, 99)
	if w := complete(sel, strangerID, "", "inel-1"); w.Code != http.StatusForbidden || errCode(w) != "profile_ineligible" {
		t.Fatalf("ineligible: %d %s", w.Code, w.Body.String())
	}

	// Bad PIN -> 401 and the selection stays usable; good PIN -> Tokens.
	if w := complete(sel, kidID, "0000", "pin-bad-1"); w.Code != http.StatusUnauthorized || errCode(w) != "invalid_pin" {
		t.Fatalf("bad PIN: %d %s", w.Code, w.Body.String())
	}
	goodBody := fmt.Sprintf(`{"selectionToken":%q,"profileId":%q,"pin":"1234","idempotencyKey":"pin-good-1","device":%s}`, sel.SelectionToken, kidID, deviceJSON)
	good := post("/vela/v0/auth/plex/profile", goodBody, "")
	if good.Code != http.StatusOK || viewerName(good) != "kid" {
		t.Fatalf("good PIN: %d %s", good.Code, good.Body.String())
	}
	mu.Lock()
	if lastComplete.PlexToken != "secret-owner-plex-token" || lastComplete.Profile.ViewerID != 2 {
		t.Fatalf("completion did not carry the retained token server-side: %+v", lastComplete)
	}
	completeBefore := completeCalls
	mu.Unlock()

	// Idempotent replay returns the same Tokens without completing again.
	replay := post("/vela/v0/auth/plex/profile", goodBody, "")
	if replay.Code != http.StatusOK || replay.Body.String() != good.Body.String() {
		t.Fatalf("replay: %d %s vs %s", replay.Code, replay.Body.String(), good.Body.String())
	}
	mu.Lock()
	if completeCalls != completeBefore {
		t.Fatal("replay completed the selection again")
	}
	mu.Unlock()

	// A fresh attempt on the consumed selection -> 409.
	if w := complete(sel, guestID, "", "consumed-1"); w.Code != http.StatusConflict || errCode(w) != "selection_consumed" {
		t.Fatalf("consumed: %d %s", w.Code, w.Body.String())
	}
	// Its avatars die with it.
	if w := getAvatar(kidP.AvatarURL); w.Code != http.StatusConflict || errCode(w) != "selection_consumed" {
		t.Fatalf("consumed avatar: %d %s", w.Code, w.Body.String())
	}

	// Unknown and expired selections -> 410.
	unknown := selection{SelectionToken: strings.Repeat("ab", 32)}
	if w := complete(unknown, guestID, "", "unknown-1"); w.Code != http.StatusGone || errCode(w) != "selection_expired" {
		t.Fatalf("unknown: %d %s", w.Code, w.Body.String())
	}
	expiring := startSelection("expiry-1")
	expiringKidAvatar := expiring.Profiles[1].AvatarURL
	inst.plexSelMu.Lock()
	inst.plexSels[hash(expiring.SelectionToken)].expires = time.Now().Add(-time.Minute)
	inst.plexSelMu.Unlock()
	if w := complete(expiring, guestID, "", "expired-1"); w.Code != http.StatusGone || errCode(w) != "selection_expired" {
		t.Fatalf("expired: %d %s", w.Code, w.Body.String())
	}
	if w := getAvatar(expiringKidAvatar); w.Code != http.StatusGone || errCode(w) != "selection_expired" {
		t.Fatalf("expired avatar: %d %s", w.Code, w.Body.String())
	}

	// Device binding: another device cannot spend the selection.
	bound := startSelection("device-1")
	otherBody := fmt.Sprintf(`{"selectionToken":%q,"profileId":%q,"idempotencyKey":"device-2","device":{"id":"tv-9","name":"TV","platform":"tvOS","appVersion":"0.2.0"}}`, bound.SelectionToken, guestID)
	if w := post("/vela/v0/auth/plex/profile", otherBody, ""); w.Code != http.StatusForbidden {
		t.Fatalf("device mismatch: %d %s", w.Code, w.Body.String())
	}
	// The bound device still can (unprotected profile needs no PIN).
	if w := complete(bound, guestID, "", "device-3"); w.Code != http.StatusOK || viewerName(w) != "guest" {
		t.Fatalf("unprotected completion: %d %s", w.Code, w.Body.String())
	}

	// PIN attempts are rate-limited per selection.
	limited := startSelection("pin-limit-1")
	for n := 0; n < plexSelectionMaxPIN; n++ {
		if w := complete(limited, kidID, "0000", fmt.Sprintf("pin-limit-try-%d", n)); w.Code != http.StatusUnauthorized {
			t.Fatalf("PIN try %d: %d %s", n, w.Code, w.Body.String())
		}
	}
	if w := complete(limited, kidID, "1234", "pin-limit-over"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("PIN limit: %d %s", w.Code, w.Body.String())
	}

	// Signed-in switch: challenge needs auth, completion revokes the old session.
	if w := post("/vela/v0/auth/plex/profiles/challenge", `{"idempotencyKey":"chal-anon","device":`+deviceJSON+`}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous challenge: %d %s", w.Code, w.Body.String())
	}
	chal := post("/vela/v0/auth/plex/profiles/challenge", `{"idempotencyKey":"chal-1","device":`+deviceJSON+`}`, "eg1_owner_grant")
	if chal.Code != http.StatusOK {
		t.Fatalf("challenge: %d %s", chal.Code, chal.Body.String())
	}
	var switchSel selection
	if err := json.Unmarshal(chal.Body.Bytes(), &switchSel); err != nil || len(switchSel.Profiles) != 3 {
		t.Fatalf("challenge selection: %+v %v", switchSel, err)
	}
	// Switch selections carry the same avatar/restricted fields, fetched
	// through the retained link (no client Plex token).
	if switchSel.Profiles[1].Restricted == nil || !*switchSel.Profiles[1].Restricted || switchSel.Profiles[1].AvatarURL == "" {
		t.Fatalf("switch kid profile = %+v", switchSel.Profiles[1])
	}
	if w := getAvatar(switchSel.Profiles[1].AvatarURL); w.Code != http.StatusOK {
		t.Fatalf("switch kid avatar: %d %s", w.Code, w.Body.String())
	}
	mu.Lock()
	if lastAvatar.PlexToken != "" || lastAvatar.ViewerID != 1 {
		t.Fatalf("switch avatar should use the retained link, got %+v", lastAvatar)
	}
	mu.Unlock()
	switched := complete(switchSel, kidID, "1234", "switch-1")
	if switched.Code != http.StatusOK || viewerName(switched) != "kid" {
		t.Fatalf("switch completion: %d %s", switched.Code, switched.Body.String())
	}
	mu.Lock()
	oldRevoked := revoked["eg1_owner_grant"]
	switchReq := lastComplete
	mu.Unlock()
	if !oldRevoked {
		t.Fatal("switch did not revoke the old session")
	}
	if switchReq.PlexToken != "" || switchReq.ViewerID != 1 {
		t.Fatalf("switch completion should use the retained link, got %+v", switchReq)
	}
	// The old access token no longer resolves.
	if w := post("/vela/v0/auth/plex/profiles/challenge", `{"idempotencyKey":"chal-old","device":`+deviceJSON+`}`, "eg1_owner_grant"); w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session challenge: %d %s", w.Code, w.Body.String())
	}
}
