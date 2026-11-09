package console

import (
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/pse/seerrcatalog"
	"net/http"
	"time"
)

func (h *handler) testSeerrCatalog(w http.ResponseWriter, r *http.Request) {
	s := seerrcatalog.New(nil, egress.Client(egress.CapMetadata, 10*time.Second))
	code := s.Probe(r.Context(), seerrcatalog.Load())
	writeJSON(w, http.StatusOK, map[string]any{"ok": code == "ok", "code": code})
}
