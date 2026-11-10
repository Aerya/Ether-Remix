// Package frenchcat filters the catalogue by configured language evidence.
// The package name and defaults preserve the historical French configuration.
//
// Un titre est gardé si au moins un critère est vérifié :
//   - sa langue originale est le français (productions françaises,
//     québécoises, belges, suisses…) ;
//   - pour un film, TMDB connaît une sortie numérique (4) ou physique (5)
//     dans un des pays choisis (France par défaut), à une date passée ;
//   - l'indexeur de PSE a trouvé une release en VF (VFF, VFQ, TRUEFRENCH,
//     FRENCH) ou MULTi.
//
// Les dates de diffusion TV (type 6) ne sont pas conservées par Ether et ne
// sont donc pas utilisées. Pour les séries, seuls la langue originale et les
// releases indexées comptent : activer l'indexeur (PSE_INDEXER=on) est
// recommandé.
package frenchcat

import (
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"

	"github.com/braedonsaunders/ether/internal/core"
	// Tables pse_releases utilisées par la condition SQL.
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
)

// Settings règle le catalogue en français (variables PSE_FRENCH_*).
type Settings struct {
	// Enabled : PSE_FRENCH=on. Désactivé par défaut.
	Enabled bool
	// Nil retains the legacy French original-language condition; an empty slice disables it.
	OriginalLanguages []string
	// Countries : PSE_FRENCH_COUNTRIES, pays dont une sortie numérique ou
	// physique compte (codes ISO, défaut FR). Ajouter CA inclut les sorties
	// canadiennes, qui ne sont pas toujours doublées en français.
	Countries []string
	// Releases : PSE_FRENCH_RELEASES, langues de release indexée qui
	// comptent (défaut french,multi ; "vfq" pour le québécois seul).
	Releases []string
	// Types : PSE_FRENCH_TYPES, types filtrés (défaut movie,show).
	Types map[core.MediaType]bool
}

// Load lit la configuration depuis l'environnement.
func Load() Settings {
	s := Settings{Countries: []string{"FR"}, Releases: []string{"french", "multi"}, Types: map[core.MediaType]bool{core.TypeMovie: true, core.TypeShow: true}}
	switch strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_FRENCH"))) {
	case "on", "1", "true", "yes", "oui":
		s.Enabled = true
	}
	if v := list(pseenv.Get("PSE_FRENCH_COUNTRIES")); v != nil {
		s.Countries = nil
		for _, c := range v {
			s.Countries = append(s.Countries, strings.ToUpper(c))
		}
	}
	if v := list(pseenv.Get("PSE_CATALOG_ORIGINAL_LANGUAGES")); v != nil {
		s.OriginalLanguages = []string{}
		for _, code := range v {
			if code != "none" {
				s.OriginalLanguages = append(s.OriginalLanguages, strings.Split(code, "-")[0])
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_CATALOG_COUNTRY_EVIDENCE"))) {
	case "off", "false", "0", "no", "non":
		s.Countries = nil
	}
	if v := list(pseenv.Get("PSE_FRENCH_RELEASES")); v != nil {
		s.Releases = nil
		for _, l := range v {
			s.Releases = append(s.Releases, canonical(l))
		}
	}
	if v := list(pseenv.Get("PSE_FRENCH_TYPES")); v != nil {
		s.Types = map[core.MediaType]bool{}
		for _, t := range v {
			switch t {
			case "movie", "movies", "film", "films":
				s.Types[core.TypeMovie] = true
			case "show", "shows", "series", "série", "séries":
				s.Types[core.TypeShow] = true
			}
		}
	}
	return s
}

func list(v string) []string {
	var out []string
	for _, x := range strings.Split(v, ",") {
		if x = strings.ToLower(strings.TrimSpace(x)); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func canonical(l string) string {
	switch l {
	case "fr", "vf", "francais", "français":
		return "french"
	case "qc", "fr-ca", "quebec", "québec":
		return "vfq"
	case "fr-fr", "truefrench":
		return "vff"
	}
	return releaseparse.LanguageName(l)
}

var (
	once sync.Once
	cur  atomic.Pointer[Settings]
)

// Current renvoie les réglages actifs (lus une fois dans l'environnement).
func Current() Settings {
	once.Do(func() {
		if cur.Load() == nil {
			s := Load()
			cur.Store(&s)
		}
	})
	return *cur.Load()
}

// Override remplace les réglages actifs (tests) et renvoie la fonction qui
// les rétablit.
func Override(s Settings) (restore func()) {
	Current()
	prev := cur.Load()
	cur.Store(&s)
	return func() { cur.Store(prev) }
}

// NeedsHomeReleases indique si les dates de sortie numériques doivent être
// récupérées même hors du mode « sorties numériques » d'Ether.
func (s Settings) NeedsHomeReleases() bool {
	return s.Enabled && s.Types[core.TypeMovie] && len(s.Countries) > 0
}

// SQL renvoie la condition qui garde les titres disponibles en français.
// keyCol est la colonne de clé du titre ("t.key", ou "t.show_key" pour les
// saisons et épisodes) ; typeCol la colonne de type quand keyCol désigne
// directement le titre ("t.type"), vide sinon. nowTS borne les dates de
// sortie. Vide si rien n'est à filtrer.
func (s Settings) SQL(keyCol, typeCol string, kind core.MediaType, nowTS int64) (string, []any) {
	if !s.Enabled || len(s.Types) == 0 {
		return "", nil
	}
	if (kind == core.TypeEpisode || kind == core.TypeSeason) && !s.Types[core.TypeShow] {
		return "", nil
	}
	if (kind == core.TypeMovie || kind == core.TypeShow) && !s.Types[kind] {
		return "", nil
	}
	var ors []string
	var args []any
	originals := s.OriginalLanguages
	if originals == nil {
		originals = []string{"fr"}
	}
	if len(originals) > 0 {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(originals)), ",")
		ors = append(ors, "EXISTS (SELECT 1 FROM catalog_titles fl WHERE fl.key="+keyCol+" AND LOWER(fl.language) IN ("+ph+"))")
		for _, code := range originals {
			args = append(args, code)
		}
	}
	if s.Types[core.TypeMovie] && len(s.Countries) > 0 && kind != core.TypeEpisode && kind != core.TypeSeason {
		ph := strings.TrimSuffix(strings.Repeat("?,", len(s.Countries)), ",")
		ors = append(ors, "EXISTS (SELECT 1 FROM catalog_home_release_dates fd WHERE fd.key="+keyCol+" AND fd.country IN ("+ph+") AND fd.released_ts<=?)")
		for _, c := range s.Countries {
			args = append(args, c)
		}
		args = append(args, nowTS)
	}
	if len(s.Releases) > 0 {
		var likes []string
		for _, l := range s.Releases {
			likes = append(likes, "(','||fr.languages||',') LIKE ?")
			args = append(args, "%,"+l+",%")
			if l == "spanish" || l == "portuguese" {
				likes = append(likes, "(','||fr.languages||',') LIKE ?")
				args = append(args, "%,"+l+"-%,%")
			}
		}
		ors = append(ors, "EXISTS (SELECT 1 FROM pse_releases fr WHERE fr.title_key="+keyCol+" AND ("+strings.Join(likes, " OR ")+"))")
	}
	// Exceptions de langue (concerts, sélection en VOSTFR…).
	ors = append(ors, releasedb.LangExemptSQL(keyCol))
	clause := "(" + strings.Join(ors, " OR ") + ")"
	if typeCol != "" && kind == "" {
		// Liste mélangée : seuls les types choisis sont filtrés.
		var free []string
		for _, t := range []core.MediaType{core.TypeMovie, core.TypeShow} {
			if !s.Types[t] {
				free = append(free, "'"+string(t)+"'")
			}
		}
		if len(free) > 0 {
			clause = "(" + typeCol + " IN (" + strings.Join(free, ",") + ") OR " + clause + ")"
		}
	}
	return clause, args
}
