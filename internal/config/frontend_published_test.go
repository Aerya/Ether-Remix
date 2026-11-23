package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOpenMigratesFrontendPublicURLToPublishedAddresses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{
  "plex": {
    "customUrls": ["https://dashboard.example.com/"]
  },
  "frontends": {
    "jellyfin": {
      "enabled": true,
      "publicUrl": "https://jellyfin.example.com/"
    },
    "emby": {
      "enabled": true,
      "publicUrl": "https://emby.example.com/",
      "publishedAddresses": ["https://media.example.com/emby"]
    },
    "silo": {
      "enabled": true,
      "publicUrl": "https://silo.example.com/",
      "publishedAddresses": ["https://silo.example.com"]
    },
    "plex": {
      "enabled": true,
      "publicUrl": "https://ignored.example.com"
    }
  }
}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cfg := store.Get()
	if got := cfg.Frontends["jellyfin"].PublishedAddresses; !reflect.DeepEqual(got, []string{"https://jellyfin.example.com"}) {
		t.Fatalf("jellyfin publishedAddresses = %v, want [https://jellyfin.example.com]", got)
	}
	if cfg.Frontends["jellyfin"].PublicURL != "" {
		t.Fatalf("jellyfin legacy publicUrl still populated: %q", cfg.Frontends["jellyfin"].PublicURL)
	}
	if got := cfg.Frontends["emby"].PublishedAddresses; !reflect.DeepEqual(got, []string{"https://media.example.com/emby", "https://emby.example.com"}) {
		t.Fatalf("emby publishedAddresses = %v", got)
	}
	if got := cfg.Frontends["silo"].PublishedAddresses; !reflect.DeepEqual(got, []string{"https://silo.example.com"}) {
		t.Fatalf("silo deduped publishedAddresses = %v", got)
	}
	if got := cfg.Frontends["plex"].PublishedAddresses; len(got) != 0 {
		t.Fatalf("plex should not inherit legacy publicUrl into publishedAddresses: %v", got)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), `"publicUrl"`) {
		t.Fatalf("legacy publicUrl key remained on disk after migration:\n%s", saved)
	}
}

func TestMergeSharedMigratesFrontendPublicURL(t *testing.T) {
	shared := []byte(`{"frontends":{"jellyfin":{"enabled":true,"publicUrl":"https://jellyfin.example.com/"}}}`)
	cfg, _, err := mergeShared(Defaults(""), shared)
	if err != nil {
		t.Fatalf("mergeShared: %v", err)
	}
	if got := cfg.Frontends["jellyfin"].PublishedAddresses; !reflect.DeepEqual(got, []string{"https://jellyfin.example.com"}) {
		t.Fatalf("shared jellyfin publishedAddresses = %v", got)
	}
	if cfg.Frontends["jellyfin"].PublicURL != "" {
		t.Fatalf("shared jellyfin legacy publicUrl = %q", cfg.Frontends["jellyfin"].PublicURL)
	}
}
