package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/pse/music"
)

type dmmHTTP func(*http.Request) (*http.Response, error)

func (f dmmHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPSEMusicDMMStreamsDistinctTracksAndRemovesUncachedAlbum(t *testing.T) {
	for _, uncached := range []bool{false, true} {
		t.Run(fmt.Sprint(uncached), func(t *testing.T) {
			selection := ""
			deleted, searches, added := 0, 0, 0
			hash := strings.Repeat("a", 40)
			mux := http.NewServeMux()
			mux.HandleFunc("GET /torrents", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[]`) })
			mux.HandleFunc("POST /torrents/addMagnet", func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				if !strings.Contains(r.Form.Get("magnet"), hash) {
					t.Error("DMM hash lost")
				}
				added++
				fmt.Fprint(w, `{"id":"T1"}`)
			})
			mux.HandleFunc("GET /torrents/info/T1", func(w http.ResponseWriter, r *http.Request) {
				status, selected, links := "waiting_files_selection", 0, `[]`
				if selection != "" {
					status, selected, links = "downloaded", 1, `["https://rd.example/first","https://rd.example/second"]`
				}
				if uncached && selection != "" {
					status = "downloading"
				}
				fmt.Fprintf(w, `{"id":"T1","status":%q,"files":[{"id":0,"path":"extra.m4a","bytes":10,"selected":0},{"id":1,"path":"01 First.flac","bytes":100,"selected":%d},{"id":7,"path":"02 Second.mp3","bytes":200,"selected":%d},{"id":8,"path":"cover.jpg","bytes":10,"selected":0}],"links":%s}`, status, selected, selected, links)
			})
			mux.HandleFunc("POST /torrents/selectFiles/T1", func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				selection = r.Form.Get("files")
				if selection != "1,7" {
					t.Errorf("wrong DMM cache selection: %q", selection)
				}
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
			acc.dmm = true
			finder := &music.DMMFinder{HTTP: &http.Client{Transport: dmmHTTP(func(r *http.Request) (*http.Response, error) {
				searches++
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("RD credential sent to DMM")
				}
				if r.URL.Query().Get("q") != "Artist Album" {
					t.Error("wrong album query")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"albums":[{"hash":%q,"artist":"Artist","album":"Album"}]}`, hash)))}, nil
			})}}
			res := &music.DebridResolver{Provider: "DMM · Real-Debrid", Finder: finder, Account: acc}
			for i, title := range []string{"First", "Second"} {
				st, err := res.Resolve(t.Context(), music.Track{ID: int64(i + 1), Title: title, Number: i + 1, Artist: music.Artist{Name: "Artist"}, Album: music.Album{ID: 1, Title: "Album"}})
				if uncached {
					if err == nil || deleted != 1 {
						t.Fatal("uncached album retained", err, deleted)
					}
					return
				}
				want := []string{"first", "second"}[i]
				if err != nil || st.URL != "https://audio.example/"+want || st.Provider != "DMM · Real-Debrid" {
					t.Fatal(st, err)
				}
			}
			if added != 1 || searches != 1 || deleted != 0 {
				t.Fatal("album not reused", added, searches, deleted)
			}
		})
	}
}

func TestPSEMusicDMMUsesAllRDAccountsInPriorityOrder(t *testing.T) {
	a := musicAccountsApp(t, []config.DebridProvider{
		{ID: "rd-b", Kind: config.DebridRealDebrid, APIKey: "key-b", Enabled: true, Priority: 2},
		{ID: "rd-a", Kind: config.DebridRealDebrid, APIKey: "key-a", Enabled: true, Priority: 1},
	})
	sources := a.newPSEMusicDebrid()
	if sources[0].Kind != music.DMMSource || sources[0].NoAccount {
		t.Fatal("DMM order/account missing")
	}
	rs := sources[0].Resolver.(music.Resolvers)
	if len(rs) != 2 {
		t.Fatal("missing accounts")
	}
	for i, id := range []string{"rd-a", "rd-b"} {
		r := rs[i].(*music.DebridResolver)
		account := r.Account.(*pseDebridAccount)
		if account.id != id || !account.dmm || account.kind != config.DebridRealDebrid {
			t.Fatal("incorrect account binding")
		}
		if _, ok := r.Finder.(*music.DMMFinder); !ok {
			t.Fatal("DMM depends on indexers")
		}
	}
}
