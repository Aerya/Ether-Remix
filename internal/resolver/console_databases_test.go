package resolver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type noopDatabasesIndexer struct{}

func (noopDatabasesIndexer) Name() string { return "noop" }
func (noopDatabasesIndexer) Search(context.Context, core.SearchRequest) ([]core.Release, error) {
	return nil, nil
}
func (noopDatabasesIndexer) FetchNZB(context.Context, core.Release) ([]byte, error) {
	return nil, nil
}

func openDatabasesTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.OpenPath(context.Background(), filepath.Join(t.TempDir(), "ether.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func buildTestDbTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for k := range files {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, name := range names {
		data := []byte(files[name])
		hdr := &tar.Header{
			Name:     "database-v2/" + name,
			Mode:     0o644,
			Size:     int64(len(data)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func baseDictionarryTestFiles() map[string]string {
	return map[string]string{
		"pcd.json": `{
			"name": "Dictionarry Database",
			"version": "2.0.0",
			"dependencies": {
				"https://github.com/Dictionarry-Hub/schema": "1.1.0"
			}
		}`,
		"ops/0.rosettarr.sql": `
			INSERT INTO tags (name) VALUES ('2160p'), ('1080p'), ('x265');
			INSERT INTO regular_expressions (name, pattern, description) VALUES
				('x265', '[xh][ ._-]?265|\bHEVC\b', 'HEVC/x265');
			INSERT INTO custom_formats (name, description) VALUES
				('x265', 'Matches x265/HEVC');
			INSERT INTO custom_format_conditions (custom_format_name, name, type, arr_type, negate, required) VALUES
				('x265', 'x265', 'release_title', 'all', 0, 1);
			INSERT INTO condition_patterns (custom_format_name, condition_name, regular_expression_name) VALUES
				('x265', 'x265', 'x265');

			INSERT INTO quality_profiles (name, description, upgrades_allowed, minimum_custom_format_score, upgrade_until_score, upgrade_score_increment) VALUES
				('2160p Remux', '2160p Remux profile', 1, 20000, 888888, 1),
				('1080p Efficient', '1080p Efficient profile', 1, 20000, 888888, 1);
			INSERT INTO quality_groups (quality_profile_name, name) VALUES
				('2160p Remux', '2160p Remux'),
				('1080p Efficient', '1080p Efficient');
			INSERT INTO quality_group_members (quality_profile_name, quality_group_name, quality_name, position) VALUES
				('2160p Remux', '2160p Remux', 'Remux-2160p', 0),
				('1080p Efficient', '1080p Efficient', 'Bluray-1080p', 0);
			INSERT INTO quality_profile_qualities (quality_profile_name, quality_group_name, position, enabled, upgrade_until) VALUES
				('2160p Remux', '2160p Remux', 0, 1, 1),
				('1080p Efficient', '1080p Efficient', 0, 1, 1);
			INSERT INTO quality_profile_custom_formats (quality_profile_name, custom_format_name, arr_type, score) VALUES
				('2160p Remux', 'x265', 'all', 500),
				('1080p Efficient', 'x265', 'all', 1000);
		`,
	}
}

func TestConsolePresetsAndCustomDatabases(t *testing.T) {
	officialTar := buildTestDbTarGz(t, baseDictionarryTestFiles())
	customFiles := baseDictionarryTestFiles()
	customFiles["ops/0.rosettarr.sql"] += `
		INSERT INTO quality_profiles (name, description, upgrades_allowed, minimum_custom_format_score, upgrade_until_score, upgrade_score_increment)
		VALUES ('1080p Anime Fork', 'Fork anime profile', 1, 100, 1000, 1);
		INSERT INTO quality_groups (quality_profile_name, name) VALUES ('1080p Anime Fork', '1080p Anime Fork');
		INSERT INTO quality_group_members (quality_profile_name, quality_group_name, quality_name, position)
		VALUES ('1080p Anime Fork', '1080p Anime Fork', 'Bluray-1080p', 0);
		INSERT INTO quality_profile_qualities (quality_profile_name, quality_group_name, position, enabled, upgrade_until)
		VALUES ('1080p Anime Fork', '1080p Anime Fork', 0, 1, 1);
		INSERT INTO quality_profile_custom_formats (quality_profile_name, custom_format_name, arr_type, score)
		VALUES ('1080p Anime Fork', 'x265', 'all', 750);
	`
	customTar := buildTestDbTarGz(t, customFiles)
	badTar := buildTestDbTarGz(t, map[string]string{"README.md": "not a database"})

	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := func(status int, b []byte) *http.Response {
			return &http.Response{
				StatusCode: status,
				Header:     http.Header{"Content-Type": []string{"application/gzip"}},
				Body:       io.NopCloser(bytes.NewReader(b)),
			}
		}
		switch r.URL.String() {
		case dictionarryDefaultArchiveURL:
			return body(http.StatusOK, officialTar), nil
		case "https://codeload.github.com/kaito/database/tar.gz/refs/heads/v2",
			"https://codeload.github.com/kaito/database/tar.gz/refs/heads/custom-branch",
			"https://downloads.example.com/kaito-db.tar.gz":
			return body(http.StatusOK, customTar), nil
		case "https://codeload.github.com/kaito/not-dictionarry/tar.gz/refs/heads/v2":
			return body(http.StatusOK, badTar), nil
		default:
			return body(http.StatusNotFound, []byte("not found")), nil
		}
	})

	cfg := config.Defaults(t.TempDir())
	cfg.Quality.Databases = []config.ProfileDatabase{
		{ID: "kaito-database", Name: "database", Repo: "https://github.com/kaito/database", Branch: "v2"},
	}
	svc := New(noopDatabasesIndexer{}, nil, openDatabasesTestDB(t), cfg, Options{})
	dict := NewDictionarry(DictionarryOptions{
		Client:    &http.Client{Transport: transport},
		UserAgent: "Ether/test",
	})
	syncer := NewQualitySyncer(QualitySyncDeps{
		Dictionarry: dict,
		Config:      func() config.Config { return cfg },
		Save: func(mutate func(*config.Config) error) error {
			next := cfg
			next.Quality.Profiles = append([]core.QualityProfile(nil), cfg.Quality.Profiles...)
			next.Resolver.CustomFormats = append([]core.CustomFormat(nil), cfg.Resolver.CustomFormats...)
			if err := mutate(&next); err != nil {
				return err
			}
			cfg = next
			svc.UpdateConfig(cfg)
			return nil
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return time.Date(2026, 11, 25, 1, 0, 0, 0, time.UTC) },
	})
	handler := svc.ConsoleHandler(ConsoleDeps{
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Dictionarry: dict,
		SaveConfig: func(mutate func(*config.Config) error) error {
			next := cfg
			next.Quality.Profiles = append([]core.QualityProfile(nil), cfg.Quality.Profiles...)
			next.Resolver.CustomFormats = append([]core.CustomFormat(nil), cfg.Resolver.CustomFormats...)
			if err := mutate(&next); err != nil {
				return err
			}
			cfg = next
			svc.UpdateConfig(cfg)
			return nil
		},
		SyncQuality: syncer.Sync,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// 1. GET /presets returns 4K, 1080p, 720p media-less with no Import field.
	t.Run("presets", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/presets")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		var out struct {
			Items []core.QualityProfile `json:"items"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if len(out.Items) != 3 || out.Items[0].Name != "4K" || out.Items[1].Name != "1080p" || out.Items[2].Name != "720p" {
			t.Fatalf("unexpected presets: %+v", out.Items)
		}
		for _, p := range out.Items {
			if p.Import != nil {
				t.Fatalf("preset %q must have nil Import: %+v", p.Name, p.Import)
			}
		}
	})

	// 2. POST /quality-databases/check always returns 200 with integer profiles count.
	t.Run("checkQualityDatabase", func(t *testing.T) {
		check := func(payload string) map[string]any {
			t.Helper()
			resp, err := http.Post(srv.URL+"/quality-databases/check", "application/json", strings.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected HTTP 200, got %d", resp.StatusCode)
			}
			var out map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			return out
		}

		// Normalises .git, trailing /, and /tree/<branch>
		okTree := check(`{"repo":"https://github.com/kaito/database.git/tree/custom-branch/"}`)
		if okTree["ok"] != true || okTree["repo"] != "https://github.com/kaito/database" || okTree["branch"] != "custom-branch" || okTree["id"] != "kaito-database" {
			t.Fatalf("unexpected tree check response: %+v", okTree)
		}
		count, isNum := okTree["profiles"].(float64)
		if !isNum || int(count) != 3 {
			t.Fatalf("expected integer profiles == 3, got %T (%v)", okTree["profiles"], okTree["profiles"])
		}

		// Accepts https .tar.gz URL
		okTar := check(`{"repo":"https://downloads.example.com/kaito-db.tar.gz"}`)
		if okTar["ok"] != true || int(okTar["profiles"].(float64)) != 3 {
			t.Fatalf("unexpected tarball check response: %+v", okTar)
		}

		// Rejects non-https or unsupported URLs with 200 {ok: false, error}
		for _, bad := range []string{
			`{"repo":"http://github.com/kaito/database"}`,
			`{"repo":"https://gitlab.com/kaito/database"}`,
			`{"repo":"https://github.com/kaito/not-dictionarry"}`,
			`{"repo":"https://github.com/missing/repo"}`,
		} {
			res := check(bad)
			if res["ok"] != false || strings.TrimSpace(res["error"].(string)) == "" {
				t.Fatalf("expected ok=false with error for %s, got %+v", bad, res)
			}
		}
	})

	// 3. GET /dictionarry/{app}/profiles?database=<id>
	t.Run("dictionarryProfilesByDatabase", func(t *testing.T) {
		getNames := func(query string, wantStatus int) []string {
			t.Helper()
			resp, err := http.Get(srv.URL + "/dictionarry/sonarr/profiles" + query)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, wantStatus)
			}
			if wantStatus != http.StatusOK {
				return nil
			}
			var out struct {
				Items []TrashProfile `json:"items"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, p := range out.Items {
				names = append(names, p.Name)
			}
			return names
		}

		official := getNames("", http.StatusOK)
		custom := getNames("?database=kaito-database", http.StatusOK)
		if len(custom) != len(official)+1 {
			t.Fatalf("expected custom DB to have 1 extra profile, official=%v custom=%v", official, custom)
		}
		getNames("?database=does-not-exist", http.StatusNotFound)
	})

	// 4. Import with database + sync:false keeps provenance (NoSync: true) and Sync skips it while syncing active DB profiles.
	t.Run("importCustomDatabaseAndNoSync", func(t *testing.T) {
		postImport := func(body string) {
			t.Helper()
			resp, err := http.Post(srv.URL+"/quality-import", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				raw, _ := io.ReadAll(resp.Body)
				t.Fatalf("import failed (%d): %s", resp.StatusCode, raw)
			}
		}

		postImport(`{"app":"sonarr","source":"dictionarry","database":"kaito-database","profile":"1080p-anime-fork","name":"Anime Synced","sync":true}`)
		postImport(`{"app":"sonarr","source":"dictionarry","database":"kaito-database","profile":"1080p-anime-fork","name":"Anime Frozen","sync":false}`)

		var syncedProf, frozenProf *core.QualityProfile
		for i := range cfg.Quality.Profiles {
			switch cfg.Quality.Profiles[i].Name {
			case "Anime Synced":
				syncedProf = &cfg.Quality.Profiles[i]
			case "Anime Frozen":
				frozenProf = &cfg.Quality.Profiles[i]
			}
		}
		if syncedProf == nil || syncedProf.Import == nil || syncedProf.Import.Database != "kaito-database" || syncedProf.Import.NoSync {
			t.Fatalf("unexpected synced profile import: %+v", syncedProf)
		}
		if frozenProf == nil || frozenProf.Import == nil || frozenProf.Import.Database != "kaito-database" || !frozenProf.Import.NoSync {
			t.Fatalf("frozen profile must retain Import provenance with NoSync=true: %+v", frozenProf)
		}
		if _, ok := syncedProf.CustomFormatScores["dictionarry-kaito-database-sonarr-x265"]; !ok {
			t.Fatalf("expected custom DB format ID to be scoped by database ID, got %+v", syncedProf.CustomFormatScores)
		}

		// Mutate both profiles' scores locally, then run Sync: only Anime Synced should be overwritten by sync!
		for i := range cfg.Quality.Profiles {
			if cfg.Quality.Profiles[i].Name == "Anime Synced" || cfg.Quality.Profiles[i].Name == "Anime Frozen" {
				cfg.Quality.Profiles[i].CustomFormatScores["dictionarry-kaito-database-sonarr-x265"] = 123
			}
		}
		rep, err := syncer.Sync(context.Background())
		if err != nil {
			t.Fatalf("sync failed: %v", err)
		}
		if rep.ProfilesUpdated != 1 {
			t.Fatalf("expected 1 profile updated (skipping NoSync), got %+v", rep)
		}
		for _, p := range cfg.Quality.Profiles {
			switch p.Name {
			case "Anime Synced":
				if got := p.CustomFormatScores["dictionarry-kaito-database-sonarr-x265"]; got != 750 {
					t.Fatalf("expected synced profile score restored to 750, got %d", got)
				}
			case "Anime Frozen":
				if got := p.CustomFormatScores["dictionarry-kaito-database-sonarr-x265"]; got != 123 {
					t.Fatalf("expected NoSync profile score to remain 123, got %d", got)
				}
				if p.Import == nil || !p.Import.NoSync {
					t.Fatalf("expected NoSync provenance preserved after sync: %+v", p.Import)
				}
			}
		}
	})
}
