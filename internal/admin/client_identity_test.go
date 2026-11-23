package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
)

func TestClientIdentitySection(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		seededConfig(c)
		c.SetupComplete = true
		c.Indexers = []config.Indexer{{
			ID: "ix1", Name: "Geek", URL: "https://api.nzbgeek.info", APIKey: "ix-secret", Enabled: true,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	h := admin.NewHandler(admin.Deps{
		Config:       store,
		IsOwnerToken: storedOwner(store),
		TestIndexerIdentity: func(_ context.Context, ix config.Indexer, policy config.ClientIdentity) (config.Identity, error) {
			return policy.ResolveIndexer(ix, config.RequestSearch, ix.URL), nil
		},
	})
	put := func(body string) *httptest.ResponseRecorder {
		r := req(http.MethodPut, "/ether/api/config/clientIdentity", body)
		r.Header.Set("X-Plex-Token", ownerToken)
		return serve(h, r)
	}

	// Sensitive rule header and param values are sealed at rest and redacted in responses.
	rec := put(`{"rules":[{"id":"r1","target":"nzb_downloads","profile":"sabnzbd","headers":[{"name":"Authorization","value":"Bearer s3cret"}],"params":[{"name":"token","value":"p3cret"}]}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT clientIdentity: %d %s", rec.Code, rec.Body)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "s3cret") || strings.Contains(string(raw), "p3cret") {
		t.Fatalf("rule secrets written in plaintext:\n%s", raw)
	}

	r := req(http.MethodGet, "/ether/api/config/clientIdentity", "")
	r.Header.Set("X-Plex-Token", ownerToken)
	rec = serve(h, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET clientIdentity: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "s3cret") || strings.Contains(rec.Body.String(), "p3cret") || !strings.Contains(rec.Body.String(), "••••") {
		t.Fatalf("section GET leaks secrets: %s", rec.Body)
	}
	var section struct {
		Value            json.RawMessage `json:"value"`
		DefaultUserAgent string          `json:"defaultUserAgent"`
	}
	decode(t, rec, &section)
	if section.DefaultUserAgent != config.DefaultUserAgent() || !strings.Contains(section.DefaultUserAgent, "/docs/indexer-operators.md") {
		t.Fatalf("defaultUserAgent = %q, want %q", section.DefaultUserAgent, config.DefaultUserAgent())
	}

	// Sending the redacted form back keeps the stored secrets.
	if rec = put(string(section.Value)); rec.Code != http.StatusOK {
		t.Fatalf("re-PUT redacted section: %d %s", rec.Code, rec.Body)
	}
	gotRule := store.Get().ClientIdentity.Rules[0]
	if gotRule.Headers[0].Value != "Bearer s3cret" || gotRule.Params[0].Value != "p3cret" {
		t.Fatalf("redacted round-trip lost secrets: %+v", gotRule)
	}

	// Removing apikey/t is rejected on validation.
	rec = put(`{"rules":[{"id":"r2","target":"all_searches","profile":"ether","params":[{"name":"apikey","remove":true}]}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("removing apikey should fail: %d %s", rec.Code, rec.Body)
	}

	// Identity caps test resolves draft rules for the chosen indexer.
	testReq := req(http.MethodPost, "/ether/api/test/indexer-identity", `{"indexerId":"ix1","rules":[{"id":"r-sonarr","target":"indexer","indexerId":"ix1","profile":"sonarr"}]}`)
	testReq.Header.Set("X-Plex-Token", ownerToken)
	rec = serve(h, testReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST test/indexer-identity: %d %s", rec.Code, rec.Body)
	}
	var testRes struct {
		OK      bool   `json:"ok"`
		Profile string `json:"profile"`
		RuleID  string `json:"ruleId"`
	}
	decode(t, rec, &testRes)
	if !testRes.OK || testRes.Profile != "sonarr" || testRes.RuleID != "r-sonarr" {
		t.Fatalf("unexpected identity test result: %+v", testRes)
	}
}
