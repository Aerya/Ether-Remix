package app

import "github.com/braedonsaunders/ether/internal/extend"

// pseNavGroup est le groupe de la barre latérale qui réunit les pages Remix,
// au-dessus de « Bibliothèque ». La console l’affiche sous le titre « Remix ».
const pseNavGroup = "remix"

// pseNavItem décrit une entrée du groupe Remix. La liste doit rester identique à
// REMIX_NAV (web/src/pse/navigation.tsx), qui porte les libellés dans les deux
// langues, l’ordre et le mode (simple ou expert) : un test de la console le vérifie.
type pseNavItem struct {
	id, label, icon, route string
}

var pseNavItems = []pseNavItem{
	{"pse-accueil", "Démarrage", "House", "/remix/accueil"},
	{"pse-sources", "Sources", "Plug", "/remix/sources"},
	{"pse-indexation", "Indexation", "Search", "/remix/indexation"},
	{"pse-langues", "Langues et qualité", "Languages", "/remix/langues"},
	{"pse-lecture", "Lecture", "MonitorPlay", "/remix/lecture"},
	{"pse-contenus", "Contenus", "Film", "/remix/contenus"},
	{"pse-visuels", "Visuels", "Palette", "/remix/visuels"},
	{"pse-suivi", "Suivi", "Eye", "/remix/suivi"},
	{"pse-apercu", "Statistiques", "Gauge", "/remix/apercu"},
	{"pse-diagnostic", "Diagnostic", "Stethoscope", "/remix/diagnostic"},
	{"pse-expert", "Expert", "SlidersHorizontal", "/remix/expert"},
}

// registerPSEDescriptors ajoute la page « Ether-Remix » (le nom public du fork)
// et son groupe « Remix » dans la barre latérale, juste au-dessus de « Bibliothèque ».
// Les libellés n'utilisent pas extend.T : « Ether-Remix » est un nom propre, les
// catalogues de traduction d'Ether restent inchangés et la console traduit les
// entrées Remix elle-même.
func (a *App) registerPSEDescriptors(r *extend.Registry) error {
	label := extend.Text{Source: "Ether-Remix"}
	meta := extend.PageMeta{
		Route: "/remix", NavGroup: pseNavGroup, InNav: true,
		Keywords: extend.Text{Source: "remix accent sources stremio torrent usenet debrid vf french français langues qualité catalogue indexer indexation animes bibliothèques statistiques scrob music musique subsonic hifi notifications diagnostic assistant expert"},
	}
	ds := []extend.Descriptor{
		extend.Descriptor{Kind: extend.KindPage, ID: "pse", Owner: extend.Builtin, Label: label, Icon: "Languages", Group: meta.NavGroup, Order: 10}.WithMeta(meta),
	}
	for i, it := range pseNavItems {
		ds = append(ds, extend.Descriptor{
			Kind: extend.KindNavItem, ID: it.id, Owner: extend.Builtin,
			Label: extend.Text{Source: it.label}, Icon: it.icon, Group: pseNavGroup, Order: 10 + i,
		}.WithMeta(extend.NavItemMeta{Route: it.route, Page: "pse"}))
	}
	return r.Register(ds...)
}
