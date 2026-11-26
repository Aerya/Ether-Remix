package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestOpenMigratesV0320ConfigIntoRulesMigration(t *testing.T) {
	old := `{"quality":{"default":"1080p","remote":"720p","rules":[{"product":"Apple TV","profile":"4K"}],"profiles":[{"name":"4K","mediaType":"movie","maxHeight":2160,"minHeight":1080},{"name":"4K","mediaType":"tv","maxHeight":2160,"minHeight":1080,"episodeMaxSizeMbPerMin":120},{"name":"1080p","mediaType":"movie","maxHeight":1080},{"name":"1080p","mediaType":"tv","maxHeight":1080,"minHeight":720},{"name":"720p","maxHeight":720}]},"transcode":{"remoteMaxBitrateKbps":8000}}`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Get()
	q := cfg.Quality
	// Identical 4K movie/TV twins merged, keeping TV episodeMaxSizeMbPerMin.
	p4k, ok := core.FindProfile(q.Profiles, "4K")
	if !ok || p4k.EpisodeMaxSizeMBPerMin != 120 {
		t.Fatalf("4K profile after twin merge = %+v (ok=%v)", p4k, ok)
	}
	// Differing 1080p twins stayed apart as "1080p" and "1080p (TV)".
	if _, ok := core.FindProfile(q.Profiles, "1080p (TV)"); !ok {
		t.Fatalf("expected differing TV twin to be kept as '1080p (TV)', profiles=%+v", q.Profiles)
	}
	if q.RulesMigration == nil {
		t.Fatal("expected RulesMigration hand-off for app startup")
	}
	if q.RulesMigration.Home[core.MediaMovie] != "1080p" || q.RulesMigration.Home[core.MediaTV] != "1080p (TV)" {
		t.Fatalf("home defaults = %+v", q.RulesMigration.Home)
	}
	if q.RulesMigration.Away[core.MediaMovie] != "720p" || q.RulesMigration.Away[core.MediaTV] != "720p" {
		t.Fatalf("away defaults = %+v", q.RulesMigration.Away)
	}
	if len(q.RulesMigration.Rules) != 1 || q.RulesMigration.Rules[0].Product != "Apple TV" || q.RulesMigration.Rules[0].Profile != "4K" {
		t.Fatalf("legacy rules = %+v", q.RulesMigration.Rules)
	}
	// RemoteMaxBitrateKbps is kept for App.migrateQualityRules to convert into a rule.
	if cfg.Transcode.RemoteMaxBitrateKbps != 8000 {
		t.Fatalf("RemoteMaxBitrateKbps = %d, want 8000", cfg.Transcode.RemoteMaxBitrateKbps)
	}
}

func TestAssignRuleIDsAndDeletionCleanup(t *testing.T) {
	q := Quality{
		Default: "4K",
		Rules: []QualityRule{
			{ID: "r_keep0001", Libraries: []int64{3, 4}, Profile: "1080p"},
			{Libraries: []int64{3}, Profile: "720p"},
			{Viewers: []int64{9}, MaxBitrateKbps: 6000},
			{Viewers: []int64{9, 10}, MaxBitrateKbps: 8000},
		},
	}
	AssignRuleIDs(&q)
	for i, r := range q.Rules {
		if !strings.HasPrefix(r.ID, "r_") || len(r.ID) != 10 {
			t.Fatalf("rule[%d].ID = %q, want r_ + 8 chars", i, r.ID)
		}
	}
	if q.Rules[0].ID != "r_keep0001" {
		t.Fatalf("existing ID changed: %q", q.Rules[0].ID)
	}

	removedLibs, changed := RemoveLibraryFromRules(&q, 3)
	if !changed || len(removedLibs) != 1 {
		t.Fatalf("RemoveLibraryFromRules(3) = %v, %v", removedLibs, changed)
	}
	if !reflect.DeepEqual(q.Rules[0].Libraries, []int64{4}) {
		t.Fatalf("remaining libraries = %v, want [4]", q.Rules[0].Libraries)
	}

	removedViewers, changed := RemoveViewerFromRules(&q, 9)
	if !changed || len(removedViewers) != 1 {
		t.Fatalf("RemoveViewerFromRules(9) = %v, %v", removedViewers, changed)
	}
	if len(q.Rules) != 2 || !reflect.DeepEqual(q.Rules[1].Viewers, []int64{10}) {
		t.Fatalf("rules after viewer delete = %+v", q.Rules)
	}

	if !ReplaceViewerInRules(&q, 10, 11) || !reflect.DeepEqual(q.Rules[1].Viewers, []int64{11}) {
		t.Fatalf("ReplaceViewerInRules failed: %+v", q.Rules)
	}
}

func TestNormalizeProfileDatabase(t *testing.T) {
	got, err := NormalizeProfileDatabase("https://github.com/Dictionarry-Hub/database.git/", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "dictionarry-hub-database" || got.Name != "database" || got.Repo != "https://github.com/Dictionarry-Hub/database" || got.Branch != "v2" {
		t.Fatalf("got %+v", got)
	}
	if got.ArchiveURL != "https://codeload.github.com/Dictionarry-Hub/database/tar.gz/refs/heads/v2" {
		t.Fatalf("archiveURL = %q", got.ArchiveURL)
	}

	tree, err := NormalizeProfileDatabase("https://github.com/acme/custom-db/tree/main", "")
	if err != nil {
		t.Fatal(err)
	}
	if tree.Branch != "main" || tree.Repo != "https://github.com/acme/custom-db" {
		t.Fatalf("tree = %+v", tree)
	}

	tgz, err := NormalizeProfileDatabase("https://example.com/profiles/custom.tar.gz", "")
	if err != nil || tgz.ArchiveURL != "https://example.com/profiles/custom.tar.gz" {
		t.Fatalf("tgz = %+v, %v", tgz, err)
	}

	if _, err := NormalizeProfileDatabase("http://github.com/a/b", ""); err == nil {
		t.Fatal("expected http URL to be rejected")
	}
}
