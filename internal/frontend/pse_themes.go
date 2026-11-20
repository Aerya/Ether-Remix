package frontend

import (
	"context"
	"sync/atomic"

	"github.com/braedonsaunders/ether/internal/core"
)

// Liaison entre le serveur Plex et les bibliothèques à thème de PSE
// (internal/pse/themes), qu'il n'importe pas directement — voir
// docs/pse/FORK.fr.md. L'application publie le service actif avec
// SetPSEThemes.

// Numéros de section Plex des bibliothèques à thème : 5101 à 5199, hors
// des sections d'Ether (petits entiers) et de la Musique (5053). Une
// bibliothèque « Films & séries » a en plus sa moitié Séries en 5201 à 5299.
const (
	PSEThemeSectionBase = 5100
	PSEThemeShowOffset  = 100
	PSEThemeSectionMax  = 5299
)

// PSEThemeRow est une rangée d'une bibliothèque à thème : une collection
// d'Ether.
type PSEThemeRow struct {
	ID    string // identifiant de la collection (Collection Studio)
	Title string
	Key   core.RatingKey // clé Plex de la collection (0 si inconnue)
	// LangFree : collection d'exception de langue (VOSTFR, concerts,
	// SeerrCatalog), ses titres sans VF restent visibles dans cette rangée.
	LangFree bool
}

// PSETheme est une bibliothèque à thème.
type PSETheme struct {
	VisibleTo func(*core.User) bool
	Section   int
	Title     string
	Type      core.MediaType // movie ou show
	Rows      []PSEThemeRow
	// AllLanguages : bibliothèque « toutes langues » (VOSTFR, concerts…).
	// Ses titres sans VF n'apparaissent que là, jamais dans Films et Séries.
	AllLanguages bool
}

// PSEThemes est implémenté par le service des bibliothèques à thème.
type PSEThemes interface {
	Themes() []PSETheme
	// RowKeys renvoie les titres d'une rangée visibles par user, dans
	// l'ordre de la collection ; nil tant qu'elle n'est pas construite.
	RowKeys(ctx context.Context, row string, user *core.User) []core.RatingKey
}

type pseThemesBox struct{ t PSEThemes }

var pseThemes atomic.Pointer[pseThemesBox]

// SetPSEThemes publie le service actif (nil pour le couper).
func SetPSEThemes(t PSEThemes) {
	if t == nil {
		pseThemes.Store(nil)
		return
	}
	pseThemes.Store(&pseThemesBox{t})
}

// CurrentPSEThemes renvoie le service actif, ou nil.
func CurrentPSEThemes() PSEThemes {
	if b := pseThemes.Load(); b != nil {
		return b.t
	}
	return nil
}

// PSEThemeKeys renvoie les titres de toutes les rangées d'une bibliothèque,
// sans doublon, dans l'ordre des rangées.
func PSEThemeKeys(ctx context.Context, t PSEThemes, th PSETheme, user *core.User) []core.RatingKey {
	seen := map[core.RatingKey]bool{}
	out := []core.RatingKey{}
	for _, r := range th.Rows {
		for _, k := range t.RowKeys(ctx, r.ID, user) {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}

// themeMainSection ramène la moitié Séries (5200+ID) d'une bibliothèque à
// thème à son numéro principal (5100+ID).
func themeMainSection(section int) int {
	if section > PSEThemeSectionBase+PSEThemeShowOffset && section <= PSEThemeSectionMax {
		return section - PSEThemeShowOffset
	}
	return section
}

// IsThemeSection indique si un numéro de section est dans la plage des
// bibliothèques à thème (5101 à 5299).
func IsThemeSection(section int) bool {
	return section > PSEThemeSectionBase && section <= PSEThemeSectionMax
}

// NormalizeAllowedLibraries ramène les moitiés Séries (5200+ID) à leur numéro
// principal (5100+ID) et supprime les doublons, en gardant l'ordre.
func NormalizeAllowedLibraries(ids []core.LibraryID) []core.LibraryID {
	if ids == nil {
		return nil
	}
	seen := make(map[core.LibraryID]bool, len(ids))
	out := make([]core.LibraryID, 0, len(ids))
	for _, id := range ids {
		id = core.LibraryID(themeMainSection(int(id)))
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ThemeAllowed applique les restrictions de bibliothèques d'un spectateur à
// une bibliothèque à thème, identifiée par son numéro principal 5100+ID (la
// moitié Séries 5200+ID suit le même droit). Une liste vide ouvre tout ; une
// liste sans aucun numéro de thème (réglage antérieur aux bibliothèques
// Remix) conserve la visibilité d'origine, qui suit la section de base.
func ThemeAllowed(viewer *core.User, themeSection int) bool {
	if viewer == nil || viewer.IsOwner || len(viewer.AllowedLibraries) == 0 {
		return true
	}
	main := themeMainSection(themeSection)
	hasTheme := false
	for _, allowed := range viewer.AllowedLibraries {
		if !IsThemeSection(int(allowed)) {
			continue
		}
		hasTheme = true
		if themeMainSection(int(allowed)) == main {
			return true
		}
	}
	return !hasTheme
}
