package subtitles

import (
	"github.com/braedonsaunders/ether/internal/core"
	"io"
	"strings"
	"testing"
)

func TestExternalSubtitleBoundToReleaseAndEpisode(t *testing.T) {
	s := New(Options{CacheDir: t.TempDir()})
	title := &core.Title{Key: core.EpisodeKey(10, 1, 1)}
	body := []byte("1\n00:00:01,000 --> 00:00:02,000\nBonjour\n\n2\n00:00:03,000 --> 00:00:04,000\nBonsoir\n")
	first, err := s.StoreExternal(title, "fr", "Show.S01E01-GROUP", body, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.StoreExternal(title, "fr", "Show.S01E01-OTHER", body, true)
	if err != nil || first.ExternalURL == second.ExternalURL {
		t.Fatal("release collision", err)
	}
	title.Key = core.EpisodeKey(10, 1, 2)
	third, err := s.StoreExternal(title, "fr", "Show.S01E01-GROUP", body, true)
	if err != nil || third.ExternalURL == first.ExternalURL {
		t.Fatal("episode collision", err)
	}
	if !strings.Contains(first.Title, "AI") || !first.External {
		t.Fatal(first)
	}
	r, _, err := s.Open(first.ExternalURL)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, _ := io.ReadAll(r)
	if !strings.Contains(string(data), "Bonjour") {
		t.Fatal("missing SRT")
	}
	if len(s.ManualSidecars(title, nil)) != 0 {
		t.Fatal("external artifact became an unbound manual sidecar")
	}
}
