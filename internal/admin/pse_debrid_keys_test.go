package admin_test

import (
	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"strings"
	"testing"
)

func TestPseDebridKeyRequiresOwnerAndExplicitRequest(t *testing.T) {
	store := newStore(t, debridConfig)
	h := admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store)})
	for _, tc := range []struct {
		name, method, id, body, token string
		csrf                          bool
		want                          int
	}{
		{"owner", "POST", "rd", `{"kind":"realdebrid"}`, ownerToken, true, 200},
		{"other account", "POST", "tb", `{"kind":"torbox"}`, ownerToken, true, 200},
		{"anonymous", "POST", "rd", `{"kind":"realdebrid"}`, "", true, 401},
		{"viewer", "POST", "rd", `{"kind":"realdebrid"}`, "viewer-token", true, 401},
		{"csrf", "POST", "rd", `{"kind":"realdebrid"}`, ownerToken, false, 403},
		{"wrong type", "POST", "rd", `{"kind":"torbox"}`, ownerToken, true, 409},
		{"missing", "POST", "missing", `{"kind":"realdebrid"}`, ownerToken, true, 404},
		{"get", "GET", "rd", "", ownerToken, true, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := req(tc.method, "/ether/api/debrid/providers/"+tc.id+"/key", tc.body)
			if tc.token != "" {
				r.Header.Set("X-Plex-Token", tc.token)
			}
			if !tc.csrf {
				r.Header.Del("X-Ether-Request")
			}
			rec := serve(h, r)
			if rec.Code != tc.want {
				t.Fatalf("status %d want %d: %s", rec.Code, tc.want, rec.Body)
			}
			if tc.want == 200 {
				expected := tc.id + "-secret"
				if !strings.Contains(rec.Body.String(), expected) || !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
					t.Fatal("missing account key or no-store")
				}
				other := "tb-secret"
				if tc.id == "tb" {
					other = "rd-secret"
				}
				if strings.Contains(rec.Body.String(), other) {
					t.Fatal("another account exposed")
				}
			} else if strings.Contains(rec.Body.String(), "-secret") {
				t.Fatal("secret on rejected request")
			}
		})
	}
	// Even tokenless LAN setup access must never reveal an existing account key.
	setup := newStore(t, func(c *config.Config) { debridConfig(c); c.SetupComplete = false; c.Plex.Claimed = false })
	pre := admin.NewHandler(admin.Deps{Config: setup})
	rec := serve(pre, req("POST", "/ether/api/debrid/providers/rd/key", `{"kind":"realdebrid"}`))
	if rec.Code != 403 || strings.Contains(rec.Body.String(), "rd-secret") {
		t.Fatalf("setup key exposure: %d", rec.Code)
	}
}
