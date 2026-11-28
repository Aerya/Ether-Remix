package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/alko"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

// Source « Alkodiques (API StreamFusion) » en option (PSE_ALKO, désactivée par
// défaut) : une ligne d'indexeur de plus dans le gestionnaire d'Ether, servie
// par internal/pse/alko depuis un miroir local, sans appel réseau à la
// recherche. Voir docs/pse/ALKODIQUES.fr.md.

const alkoSourceID = releasedb.AlkoSourceID

// alkoSealPurpose sépare le chiffrement des liens du miroir de celui de la
// bibliothèque de références.
const alkoSealPurpose = "pse-alko"

type pseAlkoState struct {
	mu  sync.Mutex
	key [32]byte
	svc *alko.Service
}

// reloadAlko applique les réglages PSE_ALKO_* : arrêt de l'ancienne source,
// démarrage de la nouvelle. La clé entre dans l'empreinte de changement, jamais
// dans un journal.
func (rt *pseRuntime) reloadAlko(a *App) {
	st := &rt.alko
	s := alko.LoadSettings()
	raw, _ := json.Marshal(s)
	key := sha256.Sum256(raw)
	st.mu.Lock()
	defer st.mu.Unlock()
	if key == st.key && (st.svc != nil) == s.Enabled {
		return
	}
	st.key = key
	if st.svc != nil {
		st.svc.Close()
		st.svc = nil
		a.log.Info("source Alkodiques (API) Ether-Remix arrêtée")
	}
	if !s.Enabled || a.db == nil {
		return
	}
	log := a.log.With("component", "pse-alko")
	svc := &alko.Service{
		Settings: s, DB: a.db, Log: log,
		Seal: func(v string) (string, error) { return a.cfg.Seal(alkoSealPurpose, []byte(v)) },
		Open: func(v string) (string, error) { b, err := a.cfg.Unseal(alkoSealPurpose, v); return string(b), err },
		Publisher: &releasedb.AlkoPublisher{DB: a.db, Remember: func(ctx context.Context, key core.RatingKey, rs []core.Release) {
			a.rememberReleases(ctx, &core.Title{Key: key}, rs)
		}},
		Idle: a.pseAlkoIdle,
	}
	if err := svc.Start(); err != nil {
		log.Warn("source Alkodiques (API) non démarrée", "err", err)
		return
	}
	st.svc = svc
	log.Info("source Alkodiques (API) Ether-Remix active", "intervalle", s.Every.String(), "filtreFR", s.LangFR)
}

// pseAlkoIdle attend la fin des lectures en cours : une lecture passe avant la
// synchronisation (même logique que l'import SFR).
func (a *App) pseAlkoIdle(ctx context.Context) error {
	for a.playback != nil && len(a.playback.Sessions()) > 0 {
		t := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return ctx.Err()
}

func (rt *pseRuntime) stopAlko() {
	st := &rt.alko
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.svc != nil {
		st.svc.Close()
		st.svc = nil
	}
}

func (a *App) startPSEAlko() {
	rt := pseRuntimeOf(a)
	rt.reloadAlko(a)
	a.closers = append(a.closers, rt.stopAlko)
}

// pseAlko renvoie la source active, ou nil.
func (a *App) pseAlko() *alko.Service {
	st := &pseRuntimeOf(a).alko
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.svc
}

// pseAlkoIdentity résume les réglages de la source quand elle est active ("" sinon).
func pseAlkoIdentity() string {
	if s := alko.LoadSettings(); s.Enabled {
		return s.Identity()
	}
	return ""
}

// alkoIndexers ajoute la ligne de la source aux indexeurs d'Ether quand elle
// est active : cache, filtres de langue et de qualité et classement restent
// ceux d'Ether. Priorité 0 : interrogée la première, sans appel réseau.
func alkoIndexers(rows []config.Indexer) []config.Indexer {
	s := alko.LoadSettings()
	if !s.Enabled {
		return rows
	}
	out := append([]config.Indexer(nil), rows...)
	return append(out, config.Indexer{ID: alkoSourceID, Name: releasedb.AlkoIndexerName, Kind: config.IndexerKindAddon, Protocol: "torrent",
		Enabled: true, Priority: 0, DailyLimit: 1000000, URL: "https://remix.invalid/alko/" + s.Identity()})
}

// alkoBackend relie la ligne à la source active (remplacée à chaque
// changement de réglages). La recherche lit le miroir local.
type alkoBackend struct{ a *App }

func (b alkoBackend) Search(ctx context.Context, req core.SearchRequest, _ func(context.Context, string) error) ([]core.Release, error) {
	svc := b.a.pseAlko()
	if svc == nil {
		return nil, nil
	}
	return svc.Search(ctx, req)
}

func (alkoBackend) FetchNZB(context.Context, core.Release, func(context.Context, string) error) ([]byte, error) {
	return nil, errors.New("Alkodiques : fichier AllDebrid, pas de NZB")
}

func (alkoBackend) ResolveTorrent(_ context.Context, r core.Release, _ func(context.Context, string) error) (core.Release, error) {
	return r, nil
}

// pseAlkoStatus est l'état affiché dans la console.
func (a *App) pseAlkoStatus(ctx context.Context) map[string]any {
	svc := a.pseAlko()
	if svc == nil {
		s := alko.LoadSettings()
		out := map[string]any{"active": false}
		if releasedb.AlkoAPIEnabled() {
			out["problem"] = s.Problem()
		}
		return out
	}
	return map[string]any{"active": true, "state": svc.Status(ctx)}
}

// pseAlkoAPI répond aux actions Alkodiques de la console (/ether/api/pse/alko/…),
// réservées à l'administration comme le reste de la page PSE.
func (a *App) pseAlkoAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "méthode non autorisée", http.StatusMethodNotAllowed)
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/alko/") {
	case "test":
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		s := alko.LoadSettings()
		s.Enabled = true
		writePSEJSON(w, http.StatusOK, alko.Test(ctx, s, nil))
	case "sync":
		svc := a.pseAlko()
		if svc == nil {
			writePSEJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Source Alkodiques désactivée ou incomplète"})
			return
		}
		svc.Wake()
		writePSEJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Synchronisation lancée"})
	default:
		http.NotFound(w, r)
	}
}
