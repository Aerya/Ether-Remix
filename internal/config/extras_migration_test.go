package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExtrasDefaultOnAndOneTimeMigration(t *testing.T) {
	t.Run("fresh install defaults on", func(t *testing.T) {
		dir := t.TempDir()
		d := Defaults(dir)
		if !d.Extras.Enabled || !d.Extras.DefaultOnMigrated {
			t.Fatalf("Defaults().Extras = %+v, want Enabled=true DefaultOnMigrated=true", d.Extras)
		}
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		got := s.Get().Extras
		if !got.Enabled || !got.DefaultOnMigrated {
			t.Fatalf("Open fresh Extras = %+v, want Enabled=true DefaultOnMigrated=true", got)
		}
	})

	t.Run("legacy disabled config migrates once and keeps later off", func(t *testing.T) {
		dir := t.TempDir()
		raw := []byte(`{"extras":{"enabled":false,"prewarmBytes":50331648}}`)
		if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}

		// First open on v0.3.19 switches extras on and records the migration.
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		got := s.Get().Extras
		if !got.Enabled || !got.DefaultOnMigrated {
			t.Fatalf("after migration Extras = %+v, want Enabled=true DefaultOnMigrated=true", got)
		}

		// Verify the migrated marker was written to disk.
		disk, err := os.ReadFile(filepath.Join(dir, "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		var doc map[string]any
		if err := json.Unmarshal(disk, &doc); err != nil {
			t.Fatal(err)
		}
		ex, _ := doc["extras"].(map[string]any)
		if ex["enabled"] != true || ex["defaultOnMigrated"] != true {
			t.Fatalf("persisted extras = %#v, want enabled=true defaultOnMigrated=true", ex)
		}

		// Owner turns trailers off deliberately; subsequent opens must keep them off.
		if err := s.Update(func(c *Config) error {
			c.Extras.Enabled = false
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		s2, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if s2.Get().Extras.Enabled {
			t.Fatal("deliberate off was overridden on restart")
		}
		if !s2.Get().Extras.DefaultOnMigrated {
			t.Fatal("defaultOnMigrated was lost across restart")
		}
	})

	t.Run("legacy enabled config records migration marker", func(t *testing.T) {
		dir := t.TempDir()
		raw := []byte(`{"extras":{"enabled":true}}`)
		if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !s.Get().Extras.Enabled || !s.Get().Extras.DefaultOnMigrated {
			t.Fatalf("Extras = %+v, want Enabled=true DefaultOnMigrated=true", s.Get().Extras)
		}
	})
}

func TestExtrasMigrationSharedCluster(t *testing.T) {
	dirA, dirB := clusterNodes(t)
	a, err := Open(dirA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(dirB)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a cluster database written by v0.3.18 with extras.enabled=false
	// and no defaultOnMigrated marker.
	legacyShared := `{"extras":{"enabled":false,"prewarmBytes":50331648}}`
	backend := &memShared{
		found: true,
		doc:   SharedDoc{Doc: legacyShared, Version: 1, KeyFingerprint: a.KeyFingerprint()},
	}

	if err := a.AttachShared(context.Background(), backend, SharedOptions{}); err != nil {
		t.Fatal(err)
	}
	if !a.Get().Extras.Enabled || !a.Get().Extras.DefaultOnMigrated {
		t.Fatalf("node A after attach Extras = %+v, want Enabled=true DefaultOnMigrated=true", a.Get().Extras)
	}
	if backend.saves != 1 {
		t.Fatalf("expected 1 migration save to shared backend, got %d", backend.saves)
	}

	// Deliberately turn extras off in the cluster; a second node attaching must keep it off.
	if err := a.Update(func(c *Config) error {
		c.Extras.Enabled = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	savesBeforeB := backend.saves
	if err := b.AttachShared(context.Background(), backend, SharedOptions{}); err != nil {
		t.Fatal(err)
	}
	if b.Get().Extras.Enabled {
		t.Fatal("node B re-enabled deliberately disabled extras")
	}
	if backend.saves != savesBeforeB {
		t.Fatalf("node B unexpectedly rewrote shared config (%d -> %d)", savesBeforeB, backend.saves)
	}
}
