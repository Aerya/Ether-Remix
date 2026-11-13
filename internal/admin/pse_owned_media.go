package admin

import (
	"github.com/braedonsaunders/ether/internal/core"
	"net/http"
	"strings"
)

func (h *Handler) portalOwnedMedia(w http.ResponseWriter, r *http.Request) {
	viewer, err := h.portalViewer(r)
	if err != nil || viewer == nil {
		writeError(w, 401, "unauthorized", "sign in required")
		return
	}
	if r.Method != "GET" && !csrfOK(r) {
		writeError(w, 403, "csrf", "request rejected")
		return
	}
	if h.deps.OwnedMedia == nil {
		http.NotFound(w, r)
		return
	}
	r = r.Clone(core.WithCatalogViewer(r.Context(), &core.User{ID: viewer.ID}))
	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api/me/media")
	h.deps.OwnedMedia.ServeHTTP(w, r)
}
