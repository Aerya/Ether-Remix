package admin

import (
	"net/url"
	"strings"
	"testing"
)

func TestPSEStremioFilesLink(t *testing.T) {
	u, _ := url.Parse("https://ether.example/prefix/stremio/secret/manifest.json")
	result := map[string]string{}
	pseStremioFiles(result, u, "/prefix/stremio", "secret")
	if result["filesUrl"] != "https://ether.example/prefix/stremio/secret/fichiers/manifest.json" || !strings.HasPrefix(result["filesQr"], "data:image/png;base64,") {
		t.Fatalf("files link: %+v", result)
	}
}

func TestPSEStremioOptionQRs(t *testing.T) {
	u, _ := url.Parse("https://ether.example/prefix/stremio/secret/manifest.json")
	result := map[string]string{}
	pseStremioFiles(result, u, "/prefix/stremio", "secret")
	for _, key := range []string{"qr:compact-1", "qr:detaille-10", "filesQr:compact-5", "filesQr:detaille-3"} {
		if !strings.HasPrefix(result[key], "data:image/png;base64,") {
			t.Errorf("missing %s", key)
		}
	}
	if _, ok := result["qr:compact-11"]; ok || result["qr:compact-5"] == result["filesQr:compact-5"] || result["qr:compact-5"] == result["qr:detaille-5"] {
		t.Fatal("option QR codes must differ per link and per option")
	}
}
