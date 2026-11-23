package config

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityRulePrecedence(t *testing.T) {
	policy := ClientIdentity{
		Rules: []IdentityRule{
			{ID: "r-all-search", Target: TargetAllSearches, Profile: "radarr"},
			{ID: "r-nzb", Target: TargetNZBDownloads, Profile: "sabnzbd"},
			{ID: "r-torrent", Target: TargetTorrentDownloads, Profile: "nzbget"},
			{ID: "r-wild", Target: TargetHost, Host: "*.example.com", Profile: "prowlarr"},
			{ID: "r-exact", Target: TargetHost, Host: "api.example.com", Profile: "sonarr"},
			{ID: "r-ix", Target: TargetIndexer, IndexerID: "ix-1", Profile: "nzbhydra2"},
		},
	}

	// Specific indexer beats exact host, wildcard host, download type, and all searches.
	id := policy.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-1", URL: "https://api.example.com/api"})
	if id.RuleID != "r-ix" || id.Profile != "nzbhydra2" {
		t.Fatalf("specific indexer precedence: got rule=%q profile=%q", id.RuleID, id.Profile)
	}
	// Exact host beats wildcard host and catch-alls.
	id = policy.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-2", URL: "https://api.example.com/api"})
	if id.RuleID != "r-exact" || id.Profile != "sonarr" {
		t.Fatalf("exact host precedence: got rule=%q profile=%q", id.RuleID, id.Profile)
	}
	// Wildcard host matches subdomain and beats download type / all searches.
	id = policy.ResolveRequest(IdentityRequest{Kind: RequestNZBDownload, IndexerID: "ix-2", URL: "https://dl.example.com/file.nzb"})
	if id.RuleID != "r-wild" || id.Profile != "prowlarr" {
		t.Fatalf("wildcard host precedence: got rule=%q profile=%q", id.RuleID, id.Profile)
	}
	// Wildcard *.example.com does NOT match bare example.com or suffix-spoofed notexample.com.
	for _, host := range []string{"https://example.com/file.nzb", "https://notexample.com/file.nzb"} {
		id = policy.ResolveRequest(IdentityRequest{Kind: RequestNZBDownload, IndexerID: "ix-2", URL: host})
		if id.RuleID != "r-nzb" || id.Profile != "sabnzbd" {
			t.Fatalf("wildcard boundary for %s: got rule=%q profile=%q", host, id.RuleID, id.Profile)
		}
	}
	// Download type matches torrent downloads; all_searches matches unmatched searches.
	id = policy.ResolveRequest(IdentityRequest{Kind: RequestTorrentDownload, IndexerID: "ix-2", URL: "https://tracker.other.org/a.torrent"})
	if id.RuleID != "r-torrent" || id.Profile != "nzbget" {
		t.Fatalf("torrent download rule: got rule=%q profile=%q", id.RuleID, id.Profile)
	}
	id = policy.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-2", URL: "https://other.org/api"})
	if id.RuleID != "r-all-search" || id.Profile != "radarr" {
		t.Fatalf("all searches rule: got rule=%q profile=%q", id.RuleID, id.Profile)
	}
	// Order breaks ties within the same specificity tier.
	tied := ClientIdentity{
		Rules: []IdentityRule{
			{ID: "first", Target: TargetNZBDownloads, Profile: "sabnzbd"},
			{ID: "second", Target: TargetNZBDownloads, Profile: "nzbget"},
		},
	}
	if id := tied.ResolveRequest(IdentityRequest{Kind: RequestNZBDownload, URL: "https://any.test/x.nzb"}); id.RuleID != "first" || id.Profile != "sabnzbd" {
		t.Fatalf("order tie-break: got rule=%q profile=%q", id.RuleID, id.Profile)
	}
	// Implicit last rule is honest Ether with the operator docs link.
	empty := (ClientIdentity{}).ResolveRequest(IdentityRequest{Kind: RequestSearch, URL: "https://any.test/api"})
	if empty.Profile != "ether" || empty.RuleID != "" || empty.RuleIndex != -1 || !strings.Contains(empty.UserAgent, "/docs/indexer-operators.md") {
		t.Fatalf("implicit default: %+v", empty)
	}
}

func TestIdentityNoQueryMimicry(t *testing.T) {
	// Compatibility profiles only set User-Agent, Accept, Accept-Encoding, Connection —
	// never traceparent, never param reordering, never forced offset=0, season=00, or year-in-q.
	allowed := map[string]bool{
		"user-agent":      true,
		"accept":          true,
		"accept-encoding": true,
		"connection":      true,
	}
	for _, prof := range ClientProfiles() {
		for _, h := range prof.Headers {
			if !allowed[strings.ToLower(h.Name)] {
				t.Fatalf("profile %q sets unexpected header %q", prof.ID, h.Name)
			}
			if strings.EqualFold(h.Name, "traceparent") {
				t.Fatalf("profile %q still sets traceparent", prof.ID)
			}
		}
	}

	policy := ClientIdentity{
		Rules: []IdentityRule{{ID: "r1", Target: TargetAllSearches, Profile: "prowlarr"}},
	}
	id := policy.ResolveRequest(IdentityRequest{Kind: RequestSearch, URL: "https://ix.example/api"})
	params := url.Values{
		"t": {"tvsearch"}, "q": {"Show"}, "year": {"2024"}, "season": {"0"},
		"ep": {"1"}, "cat": {"5000"}, "extended": {"1"}, "apikey": {"K"}, "limit": {"100"},
	}
	got := id.ApplyParams(params)
	want := params.Encode()
	if got != want {
		t.Fatalf("prowlarr query reshaped:\ngot  %s\nwant %s", got, want)
	}

	// Parameter edits cannot remove apikey or t, and are rejected by validation.
	withProtected := ClientIdentity{
		Rules: []IdentityRule{{
			ID:      "r2",
			Target:  TargetAllSearches,
			Profile: "ether",
			Params: []KVOverride{
				{Name: "apikey", Remove: true},
				{Name: "t", Remove: true},
				{Name: "extended", Remove: true},
				{Name: "custom", Value: "1"},
			},
		}},
	}
	if errs := withProtected.Validate(); len(errs) < 2 {
		t.Fatalf("expected validation errors for removing apikey and t, got %+v", errs)
	}
	resolved := withProtected.ResolveRequest(IdentityRequest{Kind: RequestSearch, URL: "https://ix.example/api"})
	q := resolved.ApplyParams(url.Values{"t": {"search"}, "apikey": {"K"}, "extended": {"1"}, "cat": {"5000"}})
	if q != "apikey=K&cat=5000&custom=1&t=search" {
		t.Fatalf("protected params not preserved: %s", q)
	}
}

func TestIdentityMigrationWithoutLoss(t *testing.T) {
	dir := t.TempDir()
	raw := `{
  "clientIdentity": {
    "userAgent": "Radarr/6.4.4.10685 (ubuntu 26.04)",
    "headers": { "X-Owner": "kept" }
  },
  "indexers": [
    {
      "id": "ix-split",
      "name": "Split W175",
      "url": "https://split.example.com",
      "apiKey": "k1",
      "enabled": true,
      "requestIdentity": {
        "profile": "prowlarr",
        "headers": [{ "name": "X-Search", "value": "yes" }],
        "params": [{ "name": "limit", "value": "50" }],
        "grabProfile": "nzbget",
        "grabHeaders": [{ "name": "X-Grab", "value": "1" }],
        "grabParams": [{ "name": "dl", "value": "1" }]
      }
    },
    {
      "id": "ix-simple",
      "name": "Simple W175",
      "url": "https://simple.example.com",
      "apiKey": "k2",
      "enabled": true,
      "requestIdentity": { "profile": "sonarr" }
    },
    {
      "id": "ix-ua-known",
      "name": "Legacy SAB",
      "url": "https://sab.example.com",
      "apiKey": "k3",
      "enabled": true,
      "userAgent": "SABnzbd/5.1.3"
    },
    {
      "id": "ix-ua-custom",
      "name": "Legacy Custom",
      "url": "https://custom.example.com",
      "apiKey": "k4",
      "enabled": true,
      "userAgent": "MyCustomApp/1.0"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	cfg := store.Get()
	if errs := cfg.ClientIdentity.Validate(); len(errs) > 0 {
		t.Fatalf("migrated rules failed validation: %+v", errs)
	}

	// Split W175 indexer keeps distinct search and download identities, headers, and params.
	sID := cfg.ClientIdentity.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-split", URL: "https://split.example.com/api"})
	if sID.Profile != "prowlarr" || sID.Headers["X-Search"] != "yes" || len(sID.Params) != 1 || sID.Params[0].Name != "limit" {
		t.Fatalf("ix-split search migration lost data: %+v", sID)
	}
	gID := cfg.ClientIdentity.ResolveRequest(IdentityRequest{Kind: RequestNZBDownload, IndexerID: "ix-split", URL: "https://split.example.com/api"})
	if gID.Profile != "nzbget" || gID.Headers["X-Grab"] != "1" || len(gID.Params) != 1 || gID.Params[0].Name != "dl" {
		t.Fatalf("ix-split grab migration lost data: %+v", gID)
	}

	// Simple W175 indexer keeps sonarr profile.
	simpleID := cfg.ClientIdentity.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-simple", URL: "https://simple.example.com/api"})
	if simpleID.Profile != "sonarr" {
		t.Fatalf("ix-simple migration: %+v", simpleID)
	}

	// Legacy known UA becomes its profile; unknown UA becomes custom with exact User-Agent.
	sabID := cfg.ClientIdentity.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-ua-known", URL: "https://sab.example.com/api"})
	if sabID.Profile != "sabnzbd" || !strings.HasPrefix(sabID.UserAgent, "SABnzbd/") {
		t.Fatalf("ix-ua-known migration: %+v", sabID)
	}
	custID := cfg.ClientIdentity.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-ua-custom", URL: "https://custom.example.com/api"})
	if custID.Profile != "custom" || custID.UserAgent != "MyCustomApp/1.0" {
		t.Fatalf("ix-ua-custom migration: %+v", custID)
	}

	// Legacy global clientIdentity applies to unmatched indexers (both searches and downloads) with its header kept.
	otherSearch := cfg.ClientIdentity.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-other", URL: "https://other.example.com/api"})
	if otherSearch.Profile != "radarr" || otherSearch.Headers["X-Owner"] != "kept" {
		t.Fatalf("global search migration: %+v", otherSearch)
	}
	otherNZB := cfg.ClientIdentity.ResolveRequest(IdentityRequest{Kind: RequestNZBDownload, IndexerID: "ix-other", URL: "https://other.example.com/file.nzb"})
	if otherNZB.Profile != "radarr" || otherNZB.Headers["X-Owner"] != "kept" {
		t.Fatalf("global nzb migration: %+v", otherNZB)
	}

	// Persisted config.json has removed all legacy keys and is idempotent on reload.
	savedBytes, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	saved := string(savedBytes)
	for _, legacy := range []string{`"requestIdentity"`, `"userAgent"`} {
		if strings.Contains(saved, legacy) {
			t.Fatalf("legacy key %s still present after migration:\n%s", legacy, saved)
		}
	}
	reloaded, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	if len(reloaded.Get().ClientIdentity.Rules) != len(cfg.ClientIdentity.Rules) {
		t.Fatalf("reload duplicated rules: %d -> %d", len(cfg.ClientIdentity.Rules), len(reloaded.Get().ClientIdentity.Rules))
	}
}

func TestIdentityRuleMediaTypePrecedenceAndMigration(t *testing.T) {
	policy := ClientIdentity{
		Rules: []IdentityRule{
			{ID: "all-any", Target: TargetAllSearches, Profile: "prowlarr"},
			{ID: "all-movie", Target: TargetAllSearches, MediaType: IdentityMediaMovie, Profile: "radarr"},
			{ID: "all-tv", Target: TargetAllSearches, MediaType: IdentityMediaTV, Profile: "sonarr"},
			{ID: "ix-any", Target: TargetIndexer, IndexerID: "ix-1", Profile: "nzbhydra2"},
			{ID: "ix-movie", Target: TargetIndexer, IndexerID: "ix-2", MediaType: IdentityMediaMovie, Profile: "radarr"},
			{ID: "dl-movie", Target: TargetNZBDownloads, MediaType: IdentityMediaMovie, Profile: "radarr"},
			{ID: "dl-tv", Target: TargetNZBDownloads, MediaType: IdentityMediaTV, Profile: "sonarr"},
			{ID: "dl-music", Target: TargetNZBDownloads, MediaType: IdentityMediaMusic, Profile: "sabnzbd"},
			{ID: "dl-books", Target: TargetNZBDownloads, MediaType: IdentityMediaBooks, Profile: "nzbget"},
		},
	}
	if errs := policy.Validate(); len(errs) > 0 {
		t.Fatalf("valid mediaType rules rejected: %+v", errs)
	}

	// Specific mediaType beats Any within the same target tier.
	if id := policy.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-other", URL: "https://other.test/api", MediaType: IdentityMediaMovie}); id.RuleID != "all-movie" || id.Profile != "radarr" {
		t.Fatalf("movie search = %+v, want all-movie/radarr", id)
	}
	if id := policy.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-other", URL: "https://other.test/api", MediaType: IdentityMediaTV}); id.RuleID != "all-tv" || id.Profile != "sonarr" {
		t.Fatalf("tv search = %+v, want all-tv/sonarr", id)
	}
	// Unknown media type (e.g. caps) falls through media-scoped rules to Any.
	if id := policy.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-other", URL: "https://other.test/api"}); id.RuleID != "all-any" || id.Profile != "prowlarr" {
		t.Fatalf("unknown media search = %+v, want all-any/prowlarr", id)
	}
	// More specific target tier (specific indexer Any) still beats broader tier (all_searches Movie).
	if id := policy.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-1", URL: "https://ix1.test/api", MediaType: IdentityMediaMovie}); id.RuleID != "ix-any" || id.Profile != "nzbhydra2" {
		t.Fatalf("indexer Any should beat all_searches Movie: got %+v", id)
	}
	// Media-scoped indexer rule does NOT match caps or TV; falls back to all-tv / all-any.
	if id := policy.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-2", URL: "https://ix2.test/api", MediaType: IdentityMediaTV}); id.RuleID != "all-tv" {
		t.Fatalf("ix-2 TV should not match ix-movie: got %+v", id)
	}
	if id := policy.ResolveRequest(IdentityRequest{Kind: RequestSearch, IndexerID: "ix-2", URL: "https://ix2.test/api", MediaType: IdentityMediaMovie}); id.RuleID != "ix-movie" {
		t.Fatalf("ix-2 Movie should match ix-movie: got %+v", id)
	}
	// Downloads resolve Movies, TV, Music, and Books by mediaType.
	for _, tc := range []struct {
		mt      IdentityMediaType
		wantID  string
		wantPrf string
	}{
		{IdentityMediaMovie, "dl-movie", "radarr"},
		{IdentityMediaTV, "dl-tv", "sonarr"},
		{IdentityMediaMusic, "dl-music", "sabnzbd"},
		{IdentityMediaBooks, "dl-books", "nzbget"},
		{IdentityMediaAny, "", "ether"},
	} {
		if id := policy.ResolveRequest(IdentityRequest{Kind: RequestNZBDownload, IndexerID: "ix-other", URL: "https://other.test/dl.nzb", MediaType: tc.mt}); id.RuleID != tc.wantID || id.Profile != tc.wantPrf {
			t.Fatalf("download media=%q: got %+v, want %q/%q", tc.mt, id, tc.wantID, tc.wantPrf)
		}
	}

	// Unknown mediaType is rejected by Validate.
	bad := ClientIdentity{Rules: []IdentityRule{{ID: "bad", Target: TargetAllSearches, MediaType: "anime", Profile: "sonarr"}}}
	if errs := bad.Validate(); len(errs) == 0 {
		t.Fatal("expected validation error for unknown mediaType")
	}

	// Legacy pre-W175 tvSearch/movieSearch/tvGrab/movieGrab slots migrate without losing the Movie/TV split.
	dir := t.TempDir()
	raw := `{"clientIdentity":{"tvSearch":"sonarr","movieSearch":"radarr","tvGrab":"sonarr","movieGrab":"radarr"}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	migrated := store.Get().ClientIdentity
	if id := migrated.ResolveRequest(IdentityRequest{Kind: RequestSearch, URL: "https://ix.test/api", MediaType: IdentityMediaMovie}); id.Profile != "radarr" {
		t.Fatalf("migrated movie search = %+v, want radarr", id)
	}
	if id := migrated.ResolveRequest(IdentityRequest{Kind: RequestSearch, URL: "https://ix.test/api", MediaType: IdentityMediaTV}); id.Profile != "sonarr" {
		t.Fatalf("migrated tv search = %+v, want sonarr", id)
	}
	if id := migrated.ResolveRequest(IdentityRequest{Kind: RequestNZBDownload, URL: "https://ix.test/a.nzb", MediaType: IdentityMediaMovie}); id.Profile != "radarr" {
		t.Fatalf("migrated movie nzb = %+v, want radarr", id)
	}
	if id := migrated.ResolveRequest(IdentityRequest{Kind: RequestNZBDownload, URL: "https://ix.test/b.nzb", MediaType: IdentityMediaTV}); id.Profile != "sonarr" {
		t.Fatalf("migrated tv nzb = %+v, want sonarr", id)
	}
}
