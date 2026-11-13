package app

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/pse/music"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestMusicAccessTokenLifecycle(t *testing.T) {
	db := store.OpenTest(t)
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, cfg: cfg}
	for _, id := range []int{7, 8} {
		if _, err := db.Exec(`INSERT INTO auth_users(id,username,created_at,updated_at) VALUES(?,?,0,0)`, id, fmt.Sprint("viewer", id)); err != nil {
			t.Fatal(err)
		}
	}
	users, err := auth.New(t.Context(), db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a.users = users
	call := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		a.pseMusicAccess(w, httptest.NewRequest(method, "/music/access", strings.NewReader(body)))
		return w
	}
	create := func(id int) string {
		w := call("POST", fmt.Sprintf(`{"viewer":%d}`, id))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var v map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if len(v["password"]) != 64 || v["username"] != musicUsername(int64(id)) {
			t.Fatal("bad generated credentials")
		}
		var saved string
		if err := db.QueryRow("SELECT secret FROM pse_music_access WHERE viewer_id=?", id).Scan(&saved); err != nil {
			t.Fatal(err)
		}
		if !config.IsSealed(saved) || strings.Contains(saved, v["password"]) {
			t.Fatal("secret not encrypted")
		}
		listing := call("GET", "")
		if listing.Header().Get("Cache-Control") != "no-store" || strings.Contains(listing.Body.String(), v["password"]) || strings.Contains(listing.Body.String(), saved) {
			t.Fatal("secret leaked by listing")
		}
		return v["password"]
	}
	secret := create(7)
	_ = create(8)
	srv := music.NewServer(music.Options{TokenAuth: a.pseMusicTokenAuth})
	token := func(secret, salt string) string {
		sum := md5.Sum([]byte(secret + salt))
		return hex.EncodeToString(sum[:])
	}
	request := func(user, secret, salt, method string) string {
		q := url.Values{"u": {user}, "t": {token(secret, salt)}, "s": {salt}, "f": {"json"}, "c": {"Arpeggi"}, "v": {"1.16.1"}}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest("GET", "/rest/"+method+"?"+q.Encode(), nil))
		return w.Body.String()
	}
	for _, method := range []string{"ping.view", "getLicense.view", "getMusicFolders.view", "getUser.view"} {
		if body := request("music-7", secret, "salt-one", method); !strings.Contains(body, `"status":"ok"`) {
			t.Fatal(method, body)
		}
	}
	if body := request("music-8", secret, "salt-two", "ping.view"); !strings.Contains(body, `"code":40`) {
		t.Fatal("cross-viewer access", body)
	}
	// Same salt/token must be revalidated, not kept in the password cache.
	replacement := create(7)
	if body := request("music-7", secret, "salt-one", "ping.view"); !strings.Contains(body, `"code":40`) {
		t.Fatal("rotated secret accepted", body)
	}
	if body := request("music-7", replacement, "salt-one", "ping.view"); !strings.Contains(body, `"status":"ok"`) {
		t.Fatal(body)
	}
	if _, err := db.Exec("UPDATE auth_users SET active=0 WHERE id=7"); err != nil {
		t.Fatal(err)
	}
	if body := request("music-7", replacement, "salt-one", "ping.view"); !strings.Contains(body, `"code":40`) {
		t.Fatal("inactive accepted", body)
	}
	if _, err := db.Exec("UPDATE auth_users SET active=1 WHERE id=7"); err != nil {
		t.Fatal(err)
	}
	if w := call("DELETE", `{"viewer":7}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if body := request("music-7", replacement, "salt-one", "ping.view"); !strings.Contains(body, `"code":40`) {
		t.Fatal("revoked secret accepted", body)
	}
	if w := call("POST", `{"viewer":999}`); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := call("POST", `{"viewer":0}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := call("PATCH", `{"viewer":7}`); w.Code != 405 {
		t.Fatal(w.Code)
	}
	// Copying an encrypted credential across rows must fail its associated data.
	copiedSecret := create(7)
	if _, err := db.Exec("UPDATE pse_music_access SET secret=(SELECT secret FROM pse_music_access WHERE viewer_id=7) WHERE viewer_id=8"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.pseMusicTokenAuth(t.Context(), "music-8", token(copiedSecret, "salt-one"), "salt-one"); err == nil {
		t.Fatal("copied secret accepted")
	}
}
