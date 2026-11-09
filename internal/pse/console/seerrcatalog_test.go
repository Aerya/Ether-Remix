package console

import (
	"strings"
	"testing"
)

func TestSeerrCatalogCredentialsAndBaseURL(t *testing.T) {
	key := fieldByKey["PSE_SEERRCATALOG_TOKEN"]
	raw := strings.Repeat("a", 64)
	if shown := display(key, raw); shown == raw || restore(key, shown, raw) != raw {
		t.Fatal("token masking/restore")
	}
	for _, v := range []string{"http://user:pass@example.com", "https://example.com?token=secret", "https://example.com/#fragment", "file:///tmp/db"} {
		if _, err := validate(fieldByKey["PSE_SEERRCATALOG_URL"], v, nil); err == nil {
			t.Fatal("unsafe base accepted", v)
		}
	}
	if _, err := validate(key, "short", nil); err == nil {
		t.Fatal("short token accepted")
	}
}

func TestSeerrCatalogEnableRequiresConnection(t *testing.T) {
	h, applied, _ := newHandler(t)
	code, _ := do(t, h, "PUT", "/", `{"values":{"PSE_SEERRCATALOG":"on"}}`)
	if code != 422 || *applied != 0 {
		t.Fatal("incomplete configuration accepted", code, *applied)
	}
}
