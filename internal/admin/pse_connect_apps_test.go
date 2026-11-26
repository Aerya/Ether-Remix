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
	for _, key := range []string{"qr:compact", "qr:detaille", "filesQr:compact", "filesQr:detaille"} {
		if !strings.HasPrefix(result[key], "data:image/png;base64,") {
			t.Errorf("missing %s", key)
		}
	}
	if result["qr:compact"] == result["filesQr:compact"] || result["qr:compact"] == result["qr:detaille"] {
		t.Fatal("option QR codes must differ per link and per format")
	}
}
