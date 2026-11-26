package admin_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
)

type webPlayerFrontends struct{ allConnectFrontends }

func (webPlayerFrontends) List(context.Context) ([]admin.FrontendInfo, error) {
	return []admin.FrontendInfo{
		{Descriptor: frontend.Descriptor{ID: "vela"}, Enabled: true, Status: frontend.Status{Mounted: true, WebPlayerReady: true}},
		{Descriptor: frontend.Descriptor{ID: "jellyfin"}, Enabled: true, Status: frontend.Status{Mounted: true}},
	}, nil
}

// A console reached through a reverse tunnel arrives on loopback with the
// tunnel's own host: the web players must open on the browser's origin.
func TestWebPlayerBehindLoopbackTunnelOpensOnConsoleOrigin(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	local, _ := identity.NewLocal(db)
	viewerID, _ := local.CreateViewer(ctx, "alice", "password-1234")
	if err := users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	cfgStore := newStore(t, nil)
	grants, err := identity.NewGrants(db, users, time.Hour, nil, cfgStore)
	if err != nil {
		t.Fatal(err)
	}
	portal, _ := grants.Issue(ctx, viewerID, nil, "viewer-portal", core.Device{})
	h := admin.NewHandler(admin.Deps{
		Config: cfgStore, Frontends: webPlayerFrontends{}, IdentityGrants: grants,
		// What the shared resolver derives from the tunnel's request.
		ViewerPortalURL: func(context.Context, *http.Request) (string, error) {
			return "https://127.0.0.1:32550/ether/me", nil
		},
	})
	players := func() map[string]string {
		r := req(http.MethodGet, "/ether/api/web-player", "")
		r.RemoteAddr = "127.0.0.1:50122"
		r.Host = "127.0.0.1:32550"
		r.Header.Set("Origin", "http://127.0.0.1:32550")
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "203.0.113.7")
		r.Header.Set("Authorization", "Bearer "+portal)
		rec := serve(h, r)
		var out struct {
			Players []struct{ ID, URL string } `json:"players"`
		}
		decode(t, rec, &out)
		got := map[string]string{}
		for _, p := range out.Players {
			got[p.ID] = p.URL
		}
		return got
	}
	if got := players(); got["vela"] != "/vela/" || got["jellyfin"] != "/jellyfin/web/ether-signin" {
		t.Fatalf("tunnelled players = %#v, want same-origin paths", got)
	}
	// An address the owner published still wins.
	if err := cfgStore.Update(func(c *config.Config) error {
		c.Frontends = map[string]config.Frontend{"vela": {Enabled: true, PublishedAddresses: []string{"https://vela.example.com"}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := players(); got["vela"] != "https://vela.example.com/" {
		t.Fatalf("published vela = %q, want https://vela.example.com/", got["vela"])
	}
}
