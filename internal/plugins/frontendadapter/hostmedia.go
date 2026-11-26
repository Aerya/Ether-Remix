package frontendadapter

import (
	"context"
	"net/http"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// HostMediaServer serves host-owned bytes named by an adapter response: a
// playable kind item (FrontendMedia) or a kind item's stored artwork
// (FrontendArt). Implementations re-check that the item belongs to the plugin
// and that the viewer may open its library before writing anything; allowed
// lists the adapter libraries this request authenticated for.
type HostMediaServer interface {
	ServeFrontendMedia(ctx context.Context, w http.ResponseWriter, r *http.Request, pluginID string, viewerID int64, allowed []core.LibraryID, spec plugins.FrontendMedia)
	ServeFrontendArt(ctx context.Context, w http.ResponseWriter, r *http.Request, pluginID string, viewerID int64, allowed []core.LibraryID, spec plugins.FrontendArt)
}

// writeHostMedia hands a media or art descriptor to the host. Media keeps the
// client's Range header, so seeking works and each range re-authenticates with
// the request's own credentials instead of an expiring ticket.
func (f *Frontend) writeHostMedia(w http.ResponseWriter, r *http.Request, auth authenticated, resp plugins.Response) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	srv, ok := f.mgr.(HostMediaServer)
	if !ok || (resp.Status != 0 && resp.Status != http.StatusOK) || !emptyBody(resp.Body) || (resp.Media != nil && resp.Art != nil) {
		http.Error(w, "invalid media response", http.StatusBadGateway)
		return
	}
	if resp.Media != nil {
		srv.ServeFrontendMedia(r.Context(), w, r, f.decl.PluginID, auth.viewer.ID, auth.libraries, *resp.Media)
		return
	}
	srv.ServeFrontendArt(r.Context(), w, r, f.decl.PluginID, auth.viewer.ID, auth.libraries, *resp.Art)
}

// emptyBody reports whether a descriptor response carried no body: hosted
// runtimes may encode an absent body as null or an empty string.
func emptyBody(body []byte) bool {
	switch string(body) {
	case "", "null", `""`:
		return true
	}
	return false
}
