package admin

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

const maxVelaSkinUploadBytes = 8 << 20 // 8 MB .vskin archive limit

// VelaSkin describes a built-in or owner-installed Vela skin for the console.
type VelaSkin struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Kind        string   `json:"kind"`
	Builtin     bool     `json:"builtin"`
	Preview     string   `json:"preview,omitempty"`
	Swatches    []string `json:"swatches"`
	InstalledAt int64    `json:"installedAt,omitempty"`
}

// VelaSkinValidationError carries a user-facing validation or policy error.
type VelaSkinValidationError struct {
	Code    string
	Message string
	Status  int
}

func (e *VelaSkinValidationError) Error() string { return e.Message }

// VelaSkinAdmin backs /api/vela/skins*.
type VelaSkinAdmin interface {
	ListSkins(ctx context.Context) ([]VelaSkin, error)
	InstallSkinArchive(ctx context.Context, pkg []byte) (VelaSkin, error)
	InstallSkinURL(ctx context.Context, rawURL string, lan bool) (VelaSkin, error)
	RemoveSkin(ctx context.Context, id string) error
}

func (h *Handler) velaSkinRoutes(api func(string, http.HandlerFunc)) {
	api("GET /api/vela/skins", h.handleVelaSkinList)
	api("POST /api/vela/skins", h.handleVelaSkinInstall)
	api("DELETE /api/vela/skins/{id}", h.handleVelaSkinRemove)
}

func (h *Handler) requireVelaSkinOwner(w http.ResponseWriter, r *http.Request) (VelaSkinAdmin, bool) {
	if !h.isOwner(r, tokenFromRequest(r)) {
		writeError(w, http.StatusForbidden, "forbidden", "owner sign-in required")
		return nil, false
	}
	if h.deps.VelaSkins == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Vela skins unavailable")
		return nil, false
	}
	return h.deps.VelaSkins, true
}

func (h *Handler) writeVelaSkinError(w http.ResponseWriter, err error) {
	var ve *VelaSkinValidationError
	if errors.As(err, &ve) {
		status := ve.Status
		if status == 0 {
			status = http.StatusBadRequest
		}
		code := ve.Code
		if code == "" {
			code = "invalid_skin"
		}
		writeError(w, status, code, ve.Message)
		return
	}
	h.log.Warn("vela skin request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "Vela skin operation failed")
}

func (h *Handler) handleVelaSkinList(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.requireVelaSkinOwner(w, r)
	if !ok {
		return
	}
	skins, err := svc.ListSkins(r.Context())
	if err != nil {
		h.writeVelaSkinError(w, err)
		return
	}
	if skins == nil {
		skins = []VelaSkin{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"skins": skins})
}

func (h *Handler) handleVelaSkinInstall(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.requireVelaSkinOwner(w, r)
	if !ok {
		return
	}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch strings.ToLower(strings.TrimSpace(ct)) {
	case "multipart/form-data":
		mr, err := r.MultipartReader()
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_upload", "Invalid multipart upload.")
			return
		}
		var pkg []byte
		var found bool
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				writeError(w, http.StatusBadRequest, "bad_upload", "Failed to read uploaded .vskin archive.")
				return
			}
			name := part.FormName()
			if name == "file" || name == "package" {
				data, readErr := io.ReadAll(io.LimitReader(part, maxVelaSkinUploadBytes+1))
				_ = part.Close()
				if readErr != nil {
					writeError(w, http.StatusBadRequest, "bad_upload", "Failed to read uploaded .vskin archive.")
					return
				}
				if len(data) > maxVelaSkinUploadBytes {
					writeError(w, http.StatusBadRequest, "too_large", "Skin archive exceeds the 8 MB limit.")
					return
				}
				pkg = data
				found = true
				break
			}
			_ = part.Close()
		}
		if !found {
			writeError(w, http.StatusBadRequest, "bad_upload", "Select a .vskin file to upload.")
			return
		}
		installed, err := svc.InstallSkinArchive(r.Context(), pkg)
		if err != nil {
			h.writeVelaSkinError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, installed)
	case "application/octet-stream":
		if r.Body == nil {
			writeError(w, http.StatusBadRequest, "bad_upload", "Empty .vskin archive.")
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, maxVelaSkinUploadBytes+1))
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_upload", "Failed to read uploaded .vskin archive.")
			return
		}
		if len(data) > maxVelaSkinUploadBytes {
			writeError(w, http.StatusBadRequest, "too_large", "Skin archive exceeds the 8 MB limit.")
			return
		}
		installed, err := svc.InstallSkinArchive(r.Context(), data)
		if err != nil {
			h.writeVelaSkinError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, installed)
	default:
		var req struct {
			URL string `json:"url"`
			LAN bool   `json:"lan"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "bad_json", "Invalid skin install request.")
			return
		}
		installed, err := svc.InstallSkinURL(r.Context(), req.URL, req.LAN)
		if err != nil {
			h.writeVelaSkinError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, installed)
	}
}

func (h *Handler) handleVelaSkinRemove(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.requireVelaSkinOwner(w, r)
	if !ok {
		return
	}
	if err := svc.RemoveSkin(r.Context(), r.PathValue("id")); err != nil {
		h.writeVelaSkinError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
