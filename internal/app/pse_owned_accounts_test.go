package app

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPersonalMediaAPIScopesAccountsAndRejectsHostPaths(t *testing.T) {
	db := store.OpenTest(t)
	a := &App{db: db}
	handler := a.ownedMediaAPI()
	call := func(viewer int64, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r = r.WithContext(core.WithCatalogViewer(r.Context(), &core.User{ID: viewer}))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := call(7, "POST", "/accounts", `{"name":"Mon compte"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	accounts, err := a.ibAccounts(t.Context(), 7)
	if err != nil || len(accounts) != 1 || accounts[0].Access.OwnerID != 7 || accounts[0].Access.SharedAll {
		t.Fatal(accounts, err)
	}
	if got := call(8, "GET", "/accounts", ""); got.Code != 200 || strings.TrimSpace(got.Body.String()) != "[]" {
		t.Fatal(got.Code, got.Body.String())
	}
	if got := call(8, "DELETE", "/ibroadcast?account=2", ""); got.Code != http.StatusNotFound {
		t.Fatal(got.Code)
	}
	if got := call(7, "POST", "/local", `{"name":"Secrets","path":"/etc","kind":"audio"}`); got.Code != http.StatusForbidden {
		t.Fatal(got.Code)
	}
	if mediaaccess.Allowed(t.Context(), db, "ibroadcast:2", 8) {
		t.Fatal("new account shared by default")
	}
	if got := call(8, "PUT", "/sharing", `{"source":"ibroadcast:2","policy":{"sharedAll":true}}`); got.Code == 200 {
		t.Fatal("other user altered sharing")
	}
}
