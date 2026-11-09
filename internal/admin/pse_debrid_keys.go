package admin

import "net/http"

// handlePseDebridKey reveals only the selected account, on explicit owner action.
// Normal configuration responses remain redacted, including during local setup.
func (h *Handler) handlePseDebridKey(w http.ResponseWriter, r *http.Request) {
	access, ok := RequestAccess(r.Context())
	if !ok || !access.Owner {
		writeError(w, http.StatusForbidden, "owner_required", "sign in with an owner account")
		return
	}
	var input struct {
		Kind string `json:"kind"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid account request")
		return
	}
	for _, provider := range h.deps.Config.Get().Debrid.Providers {
		if provider.ID != r.PathValue("id") {
			continue
		}
		if provider.Kind != input.Kind {
			writeError(w, http.StatusConflict, "account_changed", "the account type has changed")
			return
		}
		w.Header().Set("Cache-Control", "no-store, private")
		writeJSON(w, http.StatusOK, map[string]string{"key": provider.APIKey})
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "account not found")
}
