package stremio

import (
	"net/http"
)

// Lien « fichiers seuls » : même jeton, chemin /fichiers/. L'addon ne publie
// que la ressource stream (aucun catalogue ni meta) ; il a un autre id pour
// pouvoir être installé à côté du lien complet. Voir docs/pse/FORK.fr.md.

const pseFilesSegment = "fichiers"

func (i *instance) registerPSEFilesOnlyRoutes() error {
	if err := i.router.Handle(http.MethodGet, "/{credential}/"+pseFilesSegment+"/manifest.json", http.HandlerFunc(i.pseFilesManifest)); err != nil {
		return err
	}
	if err := i.router.Handle(http.MethodGet, "/{credential}/"+pseFilesSegment+"/stream/{type}/{id}", http.HandlerFunc(i.stream)); err != nil {
		return err
	}
	return i.registerPSELinkOptionRoutes() // options d'affichage et de nombre de flux, dans l'URL
}

func (i *instance) pseFilesManifest(w http.ResponseWriter, r *http.Request) {
	credential := r.PathValue("credential")
	viewer, err := i.host.Identity.Resolve(r.Context(), credential)
	if err != nil || viewer == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	i.observeClient(r, credential, viewer)
	writeJSON(w, pseFilesManifestBody())
}

func pseFilesManifestBody() map[string]any {
	return map[string]any{
		"id": "org.ether.viewer.files", "version": "1.0.0", "name": "Ether (fichiers)",
		"types": []string{"movie", "series"}, "resources": []string{"stream"},
		"idPrefixes": []string{"tt", "tmdb:"},
		"catalogs":   []any{},
	}
}
