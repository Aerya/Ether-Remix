// Package scrobview expose à Scrob (et aux clients Jellyfin/Emby) deux
// bibliothèques « Vus et notés » : les films, séries et épisodes que le
// spectateur a vus ou notés, et rien d'autre. Scrob importe alors son
// historique et ses notes sans parcourir tout le catalogue TMDB de PSE.
//
// Dans Scrob, ne sélectionner que ces deux bibliothèques pour la connexion
// PSE (sinon Scrob importe tout le catalogue comme collection).
package scrobview

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"

	"github.com/braedonsaunders/ether/internal/core"
)

// TypeShow est exposé pour que les frontends n'importent pas internal/core.
const TypeShow = core.TypeShow

// Numéros des bibliothèques PSE. Les bibliothèques d'Ether ont de petits
// numéros et les étagères personnelles le bit 62 ; PSE prend le bit 61.
const (
	viewBase   int64 = 1 << 61
	MoviesView       = viewBase + 1
	ShowsView        = viewBase + 2
)

// Enabled : PSE_SCROB_VIEWS=on. Désactivé par défaut : les deux
// bibliothèques apparaissent aussi dans les autres applications Jellyfin.
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_SCROB_VIEWS"))) {
	case "on", "1", "true", "yes", "oui":
		return true
	}
	return forced
}

var forced bool

// ForceEnable active la fonction sans variable d'environnement (tests).
func ForceEnable(on bool) (restore func()) {
	prev := forced
	forced = on
	return func() { forced = prev }
}

// View décrit une bibliothèque PSE.
type View struct {
	Number int64
	Name   string
	Type   core.MediaType // movie ou show
}

// Views renvoie les bibliothèques PSE, vides si la fonction est désactivée.
func Views() []View {
	if !Enabled() {
		return nil
	}
	return []View{
		{Number: MoviesView, Name: "Vus et notés — Films", Type: core.TypeMovie},
		{Number: ShowsView, Name: "Vus et notés — Séries", Type: core.TypeShow},
	}
}

// Lookup renvoie la bibliothèque PSE de ce numéro.
func Lookup(number int64) (View, bool) {
	for _, v := range Views() {
		if v.Number == number {
			return v, true
		}
	}
	return View{}, false
}

// Provider lit l'historique du spectateur. Il est fourni par l'application.
type Provider struct {
	// Watched renvoie les clés vues (au moins une lecture complète) ou
	// notées par ce spectateur.
	Watched func(ctx context.Context, viewerID int64) ([]core.RatingKey, error)
	// Titles renvoie les titres connus du catalogue local (sans appel
	// TMDB), dans un ordre quelconque.
	Titles func(ctx context.Context, keys []core.RatingKey) ([]core.Title, error)
	// Resolve renvoie un titre même absent du catalogue local (une requête
	// TMDB au plus) : sert à retrouver un titre par son identifiant TMDB.
	Resolve func(ctx context.Context, key core.RatingKey) (*core.Title, error)
}

var (
	mu       sync.RWMutex
	provider Provider
)

// SetProvider branche l'historique (appelé au démarrage).
func SetProvider(p Provider) {
	mu.Lock()
	provider = p
	mu.Unlock()
}

func current() Provider {
	mu.RLock()
	defer mu.RUnlock()
	return provider
}

// Items renvoie le contenu d'une bibliothèque PSE pour un type demandé :
// films vus/notés ; séries ayant un épisode vu/noté ou elles-mêmes notées ;
// épisodes vus/notés. typ vide : le type principal de la bibliothèque.
func Items(ctx context.Context, viewerID int64, view View, types []core.MediaType) ([]core.Title, error) {
	p := current()
	if p.Watched == nil || p.Titles == nil {
		return nil, nil
	}
	keys, err := p.Watched(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	want := map[core.MediaType]bool{}
	for _, t := range types {
		want[t] = true
	}
	if len(want) == 0 {
		want[view.Type] = true
	}
	var movies, shows, episodes []core.RatingKey
	for _, k := range keys {
		switch k.Kind() {
		case core.KindMovie:
			movies = append(movies, k)
		case core.KindShow:
			shows = append(shows, k)
		case core.KindEpisode:
			episodes = append(episodes, k)
		}
	}
	var out []core.Title
	if view.Type == core.TypeMovie {
		if !want[core.TypeMovie] {
			return nil, nil
		}
		return p.Titles(ctx, movies)
	}
	var eps []core.Title
	if len(episodes) > 0 && (want[core.TypeEpisode] || want[core.TypeShow]) {
		if eps, err = p.Titles(ctx, episodes); err != nil {
			return nil, err
		}
	}
	if want[core.TypeShow] {
		seen := map[core.RatingKey]bool{}
		for _, k := range shows {
			seen[k] = true
		}
		for _, e := range eps {
			if e.ShowKey != 0 && !seen[e.ShowKey] {
				seen[e.ShowKey] = true
				shows = append(shows, e.ShowKey)
			}
		}
		s, err := p.Titles(ctx, shows)
		if err != nil {
			return nil, err
		}
		out = append(out, s...)
	}
	if want[core.TypeEpisode] {
		out = append(out, eps...)
	}
	return out, nil
}

// ByProviderID résout un identifiant « Tmdb.123 », « Imdb.tt123 » ou
// « Tvdb.1 » (paramètre AnyProviderIdEquals de Jellyfin) en titres, pour
// les types demandés (films et séries). imdb résout un identifiant IMDb
// et peut être nil.
func ByProviderID(ctx context.Context, value string, types []core.MediaType, imdb func(context.Context, string, core.MediaType) (core.RatingKey, error)) ([]core.Title, error) {
	p := current()
	if p.Resolve == nil {
		return nil, nil
	}
	if len(types) == 0 {
		types = []core.MediaType{core.TypeMovie, core.TypeShow}
	}
	var out []core.Title
	seen := map[core.RatingKey]bool{}
	for _, part := range strings.Split(value, ",") {
		src, id, ok := strings.Cut(strings.TrimSpace(part), ".")
		if !ok || id == "" {
			continue
		}
		for _, typ := range types {
			if typ != core.TypeMovie && typ != core.TypeShow {
				continue
			}
			var key core.RatingKey
			switch strings.ToLower(src) {
			case "tmdb":
				n, err := strconv.ParseInt(id, 10, 64)
				if err != nil || n <= 0 {
					continue
				}
				if typ == core.TypeMovie {
					key = core.MovieKey(n)
				} else {
					key = core.ShowKey(n)
				}
			case "imdb":
				if imdb == nil {
					continue
				}
				k, err := imdb(ctx, id, typ)
				if err != nil || k == 0 {
					continue
				}
				key = k
			default:
				continue
			}
			if seen[key] {
				continue
			}
			t, err := p.Resolve(ctx, key)
			if err != nil || t == nil || t.Type != typ {
				continue
			}
			seen[key] = true
			out = append(out, *t)
		}
	}
	return out, nil
}
