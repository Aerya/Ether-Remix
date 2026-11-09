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

func musicAccountsApp(t *testing.T, providers []config.DebridProvider) *App {
	t.Helper()
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) error { c.Debrid.Providers = providers; return nil }); err != nil {
		t.Fatal(err)
	}
	return &App{cfg: cfg}
}

// Plusieurs comptes d'un même service : un résolveur par compte, dans
// l'ordre des priorités ; les services suivent leur meilleur compte.
func TestPSEMusicDebridAccountsOrder(t *testing.T) {
	a := musicAccountsApp(t, []config.DebridProvider{
		{ID: "ad", Kind: config.DebridAllDebrid, APIKey: "k1", Priority: 5, Enabled: true},
		{ID: "tb-b", Kind: config.DebridTorBox, APIKey: "k2", Priority: 3, Enabled: true},
		{ID: "tb-a", Kind: config.DebridTorBox, APIKey: "k3", Priority: 1, Enabled: true},
		{ID: "tb-off", Kind: config.DebridTorBox, APIKey: "k4", Priority: 0, Enabled: false},
		{ID: "rd-nokey", Kind: config.DebridRealDebrid, Priority: 0, Enabled: true},
	})
	srcs := a.newPSEMusicDebrid()
	if srcs[0].Kind != config.DebridTorBox || srcs[1].Kind != config.DebridAllDebrid {
		t.Fatalf("ordre des services : %s, %s", srcs[0].Kind, srcs[1].Kind)
	}
	rs, ok := srcs[0].Resolver.(music.Resolvers)
	if !ok || len(rs) != 2 {
		t.Fatalf("TorBox : deux comptes actifs attendus, %T", srcs[0].Resolver)
	}
	for i, want := range []string{"tb-a", "tb-b"} {
		r := rs[i].(*music.DebridResolver)
		if got := r.Account.(*pseDebridAccount).id; got != want || r.Provider != fmt.Sprintf("TorBox #%d", i+1) {
			t.Fatalf("compte %d : %s (%s), %s attendu", i, got, r.Provider, want)
		}
	}
	if _, ok := srcs[1].Resolver.(*music.DebridResolver); !ok {
		t.Fatalf("un seul compte AllDebrid : résolveur simple attendu, %T", srcs[1].Resolver)
	}
}

// Un compte lié par identifiant ne bascule jamais en silence sur un autre
// compte : retiré, il refuse ; sa clé change, ses anciens liens sont refusés.
func TestPSEMusicDebridAccountBinding(t *testing.T) {
	providers := []config.DebridProvider{
		{ID: "one", Kind: config.DebridTorBox, APIKey: "k1", Priority: 1, Enabled: true},
		{ID: "two", Kind: config.DebridTorBox, APIKey: "k2", Priority: 2, Enabled: true},
	}
	cur := providers
	acc := &pseDebridAccount{kind: config.DebridTorBox, id: "two", cfg: func() config.Debrid { return config.Debrid{Providers: cur} }}
	p1, err := acc.provider()
	if err != nil {
		t.Fatal(err)
	}
	old := music.DebridFile{Path: "01 a.flac", TorrentID: "1", FileID: "0", AccountID: fmt.Sprintf("%p", p1)}
	cur = []config.DebridProvider{providers[0], {ID: "two", Kind: config.DebridTorBox, APIKey: "k2-new", Priority: 2, Enabled: true}}
	p2, err := acc.provider()
	if err != nil || p2 == p1 {
		t.Fatal("clé changée : nouveau client attendu")
	}
	if _, err := acc.Unlock(t.Context(), old); err == nil {
		t.Fatal("lien d'avant le changement de clé accepté")
	}
	cur = providers[:1]
	if _, err := acc.provider(); err == nil {
		t.Fatal("compte retiré : ne doit pas basculer sur un autre compte")
	}
}

// Le premier compte n'a pas l'album en cache : le second prend le relais,
// et ses identifiants restent les siens.
func TestPSEMusicDebridFallsBackToSecondAccount(t *testing.T) {
	uncached := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/torrents/createtorrent":
			fmt.Fprint(w, `{"success":false,"error":"DOWNLOAD_NOT_CACHED","detail":"not cached"}`)
		case "/torrents/mylist":
			fmt.Fprint(w, `{"success":true,"data":[]}`)
		default:
			fmt.Fprint(w, `{"success":true,"data":null}`)
		}
	}))
	defer uncached.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /torrents/createtorrent", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":{"torrent_id":77}}`)
	})
	mux.HandleFunc("GET /torrents/mylist", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":{"id":77,"download_present":true,"files":[{"id":3,"name":"01 First.flac","size":100}]}}`)
	})
	mux.HandleFunc("GET /torrents/requestdl", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"success":true,"data":"https://audio.example/%s-%s"}`, r.URL.Query().Get("torrent_id"), r.URL.Query().Get("file_id"))
	})
	cached := httptest.NewServer(mux)
	defer cached.Close()

	providers := config.Debrid{Providers: []config.DebridProvider{
		{ID: "a", Kind: config.DebridTorBox, APIKey: "k1", Priority: 1, Enabled: true},
		{ID: "b", Kind: config.DebridTorBox, APIKey: "k2", Priority: 2, Enabled: true},
	}}
	cfg := func() config.Debrid { return providers }
	opts := func(base string) debrid.ProviderOptions {
		return debrid.ProviderOptions{BaseURL: base, PollInterval: time.Millisecond, ReadyPolls: 1}
	}
	rs := music.Resolvers{
		&music.DebridResolver{Provider: "TorBox #1", Finder: musicAlbumFinder{}, Account: &pseDebridAccount{kind: config.DebridTorBox, id: "a", cfg: cfg, opts: opts(uncached.URL)}},
		&music.DebridResolver{Provider: "TorBox #2", Finder: musicAlbumFinder{}, Account: &pseDebridAccount{kind: config.DebridTorBox, id: "b", cfg: cfg, opts: opts(cached.URL)}},
	}
	st, err := rs.Resolve(context.Background(), music.Track{ID: 1, Number: 1, Title: "First", Artist: music.Artist{Name: "Artist"}, Album: music.Album{ID: 1, Title: "Album"}})
	if err != nil || st.URL != "https://audio.example/77-3" || st.Provider != "TorBox #2" {
		t.Fatalf("relais du second compte : %+v %v", st, err)
	}
	if strings.Contains(st.URL, "k2") {
		t.Fatal("clé exposée dans le lien")
	}
}
