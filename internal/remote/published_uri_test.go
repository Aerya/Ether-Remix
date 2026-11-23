package remote

import "testing"

func TestParsePublishedAddress(t *testing.T) {
	good := []struct {
		in      string
		wantURI string
		wantPfx string
		local   bool
	}{
		{"https://Jellyfin.Example.com/", "https://jellyfin.example.com", "", false},
		{"https://example.com/jellyfin/", "https://example.com/jellyfin", "/jellyfin", false},
		{"http://192.168.1.5:8096", "http://192.168.1.5:8096", "", true},
		{"https://example.com:8443/media/Emby/", "https://example.com:8443/media/Emby", "/media/Emby", false},
		{"http://100.100.1.2:8096/silo", "http://100.100.1.2:8096/silo", "/silo", true},
	}
	for _, tc := range good {
		p, err := ParsePublishedAddress(tc.in)
		if err != nil {
			t.Fatalf("ParsePublishedAddress(%q) error: %v", tc.in, err)
		}
		if p.URI != tc.wantURI || p.Path != tc.wantPfx || p.Local != tc.local {
			t.Fatalf("ParsePublishedAddress(%q) = %+v, want URI=%q Path=%q Local=%v", tc.in, p, tc.wantURI, tc.wantPfx, tc.local)
		}
	}

	bad := []string{
		"",
		"jellyfin.example.com",
		"ftp://jellyfin.example.com",
		"https://user:pass@example.com/jellyfin",
		"https://example.com/jellyfin?token=1",
		"https://example.com/jellyfin#frag",
		"https://example.com/../etc",
		"https://example.com/jellyfin//extra",
		"http://0.0.0.0:8096",
		"http://127.0.0.1:8096",
		"http://localhost:8096",
	}
	for _, raw := range bad {
		if _, err := ParsePublishedAddress(raw); err == nil {
			t.Fatalf("ParsePublishedAddress(%q) unexpectedly succeeded", raw)
		}
	}
}
