package app

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plextv"
)

func TestPlexLibrariesChangeWithoutRestart(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := t.Context()
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "test-owner"); err != nil {
		t.Fatal(err)
	}
	inst, ok := a.frontends.registry.Instance("plex")
	if !ok {
		t.Fatal("Plex not mounted")
	}
	handler := inst.Handler()
	sections := func() map[string]string {
		t.Helper()
		req := httptest.NewRequest("GET", "/library/sections", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Plex-Token", "test-owner")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		var body struct {
			MediaContainer struct {
				Directory []struct {
					Key   string
					Title string
				}
			}
		}
		if w.Code != 200 {
			t.Fatalf("sections: %d %s", w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, d := range body.MediaContainer.Directory {
			out[d.Key] = d.Title
		}
		return out
	}
	if len(sections()) != 2 {
		t.Fatalf("initial sections: %v", sections())
	}
	var present core.Presentations
	if err := json.Unmarshal([]byte(`{"plex":{"base":"movie"}}`), &present); err != nil {
		t.Fatal(err)
	}
	lib, err := a.libraries.CreateCollection(ctx, "Xperience picks", 42, core.TypeMovie, present)
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(int64(lib.ID), 10)
	if sections()[id] != lib.Name {
		t.Fatalf("new library missing: %v", sections())
	}
	if err := a.libraries.Rename(ctx, lib.ID, "Renamed picks"); err != nil {
		t.Fatal(err)
	}
	if sections()[id] != "Renamed picks" {
		t.Fatalf("stale name: %v", sections())
	}
	for _, builtin := range []core.LibraryID{1, 2} {
		if err := a.libraries.SetHidden(ctx, builtin, true); err != nil {
			t.Fatal(err)
		}
	}
	if got := sections(); len(got) != 1 || got[id] != "Renamed picks" {
		t.Fatalf("hidden builtins or missing collection: %v", got)
	}
	if err := a.libraries.SetHidden(ctx, lib.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := sections(); len(got) != 0 {
		t.Fatalf("hidden libraries returned: %v", got)
	}
	if err := a.libraries.SetHidden(ctx, lib.ID, false); err != nil {
		t.Fatal(err)
	}
	if sections()[id] != "Renamed picks" {
		t.Fatal("unhidden library missing")
	}
	if err := a.libraries.Delete(ctx, lib.ID); err != nil {
		t.Fatal(err)
	}
	if got := sections(); len(got) != 0 {
		t.Fatalf("deleted library returned: %v", got)
	}
	current, _ := a.frontends.registry.Instance("plex")
	if current.Handler() != handler {
		t.Fatal("Plex handler restarted")
	}
}
