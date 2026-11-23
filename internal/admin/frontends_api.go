package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/remote"
)

func (h *Handler) presentConnectURLs(r *http.Request, info FrontendInfo) FrontendInfo {
	if h.deps.Config == nil {
		return info
	}
	cfg := h.deps.Config.Get()
	id := string(info.Descriptor.ID)
	info.Status.ConnectURLs = consoleConnectURLs(r, id, info.Status.ConnectURLs, cfg, h.proxyPrefixes())
	if len(info.Status.PublishedAddresses) == 0 {
		if consoleFrontendPolicy(id).MainListener {
			for _, raw := range cfg.Plex.CustomURLs {
				if c, err := remote.ParseCustomURL(strings.TrimSpace(raw)); err == nil {
					info.Status.PublishedAddresses = append(info.Status.PublishedAddresses, strings.TrimRight(c.URI, "/"))
				}
			}
		} else {
			info.Status.PublishedAddresses = frontendPublishedURLs(cfg, id)
		}
	}
	if info.PortalURL == "" {
		info.PortalURL = h.publishedPortalURL()
	}
	return info
}

// FrontendAdmin is supplied by the app's protocol lifecycle manager.
type FrontendAdmin interface {
	List(context.Context) ([]FrontendInfo, error)
	Settings(context.Context, frontend.Protocol) (frontend.Settings, error)
	PutSettings(context.Context, frontend.Protocol, frontend.Settings) error
	Toggle(context.Context, frontend.Protocol, bool) error
}
type FrontendInfo struct {
	Descriptor frontend.Descriptor `json:"descriptor"`
	Enabled    bool                `json:"enabled"`
	Status     frontend.Status     `json:"status"`
	PortalURL  string              `json:"portalUrl,omitempty"`
}

func (h *Handler) frontendAvailable(w http.ResponseWriter) bool {
	if h.deps.Frontends == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "Front-end management is unavailable")
		return false
	}
	return true
}
func (h *Handler) frontendList(w http.ResponseWriter, r *http.Request) {
	if !h.frontendAvailable(w) {
		return
	}
	list, err := h.deps.Frontends.List(r.Context())
	if err != nil {
		writeError(w, 500, "frontend_error", "Could not list front ends")
		return
	}
	for i := range list {
		list[i] = h.presentConnectURLs(r, list[i])
	}
	writeJSON(w, 200, list)
}
func (h *Handler) frontendStatus(w http.ResponseWriter, r *http.Request) {
	if !h.frontendAvailable(w) {
		return
	}
	list, err := h.deps.Frontends.List(r.Context())
	if err != nil {
		writeError(w, 500, "frontend_error", "Could not read front-end status")
		return
	}
	for _, item := range list {
		if string(item.Descriptor.ID) == r.PathValue("id") {
			writeJSON(w, 200, h.presentConnectURLs(r, item).Status)
			return
		}
	}
	writeError(w, 404, "not_found", "Front end not found")
}
func (h *Handler) frontendSettings(w http.ResponseWriter, r *http.Request) {
	if !h.frontendAvailable(w) {
		return
	}
	id := frontend.Protocol(r.PathValue("id"))
	if r.Method == http.MethodGet {
		settings, err := h.deps.Frontends.Settings(r.Context(), id)
		if err != nil {
			writeError(w, 404, "not_found", "Front end not found")
			return
		}
		writeJSON(w, 200, settings)
		return
	}
	var settings frontend.Settings
	if err := decodeJSON(r, &settings); err != nil || settings == nil {
		writeError(w, 400, "invalid_json", "Invalid front-end settings")
		return
	}
	if err := h.deps.Frontends.PutSettings(r.Context(), id, settings); err != nil {
		writeError(w, 400, "frontend_error", "Could not update front end")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (h *Handler) frontendToggle(w http.ResponseWriter, r *http.Request) {
	if !h.frontendAvailable(w) {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Enabled == nil {
		writeError(w, 400, "invalid_json", "Enabled is required")
		return
	}
	if err := h.deps.Frontends.Toggle(r.Context(), frontend.Protocol(r.PathValue("id")), *body.Enabled); err != nil {
		writeError(w, 400, "frontend_error", "Could not update front end")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// frontendHealth reports readiness on this node, including disabled protocols.
// It uses the owner API so probes do not expose listeners or failure details.
func (h *Handler) frontendHealth(w http.ResponseWriter, r *http.Request) {
	if !h.frontendAvailable(w) {
		return
	}
	list, err := h.deps.Frontends.List(r.Context())
	if err != nil {
		writeError(w, 503, "unavailable", "Could not read front-end health")
		return
	}
	for _, item := range list {
		if string(item.Descriptor.ID) != r.PathValue("id") {
			continue
		}
		healthy := item.Enabled && item.Status.Mounted && item.Status.Error == ""
		status := http.StatusOK
		if !healthy {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, map[string]any{"protocol": item.Descriptor.ID, "healthy": healthy, "enabled": item.Enabled, "mounted": item.Status.Mounted})
		return
	}
	writeError(w, 404, "not_found", "Front end not found")
}

// FrontendAPIKeys manages Jellyfin/Emby API keys for the owner console.
type FrontendAPIKeys interface {
	List(ctx context.Context, surface string) ([]frontend.APIKeyInfo, error)
	RevokeByID(ctx context.Context, surface string, id int64) error
}

type frontendAPIKeyItem struct {
	ID        int64      `json:"id"`
	AppName   string     `json:"appName"`
	CreatedAt time.Time  `json:"createdAt"`
	LastUsed  *time.Time `json:"lastUsed,omitempty"`
}

func (h *Handler) frontendAPIKeys(w http.ResponseWriter, r *http.Request) {
	if h.deps.FrontendAPIKeys == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "API key management is unavailable")
		return
	}
	id := r.PathValue("id")
	if id != "jellyfin" && id != "emby" {
		writeError(w, http.StatusNotFound, "not_found", "API keys are not supported for this front end")
		return
	}
	keys, err := h.deps.FrontendAPIKeys.List(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "api_keys_error", "Could not list API keys")
		return
	}
	out := make([]frontendAPIKeyItem, 0, len(keys))
	for _, k := range keys {
		item := frontendAPIKeyItem{
			ID:        k.ID,
			AppName:   k.AppName,
			CreatedAt: k.CreatedAt,
		}
		if !k.LastUsed.IsZero() {
			t := k.LastUsed
			item.LastUsed = &t
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) frontendAPIKeyRevoke(w http.ResponseWriter, r *http.Request) {
	if h.deps.FrontendAPIKeys == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "API key management is unavailable")
		return
	}
	id := r.PathValue("id")
	if id != "jellyfin" && id != "emby" {
		writeError(w, http.StatusNotFound, "not_found", "API keys are not supported for this front end")
		return
	}
	keyID, err := strconv.ParseInt(r.PathValue("keyId"), 10, 64)
	if err != nil || keyID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_id", "Invalid API key id")
		return
	}
	if err := h.deps.FrontendAPIKeys.RevokeByID(r.Context(), id, keyID); err != nil {
		if errors.Is(err, core.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "API key not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "api_keys_error", "Could not revoke API key")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
