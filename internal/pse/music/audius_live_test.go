package music

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Explicit opt-in: CI uses fixtures and never depends on public services.
// Audio is decoded from Ether-Remix's relay, without saving a track to disk.
func TestAudiusLive(t *testing.T) {
	if os.Getenv("ACCENT_MUSIC_LIVE") != "1" {
		t.Skip("set ACCENT_MUSIC_LIVE=1 to contact Deezer and Audius")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Fatal("ffmpeg required for full decode")
	}
	allowLocalAudio = false
	defer func() { allowLocalAudio = true }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	source := NewDeezerSource(NewAudius(nil), nil)
	tracks, err := source.SearchTracks(ctx, "Disclosure Waterfall", 5, 0)
	if err != nil || len(tracks) == 0 {
		t.Fatal("live catalogue search failed")
	}
	var id int64
	for _, tr := range tracks {
		if tr.Title == "Waterfall" && tr.Artist.Name == "Disclosure" {
			id = tr.ID
			break
		}
	}
	if id == 0 {
		t.Fatal("exact catalogue recording absent")
	}
	st, err := source.Stream(ctx, id, "")
	if err != nil {
		t.Fatalf("live resolution: %v", redactErr(err))
	}
	var report SourceReport
	if err := probe(ctx, &http.Client{Timeout: 20 * time.Second}, st, &report); err != nil {
		t.Fatal(err)
	}
	if report.Format != "MP3" || report.Lossless {
		t.Fatalf("unexpected format: %+v", report)
	}
	server := NewServer(Options{Source: source})
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := server.relayAudio(w, r, id); err != nil {
			http.Error(w, "stream failed", 502)
		}
	}))
	defer relay.Close()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-v", "error", "-i", relay.URL, "-f", "null", "-")
	if err := cmd.Run(); err != nil {
		t.Fatalf("full decode through Ether-Remix failed: %v", err)
	}
	t.Log("live Deezer search, Audius resolution, MP3 signature and full decode through Ether-Remix passed")
}
