package vela

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

type themeIdentity struct {
	identityFake
	revoked bool
}

func (f *themeIdentity) GrantScopeCurrent(context.Context, string, frontend.Device) (*frontend.Viewer, error) {
	if f.revoked {
		return nil, frontend.ErrUnauthorized
	}
	return &frontend.Viewer{ID: 1}, nil
}

type themeExperience struct {
	frontendtest.Experience
	denied bool
	title  frontend.Title
}

func (f *themeExperience) Item(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
	if f.denied {
		return frontend.ItemDetail{}, frontend.ErrNotFound
	}
	return frontend.ItemDetail{Title: f.title}, nil
}

type themeCacheFake struct{ reads int }

func (*themeCacheFake) ThemeEnabled() bool              { return true }
func (*themeCacheFake) Theme(frontend.Title, bool) bool { return true }
func (f *themeCacheFake) ServeTheme(w http.ResponseWriter, r *http.Request, _ frontend.Title) {
	f.reads++
	w.WriteHeader(206)
}

func TestThemeGrantScope(t *testing.T) {
	identity := &themeIdentity{}
	experience := &themeExperience{title: frontend.Title{Key: core.ShowKey(42), Title: "Example"}}
	cache := &themeCacheFake{}
	i := &instance{host: frontend.Host{Identity: identity, Experience: experience, Themes: cache, ThemeTitle: func(ctx context.Context, v *frontend.Viewer, k frontend.RatingKey) (*frontend.Title, error) {
		d, e := experience.Item(ctx, frontend.ItemRequest{Viewer: v, Key: k})
		return &d.Title, e
	}}}
	req := httptest.NewRequest("GET", "https://bound.example/vela/v0/titles/show", nil)
	req.Header.Set("X-Vela-Device-Id", "device-one")
	wire := i.detailTheme(req, "token", experience.title)
	if wire == nil || wire["expiresAt"] == nil {
		t.Fatal("grant missing")
	}
	path := wire["url"].(string)
	secret := strings.Split(path, "/")[4]
	read := func(host string) int {
		r := httptest.NewRequest("GET", "https://"+host+path, nil)
		r.SetPathValue("secret", secret)
		rec := httptest.NewRecorder()
		i.theme(rec, r)
		return rec.Code
	}
	if read("bound.example") != 206 {
		t.Fatal("valid grant rejected")
	}
	if read("other.example") != 404 {
		t.Fatal("cross-origin grant accepted")
	}
	identity.revoked = true
	if read("bound.example") != 404 {
		t.Fatal("revoked family accepted")
	}
	identity.revoked = false
	experience.denied = true
	if read("bound.example") != 404 {
		t.Fatal("current title policy ignored")
	}
	experience.denied = false
	grant := i.themes[secret]
	grant.expires = time.Now().Add(-time.Second)
	i.themes[secret] = grant
	if read("bound.example") != 404 {
		t.Fatal("expired grant accepted")
	}
	if cache.reads != 1 {
		t.Fatal("unauthorized request reached cache")
	}
}
