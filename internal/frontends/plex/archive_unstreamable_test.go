package plex

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestArchiveUnstreamableErrorSurfacesPlainly(t *testing.T) {
	wrapped := fmt.Errorf("open container: %w", frontend.ErrArchiveUnstreamable)

	if got := playbackMessage(wrapped); got != "This archive format or compression method cannot be streamed." {
		t.Fatalf("playbackMessage = %q", got)
	}

	srv := &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/library/parts/1/1/file.bin", nil)
	srv.writeErr(rec, req, wrapped)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "" {
		t.Fatalf("unexpected Retry-After header %q on permanent archive error", rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), "This archive format or compression method cannot be streamed.") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}
