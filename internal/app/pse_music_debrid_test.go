package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/pse/music"
)

func musicTestAccount(kind, base string) *pseDebridAccount {
	return &pseDebridAccount{kind: kind, cfg: func() config.Debrid {
		return config.Debrid{Providers: []config.DebridProvider{{Kind: kind, APIKey: "test-key", Enabled: true}}}
	}, opts: debrid.ProviderOptions{BaseURL: base, PollInterval: time.Millisecond, ReadyPolls: 1}}
}

type musicAlbumFinder struct{}

func (musicAlbumFinder) FindAlbum(context.Context, string) ([]music.TorrentHit, error) {
	return []music.TorrentHit{{Title: "Artist Album FLAC", Magnet: "magnet:?xt=urn:btih:" + strings.Repeat("a", 40)}}, nil
}
func (musicAlbumFinder) Magnet(_ context.Context, h music.TorrentHit) (music.TorrentHit, error) {
	return h, nil
}

func TestPSEMusicTorBoxStreamsDistinctAlbumTracks(t *testing.T) {
	created := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /torrents/createtorrent", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		if r.FormValue("add_only_if_cached") != "true" {
			t.Error("cached-only guard missing")
		}
		created++
		fmt.Fprint(w, `{"success":true,"data":{"torrent_id":77}}`)
	})
	mux.HandleFunc("GET /torrents/mylist", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":{"id":77,"download_present":true,"files":[{"id":0,"name":"01 First.flac","size":100},{"id":9,"name":"02 Second.flac","size":200},{"id":10,"name":"cover.jpg","size":10}]}}`)
	})
	mux.HandleFunc("GET /torrents/requestdl", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("torrent_id") != "77" {
			t.Error("torrent identity lost")
		}
		fmt.Fprintf(w, `{"success":true,"data":"https://audio.example/%s"}`, r.URL.Query().Get("file_id"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	res := &music.DebridResolver{Provider: "TorBox", Finder: musicAlbumFinder{}, Account: musicTestAccount(config.DebridTorBox, srv.URL)}
	for i, title := range []string{"First", "Second"} {
		st, err := res.Resolve(t.Context(), music.Track{ID: int64(i + 1), Number: i + 1, Title: title, Artist: music.Artist{Name: "Artist"}, Album: music.Album{ID: 1, Title: "Album"}})
		want := []string{"0", "9"}[i]
		if err != nil || st.URL != "https://audio.example/"+want || st.Provider != "TorBox" || st.ExpiresAt == 0 {
			t.Fatalf("track %d: %+v %v", i, st, err)
		}
	}
	if created != 1 {
		t.Fatalf("album added %d times", created)
	}
}

func TestPSEMusicRealDebridAlbumSelection(t *testing.T) {
	for _, mode := range []string{"new", "existing-partial", "uncached"} {
		t.Run(mode, func(t *testing.T) {
			selection := ""
			deleted := 0
			mux := http.NewServeMux()
			mux.HandleFunc("GET /torrents", func(w http.ResponseWriter, r *http.Request) {
				if mode == "existing-partial" {
					fmt.Fprintf(w, `[{"id":"T1","hash":"%s","status":"downloaded"}]`, strings.Repeat("a", 40))
				} else {
					fmt.Fprint(w, `[]`)
				}
			})
			mux.HandleFunc("POST /torrents/addMagnet", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"id":"T1"}`) })
			mux.HandleFunc("GET /torrents/info/T1", func(w http.ResponseWriter, r *http.Request) {
				status := "waiting_files_selection"
				first, second := 0, 0
				links := `[]`
				if mode == "existing-partial" {
					status = "downloaded"
					second = 1
					links = `["https://rd.example/second"]`
				} else if selection != "" {
					status = "downloaded"
					first = 1
					second = 1
					links = `["https://rd.example/first","https://rd.example/second"]`
					if mode == "uncached" {
						status = "downloading"
					}
				}
				fmt.Fprintf(w, `{"id":"T1","status":%q,"files":[{"id":1,"path":"01 First.flac","bytes":100,"selected":%d},{"id":7,"path":"02 Second.flac","bytes":200,"selected":%d},{"id":8,"path":"cover.jpg","bytes":20,"selected":0}],"links":%s}`, status, first, second, links)
			})
			mux.HandleFunc("POST /torrents/selectFiles/T1", func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				selection = r.Form.Get("files")
				w.WriteHeader(204)
			})
			mux.HandleFunc("DELETE /torrents/delete/T1", func(w http.ResponseWriter, r *http.Request) { deleted++; w.WriteHeader(204) })
			mux.HandleFunc("POST /unrestrict/link", func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				fmt.Fprintf(w, `{"download":%q}`, strings.Replace(r.Form.Get("link"), "rd.example", "audio.example", 1))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()
			acc := musicTestAccount(config.DebridRealDebrid, srv.URL)
			files, err := acc.OpenCached(t.Context(), music.TorrentHit{InfoHash: strings.Repeat("a", 40), Magnet: "magnet:test"})
			if mode == "uncached" {
				if err == nil || deleted != 1 {
					t.Fatalf("uncached album not removed: %v %d", err, deleted)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "new" && (selection != "1,7" || len(files) != 2) {
				t.Fatalf("selection=%s files=%v", selection, files)
			}
			if mode == "existing-partial" && (selection != "" || len(files) != 1 || files[0].FileID != "7") {
				t.Fatalf("existing selection changed: %s %v", selection, files)
			}
			for _, f := range files {
				st, err := acc.Unlock(t.Context(), f)
				if err != nil || st.URL != strings.Replace(f.Link, "rd.example", "audio.example", 1) {
					t.Fatalf("wrong track: %+v %v", st, err)
				}
			}
			if deleted != 0 {
				t.Fatal("ready torrent removed")
			}
		})
	}
}

func TestPSEMusicAllDebridAlbumRegression(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v4/magnet/upload", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"success","data":{"magnets":[{"id":555,"ready":true}]}}`)
	})
	mux.HandleFunc("POST /v4/magnet/files", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"success","data":{"magnets":[{"id":"555","files":[{"n":"01 First.flac","s":100,"l":"https://ad.example/first"},{"n":"02 Second.flac","s":200,"l":"https://ad.example/second"}]}]}}`)
	})
	mux.HandleFunc("POST /v4/link/unlock", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		fmt.Fprintf(w, `{"status":"success","data":{"link":%q}}`, strings.Replace(r.Form.Get("link"), "ad.example", "audio.example", 1))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	acc := musicTestAccount(config.DebridAllDebrid, srv.URL)
	files, err := acc.OpenCached(t.Context(), music.TorrentHit{Magnet: "magnet:test"})
	if err != nil || len(files) != 2 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	for _, file := range files {
		st, err := acc.Unlock(t.Context(), file)
		if err != nil || st.URL != strings.Replace(file.Link, "ad.example", "audio.example", 1) {
			t.Fatalf("track=%+v err=%v", st, err)
		}
	}
	// An account change must never send a previous account's file identifiers to the new one.
	acc.cfg = func() config.Debrid {
		return config.Debrid{Providers: []config.DebridProvider{{Kind: config.DebridAllDebrid, APIKey: "changed-key", Enabled: true}}}
	}
	if _, err := acc.Unlock(t.Context(), files[0]); err == nil {
		t.Fatal("old album reused on a different account")
	}
}

func TestPSEMusicAccountChangesRefreshSources(t *testing.T) {
	t.Setenv("PSE_MUSIC_TORBOX", "on")
	a := setupTestApp(t, true)
	rt := pseRuntimeOf(a)
	original := rt.music.Load()
	if original == nil {
		t.Fatal("music not mounted")
	}
	if err := a.cfg.Update(func(c *config.Config) error {
		c.Debrid.Providers = []config.DebridProvider{{Kind: config.DebridTorBox, APIKey: "test-key", Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	refreshed := rt.music.Load()
	if refreshed == nil || refreshed == original {
		t.Fatal("account change did not renew the source and link cache")
	}
	if err := a.cfg.Update(func(c *config.Config) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if rt.music.Load() != refreshed {
		t.Fatal("unchanged accounts unnecessarily reset playback sources")
	}
}
