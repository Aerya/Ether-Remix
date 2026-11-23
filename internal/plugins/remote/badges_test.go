package remote

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/remotetest"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestRemotePluginHostBadgesEndpointsAndQuota(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	mgr, err := plugins.New(plugins.Deps{
		DB:       db,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		IsLeader: func() bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.SetBadgeQuotaForTest(2)

	manifest := `{
		"id": "community.example.remotebadges",
		"name": "Remote Badges",
		"version": "1.0.0",
		"ether": {
			"protocol": 1,
			"kind": "integration",
			"summary": "Pushes poster badges from a paired sidecar",
			"runtime": { "kind": "remote", "mode": "paired" },
			"capabilities": ["poster_badges"],
			"contributes": {
				"posterBadges": [
					{ "key": "dubs", "label": "Dubbed Audio" },
					{ "key": "cuts", "label": "Special Cuts" }
				]
			}
		}
	}`
	if _, err := db.ExecContext(ctx,
		`INSERT INTO plugins_installed(id,name,version,manifest,enabled,installed_at,updated_at) VALUES(?,?,?,?,1,0,0)`,
		"community.example.remotebadges", "Remote Badges", "1.0.0", manifest,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO plugins_grants(plugin,grants) VALUES(?, '{}')`,
		"community.example.remotebadges",
	); err != nil {
		t.Fatal(err)
	}

	handler := NewHostHandler(HostOptions{
		Host: HostAdapter{
			HostFor:             mgr.HostFor,
			SettingsWithSecrets: mgr.PluginSettings,
		},
		Auth: remotetest.Credentials{
			"cred-badges": "community.example.remotebadges",
		},
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	doReq := func(method, path, cred, body string) (int, map[string]any) {
		t.Helper()
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, rdr)
		if err != nil {
			t.Fatal(err)
		}
		if cred != "" {
			req.Header.Set(HeaderCredential, cred)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	// Unauthenticated call returns 401.
	if status, _ := doReq(http.MethodGet, "/ether/plugin-host/badges/usage", "", ""); status != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d, want 401", status)
	}

	// PUT /ether/plugin-host/badges/{setKey} and POST /ether/plugin-host/badges/{setKey}/set.
	status, out := doReq(http.MethodPut, "/ether/plugin-host/badges/dubs", "cred-badges",
		`{"entries":[{"title":{"type":"movie","tmdb":603},"labels":["en dub"]}]}`)
	if status != http.StatusOK || out["ok"] != true {
		t.Fatalf("PUT badges/dubs = %d %+v", status, out)
	}
	status, out = doReq(http.MethodPost, "/ether/plugin-host/badges/cuts/set", "cred-badges",
		`{"entries":[{"title":{"type":"show","imdb":"tt0944947"},"labels":["extended"]}]}`)
	if status != http.StatusOK || out["ok"] != true {
		t.Fatalf("POST badges/cuts/set = %d %+v", status, out)
	}

	// Usage reflects 2 distinct titles and quota 2.
	status, out = doReq(http.MethodGet, "/ether/plugin-host/badges/usage", "cred-badges", "")
	if status != http.StatusOK || out["titles"] != float64(2) || out["quotaTitles"] != float64(2) {
		t.Fatalf("GET badges/usage = %d %+v", status, out)
	}

	// Exceeding distinct title quota returns 413 with code "quota_exceeded".
	status, out = doReq(http.MethodPut, "/ether/plugin-host/badges/dubs", "cred-badges",
		`{"entries":[{"title":{"type":"movie","tmdb":999},"labels":["fr dub"]}]}`)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("quota overflow status = %d (%+v), want 413", status, out)
	}
	errObj, _ := out["error"].(map[string]any)
	if errObj["code"] != "quota_exceeded" {
		t.Fatalf("quota overflow error = %+v, want code=quota_exceeded", out)
	}

	// Invalid label returns 400.
	status, _ = doReq(http.MethodPut, "/ether/plugin-host/badges/dubs", "cred-badges",
		`{"entries":[{"title":{"type":"movie","tmdb":603},"labels":["bad!label"]}]}`)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid label status = %d, want 400", status)
	}

	// DELETE /ether/plugin-host/badges/{setKey} and POST /ether/plugin-host/badges/{setKey}/clear.
	status, _ = doReq(http.MethodDelete, "/ether/plugin-host/badges/dubs", "cred-badges", "")
	if status != http.StatusOK {
		t.Fatalf("DELETE badges/dubs status = %d", status)
	}
	status, _ = doReq(http.MethodPost, "/ether/plugin-host/badges/cuts/clear", "cred-badges", "")
	if status != http.StatusOK {
		t.Fatalf("POST badges/cuts/clear status = %d", status)
	}
	status, out = doReq(http.MethodGet, "/ether/plugin-host/badges/usage", "cred-badges", "")
	if status != http.StatusOK || out["titles"] != float64(0) {
		t.Fatalf("usage after clear = %d %+v", status, out)
	}

	// Revoking poster_badges returns 403.
	if err := mgr.SetGrants(ctx, "community.example.remotebadges", plugins.Grants{Revoked: []string{"poster_badges"}}); err != nil {
		t.Fatal(err)
	}
	if status, _ := doReq(http.MethodGet, "/ether/plugin-host/badges/usage", "cred-badges", ""); status != http.StatusForbidden {
		t.Fatalf("revoked status = %d, want 403", status)
	}
}
