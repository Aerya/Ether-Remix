package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

func TestPeerAPITestBeforeEnable(t *testing.T) {
	var calls int
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-API-Key") != "fixture-key" {
			t.Error("missing credential")
		}
		io.WriteString(w, `{"version":"fixture"}`)
	}))
	defer remote.Close()
	restore := pseenv.Replace(map[string]string{"PSE_SLSKD": "off", "PSE_SLSKD_URL": remote.URL, "PSE_SLSKD_KEY": "fixture-key"})
	defer restore()
	a := &App{}
	r := httptest.NewRequest("POST", "/peer/slskd/test", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	a.psePeerAPI(w, r)
	if calls != 1 || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("%d %s", calls, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "fixture-key") {
		t.Fatal("secret exposed")
	}
}
