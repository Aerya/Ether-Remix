package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaFlowSecretRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Update(func(c *Config) error {
		c.Outbound.AllDebridMediaFlow = MediaFlow{Enabled: true, URL: "http://relay:8888", Password: "mediaflow-private-password"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if strings.Contains(string(raw), "mediaflow-private-password") {
		t.Fatal("plaintext password persisted")
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Get().Outbound.AllDebridMediaFlow.Password != "mediaflow-private-password" {
		t.Fatal("password did not round trip")
	}
}
func TestMediaFlowConfiguration(t *testing.T) {
	for _, tc := range []struct {
		m     MediaFlow
		valid bool
	}{
		{MediaFlow{}, true}, {MediaFlow{Enabled: true}, false},
		{MediaFlow{Enabled: true, URL: "http://relay:8888", Password: "secret"}, true},
		{MediaFlow{Enabled: true, URL: "https://relay/prefix", Password: "secret"}, true},
		{MediaFlow{Enabled: true, URL: "http://user:pass@relay", Password: "secret"}, false},
		{MediaFlow{Enabled: true, URL: "http://relay?token=secret", Password: "secret"}, false},
		{MediaFlow{Enabled: true, URL: "socks5://relay", Password: "secret"}, false},
	} {
		if (len(tc.m.Problems()) == 0) != tc.valid {
			t.Errorf("enabled=%v URL=%s", tc.m.Enabled, tc.m.URL)
		}
	}
}
