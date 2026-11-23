package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
)

func TestPosterSecrets(t *testing.T) {
	c := config.Defaults(t.TempDir())
	c.Posters = config.Posters{Enabled: true, Source: "custom", APIKey: "private-key", URLTemplate: "http://lan/{type}/{tmdb_id}?secret=private-template"}
	redacted := RedactConfig(c)
	raw, _ := json.Marshal(redacted)
	if strings.Contains(string(raw), "private-") {
		t.Fatal("secret exposed")
	}
	restored, errs := UnredactConfig(redacted, c)
	if len(errs) > 0 || restored.Posters != c.Posters {
		t.Fatal(errs)
	}
	redacted.Posters.URLTemplate = "http://other/{type}/{tmdb_id}?key={api_key}"
	if _, errs = UnredactConfig(redacted, c); len(errs) == 0 {
		t.Fatal("forwarded retained key to new template")
	}
}

func TestXRDBSecretRedaction(t *testing.T) {
	c := config.Defaults(t.TempDir())
	c.Posters = config.Posters{Enabled: true, Source: "xrdb", BaseURL: "https://xrdb.example", Profile: "home", APIKey: "private-key"}
	r := RedactConfig(c)
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), "private-key") {
		t.Fatal("key exposed")
	}
	restored, errs := UnredactConfig(r, c)
	if len(errs) != 0 || restored.Posters != c.Posters {
		t.Fatal(errs)
	}
	r.Posters.BaseURL = "https://other.example"
	if _, errs := UnredactConfig(r, c); len(errs) == 0 {
		t.Fatal("retained key sent to changed instance")
	}
}

func TestIndependentPosterAccountSecrets(t *testing.T) {
	c := config.Defaults(t.TempDir())
	c.Posters = config.Posters{Version: 1, Source: "tmdb"}
	c.Posters.Accounts.RPDB = config.PosterAccount{Enabled: true, APIKey: "rpdb-secret"}
	c.Posters.Accounts.Custom = config.PosterAccount{Enabled: true, APIKey: "custom-secret", URLTemplate: "https://example.com/{type}/{tmdb_id}?key={api_key}"}
	c.Posters.Accounts.Pictorium = config.PosterAccount{Enabled: true, URLTemplate: "https://example.com/api/poster/{type}/{imdb_id}?api_key=pictorium-secret"}
	c.Posters.Accounts.TopPosters = config.PosterAccount{Enabled: true, APIKey: "top-secret", URLTemplate: "https://api.top-posters.com/{api_key}/imdb/poster/{imdb_id}.jpg"}
	redacted := RedactConfig(c)
	if redacted.Posters.Accounts.Pictorium.URLTemplate != RedactedSecret || redacted.Posters.Accounts.TopPosters.APIKey != RedactedSecret {
		t.Fatal("new provider secret leaked")
	}
	if redacted.Posters.Accounts.RPDB.APIKey != RedactedSecret || redacted.Posters.Accounts.Custom.URLTemplate != RedactedSecret {
		t.Fatal("account secret leaked")
	}
	redacted.Posters.Source = "custom"
	restored, errs := UnredactConfig(redacted, c)
	if len(errs) > 0 || restored.Posters.Accounts != c.Posters.Accounts {
		t.Fatal("selecting a source lost another account's credentials", errs)
	}
}

func TestSavingPostersSectionRollsGeneration(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Plex.Token = "owner-tok"
		c.Plex.Claimed = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(Deps{
		Config:       store,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		IsOwnerToken: func(_ context.Context, tok string) bool { return tok == "owner-tok" },
	})
	req := func(method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.RemoteAddr = "192.168.1.50:49152"
		r.Header.Set("X-Plex-Token", "owner-tok")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Ether-Request", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if got := store.Get().Posters.Generation; got != 0 {
		t.Fatalf("initial generation = %d, want 0", got)
	}

	// Enable BetterPosters via PUT /api/config/posters -> generation 1.
	p := config.Posters{Version: 1, Source: "betterposters"}
	p.Accounts.BetterPosters = config.PosterAccount{Enabled: true}
	if w := req(http.MethodPut, "/api/config/posters", p); w.Code != http.StatusOK {
		t.Fatalf("enable posters: %d %s", w.Code, w.Body.String())
	}
	if got := store.Get().Posters.Generation; got != 1 {
		t.Fatalf("after enable generation = %d, want 1", got)
	}

	// Re-saving the exact same Posters section still rolls generation -> 2.
	if w := req(http.MethodPut, "/api/config/posters", p); w.Code != http.StatusOK {
		t.Fatalf("resave posters: %d %s", w.Code, w.Body.String())
	}
	if got := store.Get().Posters.Generation; got != 2 {
		t.Fatalf("after resave generation = %d, want 2", got)
	}

	// Saving an unrelated section does not change posters.generation.
	if w := req(http.MethodPatch, "/api/config/updates", map[string]any{"channel": "stable"}); w.Code != http.StatusOK {
		t.Fatalf("unrelated section: %d %s", w.Code, w.Body.String())
	}
	if got := store.Get().Posters.Generation; got != 2 {
		t.Fatalf("unrelated save changed generation = %d, want 2", got)
	}

	// Disabling rolls generation -> 3.
	p.Source = "tmdb"
	p.Accounts.BetterPosters.Enabled = false
	if w := req(http.MethodPut, "/api/config/posters", p); w.Code != http.StatusOK {
		t.Fatalf("disable posters: %d %s", w.Code, w.Body.String())
	}
	if got := store.Get().Posters.Generation; got != 3 {
		t.Fatalf("after disable generation = %d, want 3", got)
	}

	// PUT /api/config with unchanged posters keeps generation = 3 (even if client sent stale 0).
	full := RedactConfig(store.Get())
	full.Posters.Generation = 0
	if w := req(http.MethodPut, "/api/config", full); w.Code != http.StatusOK {
		t.Fatalf("full save unchanged: %d %s", w.Code, w.Body.String())
	}
	if got := store.Get().Posters.Generation; got != 3 {
		t.Fatalf("full save unchanged generation = %d, want 3", got)
	}

	// PUT /api/config with a poster change rolls generation -> 4.
	full = RedactConfig(store.Get())
	full.Posters.Accounts.BetterPosters.Enabled = true
	full.Posters.Source = "betterposters"
	if w := req(http.MethodPut, "/api/config", full); w.Code != http.StatusOK {
		t.Fatalf("full save changed: %d %s", w.Code, w.Body.String())
	}
	if got := store.Get().Posters.Generation; got != 4 {
		t.Fatalf("full save changed generation = %d, want 4", got)
	}
}
