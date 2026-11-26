package mediabrowser

// Ajout Remix : versions réelles dans « Lire la version » en Jellyfin
// (voir docs/pse/FORK.fr.md).

import (
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// pseRefillDetail lance la recherche de releases d'un film ou d'un épisode
// quand la fiche a des versions mais que le cache de recherche est froid :
// sans elle, les emplacements restent provisoires (« Same-quality version 1 »)
// car detailSources ne relance la recherche que sans version. Si une recherche
// démarre, elle est attendue dans le même budget que detailSources, puis la
// fiche est reconstruite avec les releases trouvées.
func (i *instance) pseRefillDetail(r *http.Request, d frontend.ItemDetail, rebuild func() (frontend.ItemDetail, error)) frontend.ItemDetail {
	if i.host.Editions == nil || r == nil || len(d.Versions) == 0 || d.Title.Key.IsPluginKey() || (d.Title.Type != "movie" && d.Title.Type != "episode") {
		return d
	}
	if !d.Title.Released.IsZero() && d.Title.Released.After(time.Now()) {
		return d
	}
	t := d.Title
	done := i.host.Editions.RefillSearchCache(&t)
	if done == nil {
		return d // cache déjà chaud, ou recherche impossible pour l'instant
	}
	budget := detailVariantBudget
	if frontend.JellyfinAndroidTVClient(device(r).Client) {
		budget = androidTVDetailVariantBudget
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		return d
	case <-r.Context().Done():
		return d
	}
	if fresh, err := rebuild(); err == nil {
		return fresh
	}
	return d
}
