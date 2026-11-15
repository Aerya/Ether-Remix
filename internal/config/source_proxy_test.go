package config

import (
	"strings"
	"testing"
)

func TestSourceProxyCredentialsSealedAndBound(t *testing.T) {
	doc := []byte(`{"debrid":{"providers":[{"id":"account","proxy":{"enabled":true,"url":"http://proxy:8000","username":"proxy-user","password":"proxy-secret"}}]},"indexers":[{"id":"addon","requestProxy":{"enabled":true,"url":"http://request:8000","password":"request-secret"},"streamProxy":{"enabled":true,"url":"http://stream:8000","password":"stream-secret"}}]}`)
	key := make([]byte, 32)
	sealed, err := sealDocument(doc, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"proxy-user", "proxy-secret", "request-secret", "stream-secret"} {
		if strings.Contains(string(sealed), v) {
			t.Fatal("secret stored in plaintext")
		}
	}
	opened, _, err := openDocument(sealed, key)
	if err != nil || !strings.Contains(string(opened), "stream-secret") {
		t.Fatal("roundtrip failed", err)
	}
	moved := strings.ReplaceAll(string(sealed), "http://proxy:8000", "http://other:8000")
	if _, _, err := openDocument([]byte(moved), key); err == nil {
		t.Fatal("secret usable at another proxy destination")
	}
}
func TestSourceProxyValidation(t *testing.T) {
	for _, url := range []string{"", "http://user:password@proxy:80", "http://proxy", "ftp://proxy:80"} {
		if len((SourceProxy{Enabled: true, URL: url}).Problems("source.proxy")) == 0 {
			t.Errorf("accepted invalid proxy %q", url)
		}
	}
	for _, url := range []string{"http://proxy:80", "https://proxy:443", "socks5://proxy:1080"} {
		if len((SourceProxy{Enabled: true, URL: url}).Problems("source.proxy")) != 0 {
			t.Errorf("rejected valid proxy %q", url)
		}
	}
}
