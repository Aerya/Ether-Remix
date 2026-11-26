package admin

import "net/http"

func (h *Handler) handleProviderSpeed(w http.ResponseWriter, r *http.Request) {
	access, _ := RequestAccess(r.Context())
	if !access.Owner {
		writeError(w, http.StatusForbidden, "owner_required", "owner access required")
		return
	}
	kind, id := r.PathValue("kind"), r.PathValue("id")
	if (kind != "usenet" && kind != "debrid") || h.deps.TestProviderSpeed == nil {
		writeError(w, http.StatusNotFound, "not_found", "provider speed test unavailable")
		return
	}
	writeJSON(w, http.StatusOK, h.deps.TestProviderSpeed(r.Context(), kind, id))
}
