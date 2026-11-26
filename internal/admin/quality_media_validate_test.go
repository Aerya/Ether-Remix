package admin

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func mediaConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Defaults(t.TempDir())
	cfg.Quality = config.Quality{
		Default: "Movies",
		Profiles: []core.QualityProfile{
			{Name: "Movies"},
			{Name: "Shows"},
		},
	}
	return cfg
}

func hasField(errs []FieldError, field string) bool {
	for _, e := range errs {
		if e.Field == field {
			return true
		}
	}
	return false
}

func TestValidateConfigProfileNamesAreUnique(t *testing.T) {
	cfg := mediaConfig(t)
	cfg.Quality.Profiles = append(cfg.Quality.Profiles, core.QualityProfile{Name: "Movies"})
	if errs := ValidateConfig(cfg); !hasField(errs, "quality.profiles[2].name") {
		t.Fatalf("duplicate name accepted: %v", errs)
	}
	cfg = mediaConfig(t)
	cfg.Quality.Rules = []config.QualityRule{{Devices: []config.DeviceMatch{{Type: "Roku"}}, Kind: config.KindEpisode, Profile: "Gone"}}
	if errs := ValidateConfig(cfg); !hasField(errs, "quality.rules[0].profile") {
		t.Fatalf("rule naming a missing profile accepted: %v", errs)
	}
}

func TestValidateConfigSyncIntervalDisabled(t *testing.T) {
	cfg := mediaConfig(t)
	cfg.Quality.Sync = config.QualitySync{Enabled: false}
	if errs := ValidateConfig(cfg); len(errs) != 0 {
		t.Fatalf("disabled sync with zero interval rejected: %v", errs)
	}
}

// Reproduces the v0.3.6 upgrade bug: both an install upgrading from <=0.3.5
// with custom/TRaSH rules and an install that already booted v0.3.6 with
// reset profiles + dangling rules[1..3] open into a valid Config that passes
// ValidateConfig on subsequent profile edits.
func TestValidateConfigAfterUpgradingDanglingQualityRules(t *testing.T) {
	for name, raw := range map[string]string{
		"from v0.3.5 custom profiles": `{"quality":{"default":"TRaSH UHD","rules":[` +
			`{"product":"Apple TV","profile":"4K"},` +
			`{"product":"Roku","profile":"TRaSH UHD","maxHeight":1080},` +
			`{"platform":"iOS","mediaType":"movie","profile":"TRaSH Remux"},` +
			`{"userId":7,"mediaType":"tv","profile":"TRaSH WEB"}` +
			`],"profiles":[{"name":"TRaSH UHD","mediaType":"movie","maxHeight":2160,"preferredSources":["remux"]}]}}`,
		"from wedged v0.3.6 install": `{"quality":{"default":"","rules":[` +
			`{"product":"Apple TV","profile":"4K"},` +
			`{"product":"Roku","profile":"TRaSH UHD","maxHeight":1080},` +
			`{"platform":"iOS","mediaType":"movie","profile":"TRaSH Remux"},` +
			`{"userId":7,"mediaType":"tv","profile":"TRaSH WEB"}` +
			`],"profiles":[` +
			`{"name":"4K","mediaType":"movie","maxHeight":2160,"minHeight":720},` +
			`{"name":"4K","mediaType":"tv","maxHeight":2160,"minHeight":720},` +
			`{"name":"1080p","mediaType":"movie","maxHeight":1080,"minHeight":480},` +
			`{"name":"1080p","mediaType":"tv","maxHeight":1080,"minHeight":480}` +
			`]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := config.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			cfg := store.Get()
			cfg.Quality.Profiles[0].MinHeight = 1080
			if errs := ValidateConfig(cfg); len(errs) != 0 {
				t.Fatalf("saving edited profile failed validation: %v", errs)
			}
		})
	}
}
