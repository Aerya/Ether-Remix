package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

// Pruning snapshots must preserve shared pack bytes, and an exported snapshot
// must restore without access to any server-local companion.
func TestIncrementalVaultSnapshot(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conf := filepath.Join(root, "config.json")
	if err = os.WriteFile(conf, []byte(`{"server":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "backups")
	s := New(db, conf, dir, 2)
	name := "202611/0123456789abcdef0123456789abcdef.pack"
	payload := []byte("private record for encrypted export")
	src := filepath.Join(root, packPrefix, name)
	if err = os.MkdirAll(filepath.Dir(src), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(src, payload, 0600); err != nil {
		t.Fatal(err)
	}
	first, err := s.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	companion := func(sn Snapshot) string { return filepath.Join(dir, sn.Name+".packs", name) }
	a, err := os.Stat(companion(first))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(companion(second))
	if err != nil || !os.SameFile(a, b) {
		t.Fatal("sealed pack was not reused", err)
	}
	raw, err := os.ReadFile(companion(second))
	if err != nil || bytes.Contains(raw, payload) {
		t.Fatal("unprotected companion", err)
	}
	f, err := os.Open(filepath.Join(dir, second.Name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var bundle bytes.Buffer
	if err = exportSnapshot(f, &bundle); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "restored")
	if err = RestoreBundle(ctx, &bundle, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, packPrefix, name))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("missing restored payload", err)
	}
	if err = os.Remove(companion(second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.StageSnapshot(ctx, second.Name); err != nil {
		t.Fatal("unavailable pack prevented DB restore", err)
	}
}
