package plextv

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHomeUsersAndSwitchPIN(t *testing.T) {
	const ownerToken = "owner-secret-token"
	var switchCalls []string
	combined := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/home/users":
			if r.Header.Get("X-Plex-Token") != ownerToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<MediaContainer size="3">
<User id="42" uuid="u42" username="owner" title="Owner" thumb="https://plex.tv/users/owner/avatar?c=7" protected="1" homeAdmin="1"/>
<User id="300" uuid="u300" username="kid" title="Kid" thumb="https://plex.tv/users/kid/avatar?c=9" protected="1" restricted="1"/>
<User id="301" uuid="u301" username="guest" title="Guest" protected="0"/>
</MediaContainer>`))
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/home/users/"):
			switchCalls = append(switchCalls, r.URL.String())
			pin := r.URL.Query().Get("pin")
			w.Header().Set("Content-Type", "application/xml")
			switch {
			case strings.Contains(r.URL.Path, "/300/"):
				if pin != "1234" {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`<MediaContainer><error code="1001" message="Incorrect PIN"/></MediaContainer>`))
					return
				}
				_, _ = w.Write([]byte(`<MediaContainer><User id="300" authenticationToken="kid-switched-token"/></MediaContainer>`))
			case strings.Contains(r.URL.Path, "/42/"):
				if pin != "9999" {
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`Unauthorized`))
					return
				}
				_, _ = w.Write([]byte(`<MediaContainer authenticationToken="owner-switched-token"/>`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		case r.Method == http.MethodGet && r.URL.Path == "/avatar/kid":
			if r.Header.Get("X-Plex-Token") != ownerToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("redir") == "evil" {
				http.Redirect(w, r, "http://127.0.0.1:1/secret", http.StatusFound)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nkid-avatar"))
		case r.Method == http.MethodGet && r.URL.Path == "/avatar/text":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("not an image"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/user":
			w.Header().Set("Content-Type", "application/json")
			switch r.Header.Get("X-Plex-Token") {
			case "kid-switched-token":
				_, _ = w.Write([]byte(`{"id":300,"username":"kid"}`))
			case "owner-switched-token":
				_, _ = w.Write([]byte(`{"id":42,"username":"owner"}`))
			default:
				w.WriteHeader(http.StatusUnauthorized)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer combined.Close()

	c, err := New(Options{BaseURL: combined.URL, RetryBackoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	users, err := c.HomeUsers(ctx, ownerToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 3 || users[0].ID != 42 || !users[0].Protected || !users[0].HomeAdmin ||
		users[1].ID != 300 || users[1].DisplayName() != "Kid" || !users[1].Restricted ||
		users[2].ID != 301 || users[2].Protected {
		t.Fatalf("home users = %+v", users)
	}
	if users[1].Thumb != "https://plex.tv/users/kid/avatar?c=9" || users[2].Thumb != "" || users[0].Restricted || users[2].Restricted {
		t.Fatalf("home user thumbs/restricted = %+v", users)
	}

	// Avatar proxy fetch: token header to the avatar host, image bytes out;
	// off-list hosts, plain HTTP, non-images and evil redirects are refused.
	body, contentType, err := c.HomeUserAvatar(ctx, ownerToken, combined.URL+"/avatar/kid?c=9")
	if err != nil || contentType != "image/png" || !strings.Contains(string(body), "kid-avatar") {
		t.Fatalf("avatar: ct=%q body=%q err=%v", contentType, body, err)
	}
	for name, bad := range map[string]string{
		"off-list host": "https://evil.example/avatar.png",
		"plain http":    "http://plex.tv/users/kid/avatar",
		"non-image":     combined.URL + "/avatar/text",
		"evil redirect": combined.URL + "/avatar/kid?redir=evil",
		"relative":      "/users/kid/avatar",
		"empty":         "",
	} {
		if _, _, err := c.HomeUserAvatar(ctx, ownerToken, bad); !errors.Is(err, ErrAvatarUnavailable) {
			t.Fatalf("avatar %s: err = %v, want ErrAvatarUnavailable", name, err)
		}
	}
	if _, err := c.HomeUsers(ctx, "bad-token"); err == nil {
		t.Fatal("bad token listed home users")
	}

	// Protected member without a PIN -> PIN required; wrong PIN -> incorrect.
	if _, err := c.HomeUserToken(ctx, ownerToken, 300, ""); !errors.Is(err, ErrHomePINRequired) {
		t.Fatalf("no PIN err = %v, want ErrHomePINRequired", err)
	}
	if _, err := c.HomeUserToken(ctx, ownerToken, 300, "0000"); !errors.Is(err, ErrHomePINIncorrect) {
		t.Fatalf("bad PIN err = %v, want ErrHomePINIncorrect", err)
	}
	tok, err := c.HomeUserToken(ctx, ownerToken, 300, "1234")
	if err != nil || tok != "kid-switched-token" {
		t.Fatalf("good PIN: tok=%q err=%v", tok, err)
	}
	// Owner: plex says only "Unauthorized" without a PIN; the protected
	// lookup must turn that into PIN-required, and a wrong PIN into incorrect.
	if _, err := c.HomeUserToken(ctx, ownerToken, 42, ""); !errors.Is(err, ErrHomePINRequired) {
		t.Fatalf("owner no PIN err = %v, want ErrHomePINRequired", err)
	}
	if _, err := c.HomeUserToken(ctx, ownerToken, 42, "0000"); !errors.Is(err, ErrHomePINIncorrect) {
		t.Fatalf("owner bad PIN err = %v, want ErrHomePINIncorrect", err)
	}
	if tok, err := c.HomeUserToken(ctx, ownerToken, 42, "9999"); err != nil || tok != "owner-switched-token" {
		t.Fatalf("owner good PIN: tok=%q err=%v", tok, err)
	}
	for _, call := range switchCalls {
		if strings.Contains(call, ownerToken) {
			t.Fatalf("owner token leaked into URL: %s", call)
		}
	}
}
