package admin

import (
	"errors"
	"net/http"

	"github.com/braedonsaunders/ether/internal/core"
)

// handleViewerDisplayName sets or clears the name shown for a viewer.
func (h *Handler) handleViewerDisplayName(w http.ResponseWriter, r *http.Request) {
	if h.deps.RenameViewer == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "renaming viewers is not available")
		return
	}
	u, ok := h.viewerByID(w, r)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSONLenient(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	name, err := core.CleanCustomName(body.Name)
	if err != nil {
		writeValidationError(w, []FieldError{{Field: "name", Message: err.Error()}})
		return
	}
	if err := h.deps.RenameViewer(r.Context(), u.ID, name); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "no such viewer")
			return
		}
		writeError(w, http.StatusBadGateway, "users_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name})
}
