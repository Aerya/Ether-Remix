package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/pse/localmedia"
	"github.com/braedonsaunders/ether/internal/pse/notifyfr"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/pse/xdcc"
)

// Source XDCC en option (PSE_XDCC, désactivée par défaut) : une ligne
// d'indexeur de plus dans le gestionnaire d'Ether, servie par
// internal/pse/xdcc à travers XDCC Grabscher. Voir docs/pse/XDCC.fr.md.

const xdccSourceID = releasedb.XDCCSourceID

type pseXdccState struct {
	mu  sync.Mutex
	key [32]byte
	svc *xdcc.Service
}

// reloadXdcc applique les réglages PSE_XDCC_* : arrêt de l'ancienne source,
// démarrage de la nouvelle et liaison à la lecture HTTP d'Ether.
func (rt *pseRuntime) reloadXdcc(a *App) {
	st := &rt.xdcc
	s := xdcc.LoadSettings()
	raw, _ := json.Marshal(s)
	key := sha256.Sum256(raw)
	st.mu.Lock()
	defer st.mu.Unlock()
	if key == st.key && (st.svc != nil) == s.Enabled {
		return
	}
	st.key = key
	if st.svc != nil {
		debrid.SetPSEDirectPrepare(nil)
		st.svc.Close()
		st.svc = nil
		a.log.Info("source XDCC Ether-Remix arrêtée")
	}
	if !s.Enabled || a.db == nil {
		return
	}
	log := a.log.With("component", "pse-xdcc")
	svc := &xdcc.Service{Settings: s, DB: a.db, Log: log,
		Client:    &xdcc.Client{Base: s.URL, Key: s.Key, HTTP: egress.Client(egress.CapDebrid, 20*time.Second)},
		Protected: a.pseLibraryFile}
	if notifyfr.Enabled() {
		core.LocalMessageLang.Store("fr")
	} else {
		core.LocalMessageLang.Store("en")
	}
	svc.Start()
	st.svc = svc
	debrid.SetPSEDirectPrepare(svc.Prepare)
	log.Info("source XDCC Ether-Remix active", "mode", s.Mode, "progressive", s.Progressive())
}

func (rt *pseRuntime) stopXdcc() {
	st := &rt.xdcc
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.svc != nil {
		debrid.SetPSEDirectPrepare(nil)
		st.svc.Close()
		st.svc = nil
	}
}

func (a *App) startPSEXdcc() {
	rt := pseRuntimeOf(a)
	rt.reloadXdcc(a)
	a.closers = append(a.closers, rt.stopXdcc)
}

// pseXdcc renvoie la source active, ou nil.
func (a *App) pseXdcc() *xdcc.Service {
	st := &pseRuntimeOf(a).xdcc
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.svc
}

// pseLibraryFile indique un fichier rangé dans une bibliothèque locale
// d'Ether-Remix (Contenus et bibliothèques) : la source XDCC ne l'efface
// jamais, même s'il vient de son cache.
func (a *App) pseLibraryFile(local string) bool {
	if a.db == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	roots, err := (&localmedia.Library{DB: a.db}).Roots(ctx)
	if err != nil {
		return true // dans le doute, rien n'est effacé
	}
	p := filepath.Clean(local)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	for _, root := range roots {
		dir := filepath.Clean(root.Path)
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			dir = r
		}
		if p == dir || strings.HasPrefix(p, strings.TrimRight(dir, string(filepath.Separator))+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// xdccIndexers ajoute la ligne de la source aux indexeurs d'Ether quand
// elle est active : cache, budget, priorités, filtres de langue et de
// qualité et classement restent ceux d'Ether.
func xdccIndexers(rows []config.Indexer) []config.Indexer {
	s := xdcc.LoadSettings()
	if !s.Enabled {
		return rows
	}
	out := append([]config.Indexer(nil), rows...)
	return append(out, config.Indexer{ID: xdccSourceID, Name: "XDCC (Grabscher)", Kind: config.IndexerKindAddon, Protocol: "torrent",
		Enabled: true, Priority: 1 << 20, DailyLimit: s.Daily, URL: "https://remix.invalid/xdcc/" + s.Identity()})
}

// xdccBackend relie la ligne à la source active (remplacée à chaque
// changement de réglages).
type xdccBackend struct{ a *App }

func (b xdccBackend) Search(ctx context.Context, req core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error) {
	svc := b.a.pseXdcc()
	if svc == nil {
		return nil, nil
	}
	return svc.Search(ctx, req, reserve)
}

func (xdccBackend) FetchNZB(context.Context, core.Release, func(context.Context, string) error) ([]byte, error) {
	return nil, errors.New("XDCC : flux HTTP local, pas de NZB")
}

func (xdccBackend) ResolveTorrent(_ context.Context, r core.Release, _ func(context.Context, string) error) (core.Release, error) {
	return r, nil
}

// pseXdccStatus est l'état affiché dans la console.
func (a *App) pseXdccStatus(ctx context.Context) map[string]any {
	svc := a.pseXdcc()
	if svc == nil {
		return map[string]any{"active": false}
	}
	return map[string]any{"active": true, "state": svc.Status(ctx)}
}

// pseXdccAPI répond aux actions XDCC de la console (/ether/api/pse/xdcc/…),
// réservées à l'administration comme le reste de la page PSE.
func (a *App) pseXdccAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "méthode non autorisée", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Query  string `json:"query"`
		Packet string `json:"packet"`
		Keep   bool   `json:"keep"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writePSEJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": "requête illisible"})
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/xdcc/")
	if action == "test" {
		a.pseXdccTest(w, r)
		return
	}
	svc := a.pseXdcc()
	if svc == nil {
		writePSEJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Source XDCC désactivée ou incomplète (adresse et clé)"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	switch action {
	case "search":
		res, err := svc.ManualSearch(ctx, body.Query)
		if err != nil {
			writePSEJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		writePSEJSON(w, http.StatusOK, map[string]any{"ok": true, "results": res, "streamable": svc.Settings.Progressive()})
	case "grab":
		if !validPacket(body.Packet) {
			writePSEJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": "paquet invalide"})
			return
		}
		if _, err := svc.Grab(ctx, body.Packet, body.Keep); err != nil {
			writePSEJSON(w, http.StatusOK, map[string]any{"ok": false, "message": pseXdccMessage(err)})
			return
		}
		writePSEJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "cancel", "keep":
		if !validPacket(body.Packet) {
			writePSEJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": "paquet invalide"})
			return
		}
		var err error
		if action == "cancel" {
			err = svc.Cancel(ctx, body.Packet)
		} else {
			err = svc.Keep(ctx, body.Packet)
		}
		if err != nil {
			writePSEJSON(w, http.StatusOK, map[string]any{"ok": false, "message": pseXdccMessage(err)})
			return
		}
		writePSEJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.NotFound(w, r)
	}
}

// pseXdccTest vérifie l'adresse et la clé enregistrées : la clé ne
// revient jamais au navigateur et n'en repart pas.
func (a *App) pseXdccTest(w http.ResponseWriter, r *http.Request) {
	s := xdcc.LoadSettings()
	if s.URL == "" || s.Key == "" {
		writePSEJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Enregistrez d'abord l'adresse et la clé d'API de Grabscher"})
		return
	}
	if err := xdcc.ValidateURL(s.URL); err != nil {
		writePSEJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	c := &xdcc.Client{Base: s.URL, Key: s.Key, HTTP: egress.Client(egress.CapDebrid, 15*time.Second)}
	msg, err := xdcc.Test(r.Context(), s, c)
	if err != nil {
		if msg != "" {
			msg += " ; "
		}
		writePSEJSON(w, http.StatusOK, map[string]any{"ok": false, "message": msg + err.Error()})
		return
	}
	writePSEJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

func pseXdccMessage(err error) string {
	lang := "en"
	if notifyfr.Enabled() {
		lang = "fr"
	}
	if m := core.LocalDownloadMessage(err, lang); m != "" {
		return m
	}
	return err.Error()
}

func validPacket(p string) bool {
	if len(p) != 36 {
		return false
	}
	for _, c := range p {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == '-') {
			return false
		}
	}
	return true
}

func writePSEJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
