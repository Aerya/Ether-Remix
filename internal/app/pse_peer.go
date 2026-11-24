package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/pse/peerclient"
)

type psePeerState struct {
	mu       sync.Mutex
	services map[string]*peerclient.Service
}

func (rt *pseRuntime) reloadPeers(a *App) {
	st := &rt.peers
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.services == nil {
		st.services = map[string]*peerclient.Service{}
	}
	for _, kind := range []string{"amule", "slskd"} {
		cfg := peerclient.Load(kind)
		old := st.services[kind]
		if old != nil && cfg.Enabled && old.Client.Settings == cfg {
			continue
		}
		debrid.SetPSEDirectPrepareFor(kind, nil)
		if old != nil {
			old.Close()
			delete(st.services, kind)
		}
		if !cfg.Enabled || a.db == nil {
			continue
		}
		svc := &peerclient.Service{DB: a.db, Client: &peerclient.Client{Settings: cfg, HTTP: egress.Client(egress.CapDebrid, 20*time.Second)}}
		st.services[kind] = svc
		debrid.SetPSEDirectPrepareFor(kind, svc.Prepare)
	}
}
func (a *App) peer(kind string) *peerclient.Service {
	st := &pseRuntimeOf(a).peers
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.services[kind]
}
func (a *App) startPSEPeers() {
	rt := pseRuntimeOf(a)
	rt.reloadPeers(a)
	a.closers = append(a.closers, func() {
		st := &rt.peers
		st.mu.Lock()
		defer st.mu.Unlock()
		for kind, s := range st.services {
			debrid.SetPSEDirectPrepareFor(kind, nil)
			s.Close()
		}
		st.services = nil
	})
}
func peerIndexers(rows []config.Indexer) []config.Indexer {
	out := append([]config.Indexer(nil), rows...)
	for _, kind := range []string{"amule", "slskd"} {
		s := peerclient.Load(kind)
		if !s.Enabled {
			continue
		}
		out = append(out, config.Indexer{ID: "remix-" + kind, Name: kind, Kind: config.IndexerKindAddon, Protocol: "torrent", Enabled: true, Priority: 1 << 20, DailyLimit: 300, URL: "https://remix.invalid/" + kind + "/" + s.Identity()})
	}
	return out
}

type peerBackend struct {
	a    *App
	kind string
}

func (b peerBackend) Search(ctx context.Context, r core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error) {
	s := b.a.peer(b.kind)
	if s == nil {
		return nil, nil
	}
	return s.SearchReleases(ctx, r, reserve)
}
func (peerBackend) FetchNZB(context.Context, core.Release, func(context.Context, string) error) ([]byte, error) {
	return nil, errors.New("source P2P : pas de NZB")
}
func (peerBackend) ResolveTorrent(_ context.Context, r core.Release, _ func(context.Context, string) error) (core.Release, error) {
	return r, nil
}
func (a *App) psePeerAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", 405)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/peer/"), "/")
	if len(parts) != 2 || (parts[0] != "amule" && parts[0] != "slskd") {
		http.NotFound(w, r)
		return
	}
	var body struct{ Query, ID string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}
	if parts[1] == "test" {
		cfg := peerclient.Load(parts[0])
		if peerclient.ValidateURL(cfg.URL) != nil || cfg.Secret == "" {
			writePSEJSON(w, 200, map[string]any{"ok": false, "message": "Adresse et identifiant requis / Address and credential required"})
			return
		}
		client := &peerclient.Client{Settings: cfg, HTTP: egress.Client(egress.CapDebrid, 15*time.Second)}
		if err := client.Test(r.Context()); err != nil {
			writePSEJSON(w, 200, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		writePSEJSON(w, 200, map[string]any{"ok": true})
		return
	}
	svc := a.peer(parts[0])
	if svc == nil {
		writePSEJSON(w, 200, map[string]any{"ok": false, "message": "Source désactivée ou configuration incomplète / Source disabled or incomplete configuration"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var data any
	var err error
	switch parts[1] {
	case "search":
		data, err = svc.Search(ctx, body.Query)
	case "download":
		_, err = svc.Ready(ctx, body.ID, true)
		if errors.Is(err, core.ErrDownloadQueued) {
			err = nil
		}
	case "transfers":
		items, e := svc.Items(ctx)
		err = e
		if err == nil {
			for _, i := range items {
				_, _ = svc.Ready(ctx, i.Hit.ID, false)
			}
			data, err = svc.Items(ctx)
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		writePSEJSON(w, 200, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writePSEJSON(w, 200, map[string]any{"ok": true, "data": data})
}
