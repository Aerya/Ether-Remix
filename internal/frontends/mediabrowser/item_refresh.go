package mediabrowser

import (
	"net/http"

	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// refreshItem answers POST /Items/{itemId}/Refresh ("Refresh metadata" in
// Jellyfin and Emby apps, "Update Metadata" in Infuse) as both servers do:
// administrators only, 204 once the refresh is queued, 404 for an item the
// caller cannot see. The refresh covers that one title (a season or episode
// refreshes its show) and never walks a library.
func (i *instance) refreshItem(w http.ResponseWriter, r *http.Request) {
	viewer, ok := i.requireAdmin(w, r, false)
	if !ok {
		return
	}
	key, err := ids.ParseRatingKeyGUID(r.PathValue("itemId"))
	if err != nil || key == 0 {
		http.NotFound(w, r)
		return
	}
	if _, ok := i.authorizePlaybackItem(r, viewer, key); !ok {
		http.NotFound(w, r)
		return
	}
	if i.host.TitleRefresh != nil {
		if err := i.host.TitleRefresh.RefreshTitleMetadata(i.identityContext(r), viewer, key); err != nil && i.host.Log != nil {
			i.host.Log.Debug("item refresh not queued", "key", key, "err", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
