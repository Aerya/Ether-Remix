package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/pse/music"
)

func dmmCheckAccount(t *testing.T, mode string, log *slog.Logger) *pseDebridAccount {
	t.Helper()
	selected := false
	mux := http.NewServeMux()
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		if mode == "auth" {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error_code":8,"error":"secret-token https://private.example/" }`)
			return
		}
		if mode == "quota" {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error_code":34,"error":"secret-token"}`)
			return
		}
		fmt.Fprint(w, `{"type":"premium"}`)
	})
	mux.HandleFunc("GET /torrents", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `[]`) })
	mux.HandleFunc("POST /torrents/addMagnet", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"id":"T1"}`) })
	mux.HandleFunc("GET /torrents/info/T1", func(w http.ResponseWriter, r *http.Request) {
		status, chosen, links := "waiting_files_selection", 0, `[]`
		if selected {
			status, chosen, links = "downloaded", 1, `["https://private.example/secret-token"]`
		}
		if selected && mode == "uncached" {
			status = "downloading"
		}
		fmt.Fprintf(w, `{"id":"T1","status":%q,"files":[{"id":1,"path":"01 Test.flac","bytes":100,"selected":%d}],"links":%s}`, status, chosen, links)
	})
	mux.HandleFunc("POST /torrents/selectFiles/T1", func(w http.ResponseWriter, r *http.Request) { selected = true; w.WriteHeader(204) })
	mux.HandleFunc("DELETE /torrents/delete/T1", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("POST /unrestrict/link", func(w http.ResponseWriter, r *http.Request) {
		if mode == "link_error" {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error_code":8,"error":"secret-token"}`)
			return
		}
		fmt.Fprint(w, `{"download":"https://private.example/secret-token"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	acc := musicTestAccount(config.DebridRealDebrid, srv.URL)
	acc.dmm, acc.log = true, log
	return acc
}

func dmmSampleFinder(t *testing.T, body string, log *slog.Logger) *music.DMMFinder {
	t.Helper()
	return &music.DMMFinder{Log: log, HTTP: &http.Client{Transport: dmmHTTP(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/api/music/library" || r.URL.Query().Get("summary") != "1" {
			t.Error("test still samples Deezer/search instead of DMM albums")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("RD key sent to DMM")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
}

func TestPSEMusicDMMCheckExplainsStagesAndRedactsLogs(t *testing.T) {
	for _, tc := range []struct {
		mode, want string
		ok         bool
	}{
		{"ready", "lien audio obtenu", true},
		{"uncached", "1 album(s) absents du cache", false},
		{"auth", "compte refusé ou premium inactif", false},
		{"quota", "limite de requêtes atteinte", false},
		{"link_error", "1 erreur(s) de lien", false},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, nil))
			acc := dmmCheckAccount(t, tc.mode, log)
			finder := dmmSampleFinder(t, fmt.Sprintf(`{"albums":[{"hash":%q,"artist":"Old artist","album":"Available album"}]}`, strings.Repeat("a", 40)), log)
			msg, err := checkPSEMusicDMM(t.Context(), finder, music.Resolvers{&music.DebridResolver{Account: acc}}, log)
			output := msg
			if err != nil {
				output = err.Error()
			}
			if (err == nil) != tc.ok || !strings.Contains(output, tc.want) {
				t.Fatal(output, err)
			}
			for _, text := range []string{buf.String(), output} {
				if strings.Contains(text, "secret-token") || strings.Contains(text, "private.example") {
					t.Fatal("credential/link leaked", text)
				}
			}
			if !strings.Contains(buf.String(), "music source test completed") || !strings.Contains(buf.String(), "source=dmm") || !strings.Contains(buf.String(), "cache_attempts=") {
				t.Fatal("missing structured test result", buf.String())
			}
		})
	}
}

func TestPSEMusicDMMCheckEmptyCatalogueAndAccountFallback(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	if _, err := checkPSEMusicDMM(context.Background(), dmmSampleFinder(t, `{"albums":[]}`, log), nil, log); err == nil || !strings.Contains(err.Error(), "étape catalogue") {
		t.Fatal(err)
	}
	bad, good := dmmCheckAccount(t, "auth", log), dmmCheckAccount(t, "ready", log)
	finder := dmmSampleFinder(t, fmt.Sprintf(`{"albums":[{"hash":%q,"artist":"Artist","album":"Album"}]}`, strings.Repeat("a", 40)), log)
	message, err := checkPSEMusicDMM(t.Context(), finder, music.Resolvers{&music.DebridResolver{Account: bad}, &music.DebridResolver{Account: good}}, log)
	if err != nil || !strings.Contains(message, "#2 actif") {
		t.Fatal(message, err)
	}
}
