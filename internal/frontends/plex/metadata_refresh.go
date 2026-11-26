package plex

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// maxRefreshKeys bounds one PUT /library/metadata/{ids}/refresh; the host's
// refresher also coalesces repeats and caps concurrent refreshes.
const maxRefreshKeys = 20

// handleMetadataRefresh answers PUT /library/metadata/{ids}/refresh ("Refresh
// Metadata" in Plex apps, "Update Metadata" in Infuse) as PMS does: owner
// only, an empty 200 once queued, 404 when no named title is visible. Each
// title refreshes only itself (a season or episode refreshes its show);
// nothing walks a library.
func (s *Server) handleMetadataRefresh(w http.ResponseWriter, r *http.Request) {
	pr := FromContext(r.Context())
	if pr.User.Delegated {
		writeHTTPError(w, http.StatusForbidden)
		return
	}
	keys, err := parseKeys(chi.URLParam(r, "key"))
	if err != nil {
		writeHTTPError(w, http.StatusNotFound)
		return
	}
	if len(keys) > maxRefreshKeys {
		keys = keys[:maxRefreshKeys]
	}
	ctx := r.Context()
	refresher := s.getHost().TitleRefresh
	found := false
	for _, k := range keys {
		switch k.Kind() {
		case frontend.KindMovie, frontend.KindShow, frontend.KindSeason, frontend.KindEpisode:
		default:
			continue
		}
		t, err := s.metadataTitle(ctx, pr, k)
		if err != nil || t == nil || !s.visibleTitle(ctx, pr.User, t) {
			continue
		}
		found = true
		if refresher != nil {
			if err := refresher.RefreshTitleMetadata(ctx, pr.User, k); err != nil {
				s.log.Debug("metadata refresh not queued", "key", k, "err", err)
			}
		}
	}
	if !found {
		writeHTTPError(w, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}
