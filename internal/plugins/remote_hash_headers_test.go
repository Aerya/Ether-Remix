package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

func installSecretTestPlugin(t *testing.T, env *testEnv, id string, network, secrets []string, schemaJSON string) Record {
	t.Helper()
	man := baseManifest()
	man.ID = id
	man.Name = "Hash Header Test"
	man.Ether.Permissions.Network = network
	man.Ether.Permissions.Secrets = secrets
	files := map[string][]byte{
		"dist/plugin.js": []byte(`export function handle(){ return {status:200,body:"ok"}; }`),
	}
	if schemaJSON != "" {
		man.Ether.Settings = json.RawMessage(`{"$ref":"settings.schema.json"}`)
		files["settings.schema.json"] = []byte(schemaJSON)
	}
	rawMan, err := json.Marshal(man)
	if err != nil {
		t.Fatalf("Marshal manifest: %v", err)
	}
	prev, err := env.m.PreviewInstall(context.Background(), Source{Kind: "studio", Manifest: rawMan, Files: files})
	if err != nil {
		t.Fatalf("PreviewInstall: %v", err)
	}
	rec, err := env.m.Install(context.Background(), prev, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	return rec
}

func TestHTTPHashHeadersSecretExpansionAndOriginConfinement(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	var (
		mu             sync.Mutex
		otherOriginHdr http.Header
		returnOriginH  http.Header
		sameOriginHdr  http.Header
		seenAcceptEnc  string
	)
	const payload = "authenticated remote file bytes"
	wantSHA := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))

	// Secondary origin B: receives cross-origin redirect from origin A.
	var originAURL string
	originB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		otherOriginHdr = r.Header.Clone()
		mu.Unlock()
		if r.URL.Path == "/bounce-back" {
			http.Redirect(w, r, originAURL+"/after-bounce", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(payload))
	}))
	defer originB.Close()

	// Primary origin A: enforces auth headers and tests same-origin, cross-origin, and A->B->A redirects.
	originA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth-file":
			mu.Lock()
			seenAcceptEnc = r.Header.Get("Accept-Encoding")
			mu.Unlock()
			if r.Header.Get("Cookie") != "sid=derived-session-xyz" || r.Header.Get("Authorization") != "Bearer owner-tok-abc" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(payload))
		case "/same-origin":
			http.Redirect(w, r, "/same-origin-target", http.StatusFound)
		case "/same-origin-target":
			mu.Lock()
			sameOriginHdr = r.Header.Clone()
			mu.Unlock()
			if r.Header.Get("Cookie") != "sid=derived-session-xyz" || r.Header.Get("X-Api-Key") != "owner-tok-abc" {
				http.Error(w, "missing auth on same-origin redirect", http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(payload))
		case "/cross-origin":
			http.Redirect(w, r, originB.URL+"/target", http.StatusFound)
		case "/bounce":
			http.Redirect(w, r, originB.URL+"/bounce-back", http.StatusFound)
		case "/after-bounce":
			mu.Lock()
			returnOriginH = r.Header.Clone()
			mu.Unlock()
			_, _ = w.Write([]byte(payload))
		default:
			http.NotFound(w, r)
		}
	}))
	defer originA.Close()
	originAURL = originA.URL

	uA, _ := url.Parse(originA.URL)
	uB, _ := url.Parse(originB.URL)

	rec := installSecretTestPlugin(
		t,
		env,
		"org.ether.hashhdr",
		[]string{uA.Hostname(), uB.Hostname()},
		[]string{"api_token", "session_cookie", "bad_crlf"},
		`{"fields":[{"key":"api_token","kind":"secret","label":"Access token"}]}`,
	)
	if _, err := env.m.Configure(ctx, rec.ID, map[string]any{"api_token": "owner-tok-abc"}); err != nil {
		t.Fatal(err)
	}
	env.m.client = originA.Client()

	h := env.m.HostFor(rec.ID).(*host)
	defer func() {
		env.m.cancelRemoteHashes(rec.ID)
		env.m.remoteHashMu.Lock()
		w := env.m.remoteHashes[rec.ID]
		env.m.remoteHashMu.Unlock()
		if w != nil {
			<-w.done
		}
	}()

	if err := h.SecretSet(ctx, "session_cookie", "sid=derived-session-xyz", 3600); err != nil {
		t.Fatalf("SecretSet: %v", err)
	}
	if err := h.SecretSet(ctx, "bad_crlf", "evil\r\nX-Injected: yes", 60); err != nil {
		t.Fatalf("SecretSet bad_crlf: %v", err)
	}

	waitHash := func(req HashRequest) HashResult {
		t.Helper()
		res, err := h.HTTPHash(ctx, req)
		if err != nil {
			t.Fatalf("HTTPHash(%s): %v", req.URL, err)
		}
		if res.Pending {
			env.m.remoteHashMu.Lock()
			w := env.m.remoteHashes[rec.ID]
			env.m.remoteHashMu.Unlock()
			select {
			case <-w.done:
				if w.err != nil {
					t.Fatalf("hash worker(%s): %v", req.URL, w.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("hash worker(%s) timed out", req.URL)
			}
			res, err = h.HTTPHash(ctx, req)
			if err != nil || res.Pending {
				t.Fatalf("second HTTPHash(%s): %+v %v", req.URL, res, err)
			}
		}
		return res
	}

	// 1. Authenticated file using both owner secret and derived secret succeeds,
	//    and caller Accept-Encoding is overridden to identity.
	res := waitHash(HashRequest{
		URL:  originA.URL + "/auth-file",
		Algo: "sha256",
		Headers: map[string]string{
			"Cookie":          "{secret:session_cookie}",
			"Authorization":   "Bearer {secret:api_token}",
			"Accept-Encoding": "gzip",
		},
	})
	if res.SHA256 != wantSHA {
		t.Fatalf("auth-file sha256 = %q, want %q", res.SHA256, wantSHA)
	}
	mu.Lock()
	gotAE := seenAcceptEnc
	mu.Unlock()
	if gotAE != "identity" {
		t.Fatalf("Accept-Encoding = %q, want identity", gotAE)
	}

	// Verify neither secret value was persisted in plugins_remote_hashes.
	var persisted int
	if err := env.m.store.db.QueryRow(`SELECT COUNT(*) FROM plugins_remote_hashes WHERE value LIKE '%derived-session%' OR value LIKE '%owner-tok%'`).Scan(&persisted); err != nil || persisted != 0 {
		t.Fatalf("secret leaked into plugins_remote_hashes: count=%d err=%v", persisted, err)
	}

	// 2. Same-origin redirect preserves expanded headers.
	res = waitHash(HashRequest{
		URL:  originA.URL + "/same-origin",
		Algo: "sha256",
		Headers: map[string]string{
			"Cookie":    "{secret:session_cookie}",
			"X-Api-Key": "{secret:api_token}",
		},
	})
	if res.SHA256 != wantSHA {
		t.Fatalf("same-origin sha256 = %q, want %q", res.SHA256, wantSHA)
	}
	mu.Lock()
	gotSameCookie := sameOriginHdr.Get("Cookie")
	gotSameKey := sameOriginHdr.Get("X-Api-Key")
	mu.Unlock()
	if gotSameCookie != "sid=derived-session-xyz" || gotSameKey != "owner-tok-abc" {
		t.Fatalf("same-origin redirect headers = Cookie:%q X-Api-Key:%q", gotSameCookie, gotSameKey)
	}

	// 3. Cross-origin redirect strips ALL caller headers (including custom headers & Referer),
	//    while preserving Accept-Encoding: identity.
	res = waitHash(HashRequest{
		URL:  originA.URL + "/cross-origin",
		Algo: "sha256",
		Headers: map[string]string{
			"Cookie":        "{secret:session_cookie}",
			"Authorization": "Bearer {secret:api_token}",
			"X-Api-Key":     "{secret:api_token}",
			"Referer":       originA.URL + "/private",
		},
	})
	if res.SHA256 != wantSHA {
		t.Fatalf("cross-origin sha256 = %q, want %q", res.SHA256, wantSHA)
	}
	mu.Lock()
	bHdr := otherOriginHdr.Clone()
	mu.Unlock()
	for _, k := range []string{"Cookie", "Authorization", "X-Api-Key", "Referer"} {
		if v := bHdr.Get(k); v != "" {
			t.Fatalf("cross-origin redirect leaked header %s=%q", k, v)
		}
	}
	if got := bHdr.Get("Accept-Encoding"); got != "identity" {
		t.Fatalf("cross-origin redirect Accept-Encoding = %q, want identity", got)
	}

	// 4. Redirect A -> B -> A does NOT restore stripped headers on return to A.
	res = waitHash(HashRequest{
		URL:  originA.URL + "/bounce",
		Algo: "sha256",
		Headers: map[string]string{
			"Cookie":    "{secret:session_cookie}",
			"X-Api-Key": "{secret:api_token}",
		},
	})
	if res.SHA256 != wantSHA {
		t.Fatalf("bounce sha256 = %q, want %q", res.SHA256, wantSHA)
	}
	mu.Lock()
	retHdr := returnOriginH.Clone()
	mu.Unlock()
	for _, k := range []string{"Cookie", "X-Api-Key"} {
		if v := retHdr.Get(k); v != "" {
			t.Fatalf("A->B->A redirect restored stripped header %s=%q", k, v)
		}
	}

	// 5. Validation failures reject synchronously before cache hit or worker start:
	//    hop-by-hop, framing/range, duplicate canonical, undeclared secret, bad template, CRLF in secret.
	for _, tc := range []struct {
		name    string
		headers map[string]string
		wantErr error
		noEcho  string
	}{
		{"hop-by-hop Connection", map[string]string{"Connection": "close"}, nil, ""},
		{"hop-by-hop Transfer-Encoding", map[string]string{"Transfer-Encoding": "chunked"}, nil, ""},
		{"hop-by-hop Proxy-Authorization", map[string]string{"Proxy-Authorization": "Basic abc"}, nil, ""},
		{"framing Host", map[string]string{"Host": "evil.example"}, nil, ""},
		{"framing Range", map[string]string{"Range": "bytes=0-10"}, nil, ""},
		{"duplicate canonical", map[string]string{"x-key": "1", "X-Key": "2"}, nil, ""},
		{"unclosed secret template", map[string]string{"Cookie": "{secret:session_cookie"}, nil, ""},
		{"CRLF in expanded secret", map[string]string{"Cookie": "{secret:bad_crlf}"}, nil, "evil"},
		{"undeclared secret", map[string]string{"Cookie": "{secret:not_declared}"}, ErrPermissionDenied, ""},
	} {
		_, err := h.HTTPHash(ctx, HashRequest{
			URL:     originA.URL + "/auth-file", // already fresh in cache; validation must still fail!
			Algo:    "sha256",
			Headers: tc.headers,
		})
		if err == nil {
			t.Fatalf("%s: expected error, got nil", tc.name)
		}
		if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
		}
		if tc.noEcho != "" && strings.Contains(err.Error(), tc.noEcho) {
			t.Fatalf("%s: error echoed secret value: %v", tc.name, err)
		}
	}

	// 6. Revoking secrets grant immediately blocks even a cached http.hash call that uses {secret:...}.
	if err := env.m.SetGrants(ctx, rec.ID, Grants{Revoked: []string{"secrets"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.HTTPHash(ctx, HashRequest{
		URL:     originA.URL + "/auth-file",
		Algo:    "sha256",
		Headers: map[string]string{"Cookie": "{secret:session_cookie}"},
	}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("revoked secrets grant on http.hash: err = %v, want ErrPermissionDenied", err)
	}
}

func TestDerivedSecretsIsolationTTLAndRedaction(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t)

	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	env.m.now = func() time.Time { return now }
	env.m.store.now = env.m.now

	rec := installSecretTestPlugin(
		t,
		env,
		"org.ether.derivedsec",
		nil,
		[]string{"password", "session_token", "manual_secret"},
		`{"fields":[{"key":"password","kind":"secret","label":"Password"}]}`,
	)
	if _, err := env.m.Configure(ctx, rec.ID, map[string]any{"password": "owner-password-1", "manual_secret": "owner-manual-1"}); err != nil {
		t.Fatal(err)
	}

	h := env.m.HostFor(rec.ID)

	// 1. Plugin cannot overwrite or delete owner-configured secrets (schema-declared or owner-saved).
	for _, ownerKey := range []string{"password", "manual_secret"} {
		if err := h.SecretSet(ctx, ownerKey, "hijacked", 60); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("SecretSet(%q) err = %v, want ErrPermissionDenied", ownerKey, err)
		}
		if err := h.SecretDelete(ctx, ownerKey); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("SecretDelete(%q) err = %v, want ErrPermissionDenied", ownerKey, err)
		}
	}
	if got, err := h.SecretGet(ctx, "password"); err != nil || got != "owner-password-1" {
		t.Fatalf("owner password changed: %q %v", got, err)
	}

	// 2. Undeclared names, read-only tool contexts, oversize values, and invalid TTLs are rejected.
	if err := h.SecretSet(ctx, "undeclared", "val", 60); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("undeclared SecretSet err = %v, want ErrPermissionDenied", err)
	}
	roCtx := context.WithValue(ctx, readOnlyToolContextKey{}, true)
	if err := h.SecretSet(roCtx, "session_token", "tok", 60); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("read-only SecretSet err = %v, want ErrPermissionDenied", err)
	}
	if err := h.SecretSet(ctx, "session_token", strings.Repeat("x", MaxDerivedSecretBytes+1), 60); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("oversize SecretSet err = %v, want ErrQuotaExceeded", err)
	}
	var ve *ValidationError
	if err := h.SecretSet(ctx, "session_token", "tok", -5); !errors.As(err, &ve) {
		t.Fatalf("negative TTL err = %v, want ValidationError", err)
	}

	// 3. Derived secret can be written, read via SecretGet, redacted from logs,
	//    does not leak into GetSettings(), and expires after ttlSec.
	const derivedVal = "sess_super_secret_token_42"
	if err := h.SecretSet(ctx, "session_token", derivedVal, 120); err != nil {
		t.Fatalf("SecretSet(session_token): %v", err)
	}
	if got, err := h.SecretGet(ctx, "session_token"); err != nil || got != derivedVal {
		t.Fatalf("SecretGet(session_token) = %q %v, want %q", got, err, derivedVal)
	}

	// Settings UI must NOT show derived secrets as owner settings.
	settingsMap, _, err := env.m.GetSettings(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := settingsMap["session_token"]; exists {
		t.Fatalf("derived secret leaked into Manager.GetSettings(): %+v", settingsMap)
	}

	// Plugin logs must redact derived secrets.
	if err := h.Log(ctx, "info", "oops leaked "+derivedVal, nil); err != nil {
		t.Fatal(err)
	}
	logs, err := env.m.store.logs(ctx, rec.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range logs {
		if strings.Contains(l, derivedVal) {
			t.Fatalf("derived secret not redacted from plugin logs: %q", l)
		}
	}

	// Hosted QuickJS bridge supports secret.set / secret.get / secret.delete and validates bad TTL.
	bridge := hostBridge{h: h}
	if _, err := bridge.CallHost(ctx, "secret.set", json.RawMessage(`["session_token","bridge-tok-99",60]`)); err != nil {
		t.Fatalf("bridge secret.set: %v", err)
	}
	out, err := bridge.CallHost(ctx, "secret.get", json.RawMessage(`["session_token"]`))
	if err != nil || string(out) != `"bridge-tok-99"` {
		t.Fatalf("bridge secret.get = %s %v", string(out), err)
	}
	if _, err := bridge.CallHost(ctx, "secret.set", json.RawMessage(`["session_token","bad",1.5]`)); err == nil {
		t.Fatal("bridge secret.set with fractional ttl should fail")
	} else {
		var fault *wasm.Fault
		if !errors.As(err, &fault) || fault.Code != string(wasm.StatusInvalidInput) {
			t.Fatalf("fractional ttl fault = %v, want invalid_input", err)
		}
	}

	// Advance clock past 60s TTL -> SecretGet returns ErrUnavailable.
	now = now.Add(61 * time.Second)
	if _, err := h.SecretGet(ctx, "session_token"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired derived secret err = %v, want ErrUnavailable", err)
	}

	// Empty value and SecretDelete both clear a derived secret.
	if err := h.SecretSet(ctx, "session_token", "fresh-tok", 0); err != nil {
		t.Fatal(err)
	}
	if err := h.SecretSet(ctx, "session_token", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := h.SecretGet(ctx, "session_token"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("cleared derived secret err = %v, want ErrUnavailable", err)
	}
	if err := h.SecretSet(ctx, "session_token", "before-delete", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.CallHost(ctx, "secret.delete", json.RawMessage(`["session_token"]`)); err != nil {
		t.Fatalf("bridge secret.delete: %v", err)
	}
	if _, err := h.SecretGet(ctx, "session_token"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("deleted derived secret err = %v, want ErrUnavailable", err)
	}

	// Uninstall purges plugins_derived_secrets.
	if err := h.SecretSet(ctx, "session_token", "purge-me", 0); err != nil {
		t.Fatal(err)
	}
	if err := env.m.Remove(ctx, rec.ID, false); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := env.m.store.db.QueryRow(`SELECT COUNT(*) FROM plugins_derived_secrets WHERE plugin=?`, rec.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("derived secrets not purged on uninstall: remaining=%d err=%v", remaining, err)
	}
}
