package admin_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestViewerDisplayNameRoute(t *testing.T) {
	store := newStore(t, nil)
	users := &fakeUsers{users: []core.User{{ID: 1, Username: "owner", IsOwner: true}}}
	var gotID int64
	var gotName string
	h := admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store), Users: users, Personalization: &fakeEngine{},
		RenameViewer: func(_ context.Context, id int64, name string) error {
			gotID, gotName = id, name
			return nil
		}})
	if rec := serve(h, req(http.MethodPut, "/ether/api/viewers/1/display-name", `{"name":"  Julien "}`)); rec.Code != 200 {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}
	if gotID != 1 || gotName != "Julien" {
		t.Fatalf("renamed %d to %q", gotID, gotName)
	}
	body := `{"name":"` + strings.Repeat("x", core.MaxCustomNameLength+1) + `"}`
	if rec := serve(h, req(http.MethodPut, "/ether/api/viewers/1/display-name", body)); rec.Code != 400 {
		t.Fatalf("too long: %d %s", rec.Code, rec.Body)
	}
	if rec := serve(h, req(http.MethodPut, "/ether/api/viewers/99/display-name", `{"name":"x"}`)); rec.Code != 404 {
		t.Fatalf("unknown viewer: %d %s", rec.Code, rec.Body)
	}
}
