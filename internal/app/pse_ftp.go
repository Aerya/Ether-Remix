package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/pse/ftp"
	"github.com/braedonsaunders/ether/internal/pse/notifyfr"
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

// Source FTP / SFTP en option (PSE_FTP, désactivée par défaut) : une ligne
// d'indexeur de plus dans le gestionnaire d'Ether, servie par
// internal/pse/ftp depuis l'index local des fichiers du serveur. Voir
// docs/pse/FTP.fr.md.

const ftpSourceID = releasedb.FTPSourceID

type pseFTPState struct {
	mu  sync.Mutex
	key [32]byte
	svc *ftp.Service
}

// reloadFTP applique les réglages PSE_FTP_* : arrêt de l'ancienne source,
// démarrage de la nouvelle et liaison à la lecture HTTP d'Ether. Les
// secrets entrent dans l'empreinte de changement, jamais dans un journal.
func (rt *pseRuntime) reloadFTP(a *App) {
	st := &rt.ftp
	s := ftp.LoadSettings()
	raw, _ := json.Marshal(s)
	key := sha256.Sum256(raw)
	st.mu.Lock()
	defer st.mu.Unlock()
	if key == st.key && (st.svc != nil) == s.Enabled {
		return
	}
	st.key = key
	if st.svc != nil {
		debrid.SetPSEDirectPrepareFor("ftp", nil)
		st.svc.Close()
		st.svc = nil
		a.log.Info("source FTP Ether-Remix arrêtée")
	}
	releasedb.FTPCopyFirst.Store(s.Enabled && s.Mode == ftp.ModeDownload)
	if !s.Enabled || a.db == nil {
		return
	}
	log := a.log.With("component", "pse-ftp")
	svc := &ftp.Service{Settings: s, DB: a.db, Log: log, Protected: a.pseLibraryFile}
	if notifyfr.Enabled() {
		core.LocalMessageLang.Store("fr")
	} else {
		core.LocalMessageLang.Store("en")
	}
	if err := svc.Start(); err != nil {
		log.Warn("source FTP non démarrée", "err", err)
		return
	}
	st.svc = svc
	debrid.SetPSEDirectPrepareFor("ftp", svc.Prepare)
	log.Info("source FTP Ether-Remix active", "protocole", s.Protocol, "mode", s.Mode, "dossiers", len(s.Roots))
}

func (rt *pseRuntime) stopFTP() {
	st := &rt.ftp
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.svc != nil {
		debrid.SetPSEDirectPrepareFor("ftp", nil)
		st.svc.Close()
		st.svc = nil
	}
}

func (a *App) startPSEFTP() {
	rt := pseRuntimeOf(a)
	rt.reloadFTP(a)
	a.closers = append(a.closers, rt.stopFTP)
}

// pseFTP renvoie la source active, ou nil.
func (a *App) pseFTP() *ftp.Service {
	st := &pseRuntimeOf(a).ftp
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.svc
}

// ftpIndexers ajoute la ligne de la source aux indexeurs d'Ether quand
// elle est active : cache, priorités, filtres de langue et de qualité et
// classement restent ceux d'Ether.
func ftpIndexers(rows []config.Indexer) []config.Indexer {
	s := ftp.LoadSettings()
	if !s.Enabled {
		return rows
	}
	out := append([]config.Indexer(nil), rows...)
	return append(out, config.Indexer{ID: ftpSourceID, Name: "FTP / SFTP", Kind: config.IndexerKindAddon, Protocol: "torrent",
		Enabled: true, Priority: 1 << 20, DailyLimit: s.Daily, URL: "https://remix.invalid/ftp/" + s.Identity()})
}

// ftpBackend relie la ligne à la source active (remplacée à chaque
// changement de réglages).
type ftpBackend struct{ a *App }

func (b ftpBackend) Search(ctx context.Context, req core.SearchRequest, reserve func(context.Context, string) error) ([]core.Release, error) {
	svc := b.a.pseFTP()
	if svc == nil {
		return nil, nil
	}
	return svc.Search(ctx, req, reserve)
}

func (ftpBackend) FetchNZB(context.Context, core.Release, func(context.Context, string) error) ([]byte, error) {
	return nil, errors.New("FTP : flux HTTP local, pas de NZB")
}

func (ftpBackend) ResolveTorrent(_ context.Context, r core.Release, _ func(context.Context, string) error) (core.Release, error) {
	return r, nil
}

// pseFTPStatus est l'état affiché dans la console.
func (a *App) pseFTPStatus(ctx context.Context) map[string]any {
	svc := a.pseFTP()
	if svc == nil {
		s := ftp.LoadSettings()
		out := map[string]any{"active": false}
		if on := strings.TrimSpace(strings.ToLower(pseenv.Get("PSE_FTP"))); on == "on" || on == "true" || on == "1" {
			out["problem"] = s.Problem()
		}
		return out
	}
	return map[string]any{"active": true, "state": svc.Status(ctx)}
}

// pseFTPAPI répond aux actions FTP de la console (/ether/api/pse/ftp/…),
// réservées à l'administration comme le reste de la page PSE.
func (a *App) pseFTPAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "méthode non autorisée", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Query string `json:"query"`
		ID    string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writePSEJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": "requête illisible"})
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/ftp/")
	if action == "test" {
		res := ftp.Test(r.Context(), ftp.LoadSettings())
		writePSEJSON(w, http.StatusOK, res)
		return
	}
	svc := a.pseFTP()
	if svc == nil {
		msg := "Source FTP / SFTP désactivée"
		if p := ftp.LoadSettings().Problem(); p != "" {
			msg += " ou incomplète : " + p
		}
		writePSEJSON(w, http.StatusOK, map[string]any{"ok": false, "message": msg})
		return
	}
	switch action {
	case "scan":
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
			defer cancel()
			_, _ = svc.Rescan(ctx)
		}()
		writePSEJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Parcours lancé"})
	case "search":
		writePSEJSON(w, http.StatusOK, map[string]any{"ok": true, "results": svc.ManualSearch(body.Query), "writes": svc.Settings.Writes()})
	case "grab", "cancel", "keep":
		if !validFTPID(body.ID) {
			writePSEJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": "fichier invalide"})
			return
		}
		var err error
		switch action {
		case "grab":
			_, err = svc.Grab(r.Context(), body.ID)
		case "cancel":
			err = svc.Cancel(body.ID)
		default:
			err = svc.Keep(body.ID)
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

func validFTPID(id string) bool {
	_, ok := ftp.IDOf(core.Release{GUID: ftp.GUIDPrefix + id})
	return ok
}
