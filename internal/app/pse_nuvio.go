package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/pse/nuvio"
	"github.com/braedonsaunders/ether/internal/remote"
)

// Liaison Nuvio par spectateur (internal/pse/nuvio). Voir docs/pse/FORK.fr.md.

// nuvioCatalog convertit les identifiants IMDb de Nuvio en clés d'Ether.
type nuvioCatalog struct{ a *App }

func (c nuvioCatalog) KeyFor(ctx context.Context, t nuvio.Target) (core.RatingKey, bool) {
	if c.a.catalog == nil {
		return 0, false
	}
	if !t.Series {
		m, err := c.a.catalog.ByExternalIDForType(ctx, "imdb", t.IMDB, core.TypeMovie)
		if err != nil || m == nil {
			return 0, false
		}
		return m.Key, true
	}
	show, err := c.a.catalog.ByExternalIDForType(ctx, "imdb", t.IMDB, core.TypeShow)
	if err != nil || show == nil {
		return 0, false
	}
	key, err := c.a.catalog.ResolveEpisode(ctx, show.Key, t.Season, t.Episode)
	if err != nil || key == 0 {
		return 0, false
	}
	return key, true
}

func (c nuvioCatalog) TargetOf(ctx context.Context, key core.RatingKey) (nuvio.Target, string, bool) {
	if c.a.catalog == nil {
		return nuvio.Target{}, "", false
	}
	t, err := c.a.catalog.Get(ctx, key)
	if err != nil || t == nil {
		return nuvio.Target{}, "", false
	}
	switch key.Kind() {
	case core.KindMovie:
		return nuvio.Target{IMDB: t.IDs.IMDB}, t.Title, t.IDs.IMDB != ""
	case core.KindEpisode:
		imdb := t.ShowIDs.IMDB
		if imdb == "" {
			if show, err := c.a.catalog.Get(ctx, core.ShowKey(key.TMDB())); err == nil && show != nil {
				imdb = show.IDs.IMDB
			}
		}
		return nuvio.Target{IMDB: imdb, Series: true, Season: key.Season(), Episode: key.Episode()}, t.Title, imdb != ""
	}
	return nuvio.Target{}, "", false
}

// pseNuvio construit une fois le service Nuvio de l'instance.
func (a *App) pseNuvio() *nuvio.Service {
	rt := pseRuntimeOf(a)
	rt.nuvioOnce.Do(func() {
		if a.db == nil || a.state == nil || a.catalog == nil {
			return
		}
		rt.nuvio = &nuvio.Service{
			Store:   &nuvio.Store{DB: a.db, Sealer: a.cfg},
			Client:  nuvio.NewClient(nil),
			Catalog: nuvioCatalog{a},
			Applier: nuvioWriter{a},
			Verify:  a.nuvioVerify,
			Log:     a.log.With("component", "pse-nuvio"),
		}
	})
	return rt.nuvio
}

// startPSENuvio démarre la lecture périodique et l'envoi des événements.
func (a *App) startPSENuvio() {
	svc := a.pseNuvio()
	if svc == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	go svc.Run(ctx)
	unsub := a.state.Subscribe(svc.Handle)
	frontend.SetPSENuvioIncoming(func(user int64, key core.RatingKey) { svc.MarkIncoming(user, key) })
	a.closers = append(a.closers, func() { unsub(); cancel(); frontend.SetPSENuvioIncoming(nil) })
}

// pseNuvioAPI sert /ether/api/pse/viewers/{id}/nuvio….
func (a *App) pseNuvioAPI(w http.ResponseWriter, r *http.Request) {
	svc := a.pseNuvio()
	if svc == nil {
		http.NotFound(w, r)
		return
	}
	exists := func(ctx context.Context, id int64) bool {
		vs, err := a.pseViewers(ctx)
		if err != nil {
			return false
		}
		for _, v := range vs {
			if v.ID == id {
				return true
			}
		}
		return false
	}
	http.StripPrefix("/viewers", svc.Handler(exists)).ServeHTTP(w, r)
}

func pseIsNuvioPath(p string) bool {
	return strings.HasPrefix(p, "/viewers/") && strings.Contains(p, "/nuvio")
}

// nuvioWriter écrit comme un lecteur Stremio : droits sur le titre, contrôle
// parental et visibilité du spectateur sont vérifiés par le service d'expérience.
type nuvioWriter struct{ a *App }

func (w nuvioWriter) Apply(ctx context.Context, user int64, key core.RatingKey, p frontend.OfflineProgress) error {
	if w.a.users == nil {
		return core.ErrNotAvailable
	}
	exp, err := w.a.newSurfaceExperience("stremio")
	if err != nil {
		return err
	}
	writer, ok := exp.(frontend.OfflineProgressWriter)
	if !ok {
		return core.ErrNotAvailable
	}
	viewer, err := w.a.users.ActiveUser(ctx, user)
	if err != nil {
		return err
	}
	return writer.SetOfflineProgress(ctx, viewer, key, p)
}

func hostOnly(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

// nuvioVerify accepte un lien …/stremio/<jeton>[/fichiers][/o/<options>]/manifest.json
// publié sur un hôte d'Ether (celui de la requête de la console ou une adresse
// publiée de Stremio) et renvoie le spectateur du jeton.
func (a *App) nuvioVerify(ctx context.Context, u *url.URL, requestHost string) (int64, error) {
	allowed := map[string]bool{}
	if requestHost != "" {
		allowed[hostOnly(requestHost)] = true
	}
	for _, raw := range a.cfg.Get().Frontends["stremio"].EffectivePublishedAddresses() {
		if p, err := remote.ParsePublishedAddress(raw); err == nil {
			allowed[strings.ToLower(p.Host)] = true
		}
	}
	if !allowed[hostOnly(u.Host)] {
		return 0, errors.New("hôte étranger")
	}
	seg := strings.Split(strings.Trim(u.Path, "/"), "/")
	at := -1
	for i, s := range seg {
		if core.IsGrantCredential(s) {
			at = i
			break
		}
	}
	if at < 1 || seg[at-1] != "stremio" {
		return 0, errors.New("lien Stremio d'Ether attendu")
	}
	rest := strings.Join(seg[at+1:], "/")
	if rest != "manifest.json" && rest != "fichiers/manifest.json" &&
		!(len(seg[at+1:]) >= 3 && (seg[at+1] == "o" || seg[at+1] == "fichiers") && seg[len(seg)-1] == "manifest.json" && len(seg[at+1:]) <= 5) {
		return 0, errors.New("lien Stremio d'Ether attendu")
	}
	grants, err := identity.NewGrants(a.db, a.users, 30*24*time.Hour, nil, a.cfg)
	if err != nil {
		return 0, err
	}
	viewer, err := grants.Resolve(ctx, "stremio", seg[at])
	if err != nil || viewer == nil {
		return 0, errors.New("jeton refusé")
	}
	return viewer.ID, nil
}
