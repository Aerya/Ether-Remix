package vela

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/favorites"
)

func TestVelaLiveEntitiesAndEntityFavoritesFlow(t *testing.T) {
	env := newVelaScreensEnv(t)
	gammaKey := favorites.FormatEntityKey(env.pluginID, env.feed, "team", "gamma")

	// 1. Query entities before favoriting: both entities carry canonical key and favorite=false.
	wList := httptest.NewRecorder()
	env.handler.ServeHTTP(wList, authVelaRequest(http.MethodGet, "/vela/v0/live/entities?feed=scores"))
	if wList.Code != http.StatusOK {
		t.Fatalf("GET /live/entities = %d: %s", wList.Code, wList.Body.String())
	}
	var entDoc struct {
		Feed       string `json:"feed"`
		EntityKind string `json:"entityKind"`
		Entities   []struct {
			Key      string `json:"key"`
			ID       string `json:"id"`
			Favorite bool   `json:"favorite"`
		} `json:"entities"`
	}
	if err := json.Unmarshal(wList.Body.Bytes(), &entDoc); err != nil {
		t.Fatal(err)
	}
	if entDoc.Feed != "scores" || entDoc.EntityKind != "team" || len(entDoc.Entities) != 2 {
		t.Fatalf("entities doc = %+v", entDoc)
	}
	if entDoc.Entities[1].Key != gammaKey || entDoc.Entities[1].Favorite {
		t.Fatalf("gamma before favorite = %+v", entDoc.Entities[1])
	}

	// 2. PUT /vela/v0/favorites/entities/{key} with raw colons returns 204 No Content.
	wPut := httptest.NewRecorder()
	env.handler.ServeHTTP(wPut, authVelaRequest(http.MethodPut, "/vela/v0/favorites/entities/"+gammaKey))
	if wPut.Code != http.StatusNoContent {
		t.Fatalf("PUT favorite entity = %d: %s", wPut.Code, wPut.Body.String())
	}

	// 3. Entity search now marks gamma favorite=true, and screen hero promotes gamma's live game g2 ahead of g1.
	wList2 := httptest.NewRecorder()
	env.handler.ServeHTTP(wList2, authVelaRequest(http.MethodGet, "/vela/v0/live/entities?feed=scores&league=league-b"))
	if wList2.Code != http.StatusOK {
		t.Fatalf("GET /live/entities filtered = %d: %s", wList2.Code, wList2.Body.String())
	}
	entDoc.Entities = nil
	if err := json.Unmarshal(wList2.Body.Bytes(), &entDoc); err != nil {
		t.Fatal(err)
	}
	if len(entDoc.Entities) != 1 || entDoc.Entities[0].Key != gammaKey || !entDoc.Entities[0].Favorite {
		t.Fatalf("gamma after PUT = %+v", entDoc.Entities)
	}

	wScreen := httptest.NewRecorder()
	env.handler.ServeHTTP(wScreen, authVelaRequest(http.MethodGet, "/vela/v0/screens/scores"))
	if wScreen.Code != http.StatusOK {
		t.Fatalf("GET /screens/scores = %d: %s", wScreen.Code, wScreen.Body.String())
	}
	var screenDoc struct {
		Sections []map[string]any `json:"sections"`
	}
	if err := json.Unmarshal(wScreen.Body.Bytes(), &screenDoc); err != nil {
		t.Fatal(err)
	}
	heroEv := screenDoc.Sections[0]["event"].(map[string]any)
	if heroEv["id"] != env.pluginID+":"+env.feed+":g2" {
		t.Fatalf("hero after favoriting gamma = %v, want g2", heroEv["id"])
	}
	yourTeams := screenDoc.Sections[3]["events"].([]any)
	if len(yourTeams) != 1 || yourTeams[0].(map[string]any)["id"] != env.pluginID+":"+env.feed+":g2" {
		t.Fatalf("your-teams = %#v, want [g2]", yourTeams)
	}

	// 4. DELETE with percent-encoded key and empty body returns 204 No Content and reverts hero to g1.
	wDel := httptest.NewRecorder()
	env.handler.ServeHTTP(wDel, authVelaRequest(http.MethodDelete, "/vela/v0/favorites/entities/"+url.PathEscape(gammaKey)))
	if wDel.Code != http.StatusNoContent {
		t.Fatalf("DELETE favorite entity (empty body) = %d: %s", wDel.Code, wDel.Body.String())
	}
	// Repeating DELETE with a JSON body is also idempotent and returns 204.
	reqDelJSON := httptest.NewRequest(http.MethodDelete, "/vela/v0/favorites/entities/"+gammaKey, strings.NewReader(`{"idempotencyKey":"k1"}`))
	reqDelJSON.Header.Set("Authorization", "Bearer token")
	reqDelJSON.Header.Set("X-Vela-Device-Id", "dev-screens")
	wDelJSON := httptest.NewRecorder()
	env.handler.ServeHTTP(wDelJSON, reqDelJSON)
	if wDelJSON.Code != http.StatusNoContent {
		t.Fatalf("DELETE favorite entity (JSON body) = %d: %s", wDelJSON.Code, wDelJSON.Body.String())
	}

	wScreen2 := httptest.NewRecorder()
	env.handler.ServeHTTP(wScreen2, authVelaRequest(http.MethodGet, "/vela/v0/screens/scores"))
	screenDoc.Sections = nil
	_ = json.Unmarshal(wScreen2.Body.Bytes(), &screenDoc)
	if heroEv2 := screenDoc.Sections[0]["event"].(map[string]any); heroEv2["id"] != env.pluginID+":"+env.feed+":g1" {
		t.Fatalf("hero after DELETE = %v, want g1", heroEv2["id"])
	}

	// 5. Validation & auth error codes.
	wMissingFeed := httptest.NewRecorder()
	env.handler.ServeHTTP(wMissingFeed, authVelaRequest(http.MethodGet, "/vela/v0/live/entities"))
	if wMissingFeed.Code != http.StatusBadRequest {
		t.Fatalf("missing feed = %d, want 400", wMissingFeed.Code)
	}
	wUnknownFeed := httptest.NewRecorder()
	env.handler.ServeHTTP(wUnknownFeed, authVelaRequest(http.MethodGet, "/vela/v0/live/entities?feed=nope"))
	if wUnknownFeed.Code != http.StatusNotFound {
		t.Fatalf("unknown feed = %d, want 404", wUnknownFeed.Code)
	}
	wBadKey := httptest.NewRecorder()
	env.handler.ServeHTTP(wBadKey, authVelaRequest(http.MethodPut, "/vela/v0/favorites/entities/bad-key"))
	if wBadKey.Code != http.StatusBadRequest {
		t.Fatalf("bad entity key = %d, want 400", wBadKey.Code)
	}
	wForbiddenKey := httptest.NewRecorder()
	env.handler.ServeHTTP(wForbiddenKey, authVelaRequest(http.MethodPut, "/vela/v0/favorites/entities/community.other.disabled:scores:team:x"))
	if wForbiddenKey.Code != http.StatusForbidden {
		t.Fatalf("disabled plugin entity key = %d, want 403", wForbiddenKey.Code)
	}
	wUnauth := httptest.NewRecorder()
	env.handler.ServeHTTP(wUnauth, httptest.NewRequest(http.MethodPut, "/vela/v0/favorites/entities/"+gammaKey, nil))
	if wUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauth PUT = %d, want 401", wUnauth.Code)
	}
}
