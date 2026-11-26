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
