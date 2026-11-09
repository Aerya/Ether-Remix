package app

import "github.com/braedonsaunders/ether/internal/extend"

// registerPSEDescriptors ajoute la page « Ether-Remix » (le nom public du fork)
// en tête du menu de la console, juste sous la Vue d'ensemble. Les libellés
// n'utilisent pas extend.T : « Ether-Remix » est un nom propre et les catalogues
// de traduction d'Ether restent inchangés.
func (a *App) registerPSEDescriptors(r *extend.Registry) error {
	label := extend.Text{Source: "Ether-Remix"}
	meta := extend.PageMeta{
		Route: "/accent", NavGroup: extend.NavGroupTop, InNav: true,
		Keywords: extend.Text{Source: "remix accent sources stremio torrent usenet debrid vf french français langues qualité catalogue indexer indexation animes bibliothèques statistiques scrob music musique subsonic hifi notifications"},
	}
	return r.Register(
		extend.Descriptor{Kind: extend.KindPage, ID: "pse", Owner: extend.Builtin, Label: label, Icon: "Languages", Group: meta.NavGroup, Order: 15}.WithMeta(meta),
		extend.Descriptor{Kind: extend.KindNavItem, ID: "pse", Owner: extend.Builtin, Label: label, Icon: "Languages", Group: meta.NavGroup, Order: 15}.
			WithMeta(extend.NavItemMeta{Route: meta.Route, Page: "pse"}),
	)
}
