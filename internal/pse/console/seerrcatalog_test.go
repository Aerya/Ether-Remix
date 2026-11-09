package console

import (
	"github.com/braedonsaunders/ether/internal/pse/pseenv"
	"net/http"
	"net/http/httptest"
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

func TestSeerrCatalogProbeUsesSavedSettingsWithoutApplying(t *testing.T) {
	h, applied, _ := newHandler(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 32) {
			t.Error("wrong saved token")
		}
		w.Write([]byte(`{"schema":"seerrcatalog.availability.v1","revision":0,"items":[]}`))
	}))
	defer server.Close()
	pseenv.Replace(map[string]string{"PSE_SEERRCATALOG_URL": server.URL, "PSE_SEERRCATALOG_TOKEN": strings.Repeat("x", 32)})
	code, body := do(t, h, "POST", "/seerrcatalog/test", `{}`)
	if code != 200 || body["code"] != "ok" || calls != 1 || *applied != 0 {
		t.Fatalf("code %d body %v calls %d applied %d", code, body, calls, *applied)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/seerrcatalog/test", nil))
	if rec.Code != 405 {
		t.Fatalf("GET accepted: %d", rec.Code)
	}
}
