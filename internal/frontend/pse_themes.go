package frontend

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

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
	defer BumpPSEThemesGeneration()
	if t == nil {
		pseThemes.Store(nil)
		return
	}
	pseThemes.Store(&pseThemesBox{t})
}

var pseThemesGen atomic.Uint64

// BumpPSEThemesGeneration invalide les caches dérivés des bibliothèques à thème
// (service remplacé, bibliothèques rechargées, collections reconstruites).
func BumpPSEThemesGeneration() { pseThemesGen.Add(1) }

// PSEThemesGeneration identifie l'état courant des bibliothèques à thème, pour
// les clés de cache qui dépendent des droits de bibliothèque d'un spectateur.
func PSEThemesGeneration() uint64 { return pseThemesGen.Load() }

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
// moitié Séries 5200+ID suit le même droit). Une liste vide ouvre tout.
// Le marqueur 5100 (PSEThemeSectionBase, jamais un vrai thème) signifie « choix
// Remix explicite » : seuls les thèmes listés sont ouverts, aucun si 5100 est
// seul. Une liste sans aucun numéro de 5100 à 5299 (réglage antérieur aux
// bibliothèques Remix) conserve la visibilité d'origine, qui suit la section
// de base.
func ThemeAllowed(viewer *core.User, themeSection int) bool {
	if viewer == nil || viewer.IsOwner || len(viewer.AllowedLibraries) == 0 {
		return true
	}
	main := themeMainSection(themeSection)
	explicit := false
	for _, allowed := range viewer.AllowedLibraries {
		n := int(allowed)
		if n < PSEThemeSectionBase || n > PSEThemeSectionMax {
			continue
		}
		explicit = true
		if n > PSEThemeSectionBase && themeMainSection(n) == main {
			return true
		}
	}
	return !explicit
}

// ThemeExplicit : le spectateur (non propriétaire) a fait un choix Remix
// explicite, c'est-à-dire que sa liste contient un numéro de 5100 à 5299. Ses
// droits sur les thèmes ne dépendent alors plus des sections de base.
func ThemeExplicit(viewer *core.User) bool {
	if viewer == nil || viewer.IsOwner {
		return false
	}
	for _, allowed := range viewer.AllowedLibraries {
		if n := int(allowed); n >= PSEThemeSectionBase && n <= PSEThemeSectionMax {
			return true
		}
	}
	return false
}

// ThemeKeyViewer renvoie le spectateur à utiliser pour lire les titres d'un
// thème : en mode explicite, sans restriction de sections de base (qui
// viderait les rangées), tout le reste (note maximale, identité) inchangé.
func ThemeKeyViewer(viewer *core.User) *core.User {
	if !ThemeExplicit(viewer) {
		return viewer
	}
	c := *viewer
	c.AllowedLibraries = nil
	return &c
}

type themeGrantKey struct {
	viewer int64
	list   string
	gen    uint64
}

type themeGrantEntry struct {
	keys map[core.RatingKey]bool
	at   time.Time
}

const (
	themeGrantTTL        = 60 * time.Second
	themeGrantPartialTTL = 3 * time.Second
	themeGrantMax        = 256
)

var (
	themeGrantMu    sync.Mutex
	themeGrantCache = map[themeGrantKey]themeGrantEntry{}
	themeGrantNow   = time.Now
)

// themeGrantKeys renvoie l'union des titres des thèmes que ce spectateur
// (en mode explicite) peut ouvrir. Un résultat dont une rangée n'est pas
// encore construite n'est jamais mis en cache longtemps, ni quand il est vide.
func themeGrantKeys(viewer *core.User) map[core.RatingKey]bool {
	t := CurrentPSEThemes()
	if t == nil {
		return nil
	}
	key := themeGrantKey{viewer: viewer.ID, list: fmt.Sprint(NormalizeAllowedLibraries(viewer.AllowedLibraries)), gen: PSEThemesGeneration()}
	now := themeGrantNow()
	themeGrantMu.Lock()
	if e, ok := themeGrantCache[key]; ok && now.Sub(e.at) < themeGrantTTL {
		themeGrantMu.Unlock()
		return e.keys
	}
	themeGrantMu.Unlock()

	ctx := context.Background()
	reader := ThemeKeyViewer(viewer)
	union := map[core.RatingKey]bool{}
	complete := true
	for _, th := range t.Themes() {
		if !ThemeAllowed(viewer, th.Section) || (th.VisibleTo != nil && !th.VisibleTo(viewer)) {
			continue
		}
		for _, r := range th.Rows {
			keys := t.RowKeys(ctx, r.ID, reader)
			if keys == nil {
				complete = false
			}
			for _, k := range keys {
				union[k] = true
			}
		}
	}
	if !complete {
		// Collections pas encore construites : jamais de résultat vide mis en cache.
		if len(union) == 0 {
			return union
		}
		now = now.Add(themeGrantPartialTTL - themeGrantTTL)
	}
	themeGrantMu.Lock()
	if len(themeGrantCache) >= themeGrantMax {
		clear(themeGrantCache)
	}
	themeGrantCache[key] = themeGrantEntry{keys: union, at: now}
	themeGrantMu.Unlock()
	return union
}

// PSEThemeGrant ouvre à un spectateur en mode explicite les titres (et leurs
// saisons et épisodes, par ShowKey) des bibliothèques à thème qu'il peut
// ouvrir, même sans accès aux sections de base. Faux dans tous les autres cas.
func PSEThemeGrant(viewer *core.User, title *core.Title) bool {
	if title == nil || !ThemeExplicit(viewer) {
		return false
	}
	keys := themeGrantKeys(viewer)
	if len(keys) == 0 {
		return false
	}
	return keys[title.Key] || (title.ShowKey != 0 && keys[title.ShowKey])
}
