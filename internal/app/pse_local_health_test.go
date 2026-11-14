package app

import (
	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalHealthAdminOnly(t *testing.T) {
	db := store.OpenTest(t)
	users, err := auth.New(t.Context(), db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, users: users}
	handler := a.ownedMediaAPI()
	call := func(viewer int64) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/local/health", nil)
		if viewer > 0 {
			r = r.WithContext(core.WithCatalogViewer(r.Context(), &core.User{ID: viewer}))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := call(0); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err = db.Exec(`INSERT INTO auth_users(id,username,is_owner,active,origin,created_at,updated_at) VALUES(1,'owner',1,1,'local',1,1)`); err != nil {
		t.Fatal(err)
	}
	a.users, err = auth.New(t.Context(), db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if w := call(2); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(0); w.Code != 200 || !strings.Contains(w.Body.String(), `"roots":[]`) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body.String())
	}
}
