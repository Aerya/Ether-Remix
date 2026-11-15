package transcode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// An absent Direct Stream preference uses the original-quality default;
// the transcode ladder remains independently enabled.
func TestAbrLadderUnderCopyOffWhenKeyAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"transcode":{"abrLadder":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()
	if !cfg.Transcode.ABRLadder || cfg.Transcode.ABRLadderUnderCopy {
		t.Fatalf("upgrade defaults: %+v", cfg.Transcode)
	}

	info := ladderInfo(2160, 40000)
	copyPrimary := core.Decision{
		Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, AudioStream: 1, Container: ContainerMPEGTS,
	}
	rungs := buildRungs(info, copyPrimary, adaptiveFromConfig(cfg.Transcode))
	if len(rungs) != 1 {
		t.Fatalf("default copy ladder = %d rungs (%+v), want 1", len(rungs), rungs)
	}

	on := cfg.Transcode
	on.ABRLadderUnderCopy = true
	if got := buildRungs(info, copyPrimary, adaptiveFromConfig(on)); len(got) != 5 {
		t.Fatalf("explicit under-copy on = %d rungs, want 5", len(got))
	}
}
