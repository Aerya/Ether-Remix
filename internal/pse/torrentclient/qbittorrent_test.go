package torrentclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const hashA = "0123456789abcdef0123456789abcdef01234567"

// fakeQB imite l'API WebUI v2 de qBittorrent.
type fakeQB struct {
	mu       sync.Mutex
	logins   int
	expire   bool // la prochaine requête répond 403 (session expirée)
	added    []url.Values
	tagged   []string
	torrents []map[string]any
}

func (f *fakeQB) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Referer") == "" {
			t.Errorf("%s sans Referer", r.URL.Path)
		}
		if r.URL.Path == "/qb/api/v2/auth/login" {
			_ = r.ParseForm()
			if r.Form.Get("username") != "admin" || r.Form.Get("password") != "secret" {
				_, _ = w.Write([]byte("Fails."))
				return
			}
			f.logins++
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "ok", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
			return
		}
		if c, err := r.Cookie("SID"); err != nil || c.Value != "ok" || f.expire {
			f.expire = false
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/qb/api/v2/app/version":
			_, _ = w.Write([]byte("v5.0.2"))
		case "/qb/api/v2/torrents/createCategory":
			w.WriteHeader(http.StatusConflict)
		case "/qb/api/v2/torrents/add":
			_ = r.ParseForm()
			f.added = append(f.added, r.PostForm)
			_, _ = w.Write([]byte("Ok."))
		case "/qb/api/v2/torrents/addTags":
			_ = r.ParseForm()
			f.tagged = append(f.tagged, r.PostForm.Get("hashes")+"="+r.PostForm.Get("tags"))
		case "/qb/api/v2/torrents/info":
			want := strings.Split(r.URL.Query().Get("hashes"), "|")
			cat := r.URL.Query().Get("category")
			var out []map[string]any
			for _, tr := range f.torrents {
				if r.URL.Query().Has("hashes") && !contains(want, tr["hash"].(string)) {
					continue
				}
				if cat != "" && tr["category"] != cat {
					continue
				}
				out = append(out, tr)
			}
			_ = json.NewEncoder(w).Encode(out)
		case "/qb/api/v2/torrents/files":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"name": "Film.2024/Film.2024.mkv", "size": 4}, {"name": "Film.2024/info.nfo", "size": 1}})
		default:
			http.NotFound(w, r)
		}
	})
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestQBittorrentAddListFilesAndRelogin(t *testing.T) {
	f := &fakeQB{torrents: []map[string]any{{"hash": hashA, "name": "Film.2024", "category": "remix", "progress": 1.0,
		"state": "stalledUP", "ratio": 0.4, "seeding_time": 3600, "save_path": "/downloads/remix", "tags": "a, remix-xseed"}}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	u.User = url.UserPassword("admin", "secret")
	u.Path = "/qb/"
	q, err := NewQBittorrent(u.String(), "", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if v, err := q.Version(ctx); err != nil || v != "v5.0.2" {
		t.Fatalf("version %q %v", v, err)
	}
	if err := q.Add(ctx, AddOptions{Magnet: "magnet:?xt=urn:btih:" + hashA, Category: "remix", SavePath: "/downloads/remix", Ratio: 1.5, SeedTime: 72 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	got := f.added[0]
	if got.Get("category") != "remix" || got.Get("ratioLimit") != "1.50" || got.Get("seedingTimeLimit") != "4320" || got.Get("savepath") != "/downloads/remix" {
		t.Fatalf("ajout %v", got)
	}
	f.mu.Lock()
	f.expire = true
	f.mu.Unlock()
	tr, err := q.Get(ctx, strings.ToUpper(hashA))
	if err != nil || tr == nil {
		t.Fatalf("get %v %v", tr, err)
	}
	if !tr.Done || !tr.Active || !tr.Notified || tr.Seeding != time.Hour || tr.Root != "/downloads/remix" {
		t.Fatalf("torrent %+v", tr)
	}
	if f.logins != 2 {
		t.Fatalf("connexions %d, attendu 2 (session rouverte)", f.logins)
	}
	files, err := q.Files(ctx, *tr)
	if err != nil || len(files) != 2 || files[0].Path != "/downloads/remix/Film.2024/Film.2024.mkv" {
		t.Fatalf("fichiers %+v %v", files, err)
	}
	if missing, err := q.Get(ctx, strings.Repeat("f", 40)); err != nil || missing != nil {
		t.Fatalf("absent %v %v", missing, err)
	}
	if err := q.MarkNotified(ctx, hashA); err != nil || f.tagged[0] != hashA+"="+NotifiedTag {
		t.Fatalf("étiquette %v %v", f.tagged, err)
	}
	list, err := q.List(ctx, "remix")
	if err != nil || len(list) != 1 {
		t.Fatalf("liste %v %v", list, err)
	}
}

func TestQBittorrentBadPassword(t *testing.T) {
	f := &fakeQB{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	u.User = url.UserPassword("admin", "wrong")
	u.Path = "/qb"
	q, _ := NewQBittorrent(u.String(), "", srv.Client())
	_, err := q.Version(t.Context())
	if err != ErrAuth {
		t.Fatalf("err %v, attendu ErrAuth", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatal("mot de passe dans l'erreur")
	}
}

func TestQBittorrentAPIKey(t *testing.T) {
	var mu sync.Mutex
	var logins int
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/api/v2/auth/login" {
			logins++
		}
		auth = append(auth, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer qbt_key" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/api/v2/app/version":
			_, _ = w.Write([]byte("v5.2.0"))
		case "/api/v2/app/defaultSavePath":
			_, _ = w.Write([]byte("/downloads"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	u.User = url.UserPassword("ignored", "ignored") // la clé l'emporte
	q, err := NewQBittorrent(u.String(), "qbt_key", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if v, err := q.Version(t.Context()); err != nil || v != "v5.2.0" {
		t.Fatalf("version %q %v", v, err)
	}
	if d, err := q.DefaultDir(t.Context()); err != nil || d != "/downloads" {
		t.Fatalf("dossier %q %v", d, err)
	}
	if logins != 0 {
		t.Fatalf("connexions %d, attendu 0 avec une clé", logins)
	}
	bad, _ := NewQBittorrent(srv.URL, "qbt_wrong", srv.Client())
	if _, err := bad.Version(t.Context()); err != ErrAuth {
		t.Fatalf("mauvaise clé : %v", err)
	}
}

func TestQBittorrentProgressAndETA(t *testing.T) {
	tr := qbTorrent{Hash: hashA, Progress: 0.5, State: "downloading", Size: 100, Eta: 90}.torrent()
	if tr.Done || tr.Progress != 0.5 || tr.Size != 100 || tr.ETA != 90*time.Second {
		t.Fatalf("%+v", tr)
	}
	if tr := (qbTorrent{Hash: hashA, Eta: qbNoETA}).torrent(); tr.ETA != 0 {
		t.Fatalf("ETA inconnue %v", tr.ETA)
	}
}
