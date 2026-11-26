package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// An install upgraded from a release before the Radarr/Sonarr-standard
// ranking starts on exactly the profiles a fresh install has, repoints any
// client rules that named removed custom or imported profiles while keeping
// their matchers and limits, and keeps a standard profile the owner made.
func TestPreStandardProfilesResetToDefaults(t *testing.T) {
	open := func(doc string) Config {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		st, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		return st.Get()
	}
	old := open(`{"resolver":{"customFormatScores":{"x":5}},"quality":{"default":"Mine","rules":[{"product":"Apple TV","profile":"4K"},{"product":"Roku","profile":"Mine","maxHeight":1080,"convertSurroundAudio":true},{"platform":"iOS","mediaType":"movie","profile":"TRaSH UHD"},{"userId":9,"mediaType":"tv","profile":"TRaSH HD"}],"profiles":[{"name":"Mine","mediaType":"movie","maxHeight":1080,"minHeight":480,"preferredSources":["webdl"],"preferredCodecs":["h264"],"preferredAudio":["aac"],"ranking":["resolution"]}]}}`)
	if want := DefaultQuality().Profiles; !reflect.DeepEqual(old.Quality.Profiles, want) {
		t.Fatalf("profiles %+v, want the defaults", old.Quality.Profiles)
	}
	if old.Quality.Default == "Mine" {
		t.Fatal("default still names the removed profile")
	}
	wantRules := []LegacyQualityRule{
		{Product: "Apple TV", Profile: "4K"},
		{Product: "Roku", Profile: "Mine", MaxHeight: 1080, ConvertSurroundAudio: true},
		{Platform: "iOS", MediaType: "movie", Profile: "TRaSH UHD"},
		{UserID: 9, MediaType: "tv", Profile: "TRaSH HD"},
	}
	if old.Quality.RulesMigration == nil || !reflect.DeepEqual(old.Quality.RulesMigration.Rules, wantRules) {
		t.Fatalf("migrated rules %+v, want %+v", old.Quality.RulesMigration, wantRules)
	}

	shared, _, err := mergeShared(Defaults(t.TempDir()), []byte(`{"quality":{"rules":[{"product":"Shield","profile":"Mine","maxHeight":2160}],"profiles":[{"name":"Mine","mediaType":"tv","preferredSources":["webdl"]}]}}`))
	if err != nil || !reflect.DeepEqual(shared.Quality.Profiles, DefaultQuality().Profiles) {
		t.Fatalf("shared profiles %+v (%v), want the defaults", shared.Quality.Profiles, err)
	}
	if shared.Quality.RulesMigration == nil || len(shared.Quality.RulesMigration.Rules) != 1 || shared.Quality.RulesMigration.Rules[0].MaxHeight != 2160 {
		t.Fatalf("shared rulesMigration %+v, want Shield rule with maxHeight 2160", shared.Quality.RulesMigration)
	}

	b, _ := json.Marshal(DefaultQuality().Profiles[1])
	var mine map[string]any
	_ = json.Unmarshal(b, &mine)
	mine["name"] = "Mine"
	doc, _ := json.Marshal(map[string]any{"quality": map[string]any{"default": "Mine", "profiles": []any{mine}}})
	kept := open(string(doc))
	if len(kept.Quality.Profiles) != 1 || kept.Quality.Profiles[0].Name != "Mine" || kept.Quality.Default != "Mine" {
		t.Fatalf("standard profile not kept: %+v", kept.Quality)
	}
}
