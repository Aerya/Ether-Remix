package plex

import (
	"fmt"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// Ether-Remix : un téléchargement sur le client torrent n'est plus annoncé
// comme envoyé « au service debrid ».
func TestPlaybackMessageLocalTorrentClient(t *testing.T) {
	err := &core.UncachedError{Err: fmt.Errorf("x: %w", &core.LocalDownloadError{Progress: 0.25, Lang: "fr"})}
	if msg := playbackMessage(err); !strings.Contains(msg, "client torrent") || !strings.Contains(msg, "25 %") {
		t.Fatalf("message %q", msg)
	}
	if msg := playbackMessage(fmt.Errorf("x: %w", core.ErrDownloadQueued)); msg != msgDownloadQueued {
		t.Fatalf("debrid : %q", msg)
	}
}
