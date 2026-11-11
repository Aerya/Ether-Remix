package localmedia

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestScanReopenRangeConfinementAndUnavailableRoot(t *testing.T) {
	db := store.OpenTest(t)
	dir := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(dir, "holiday.mp4")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.mp4"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.mp4"), filepath.Join(dir, "escape.mp4")); err != nil {
		t.Fatal(err)
	}
	probes := 0
	l := &Library{DB: db, Probe: func(context.Context, *os.File, string) (File, error) { probes++; return File{Duration: 60}, nil }}
	ctx := t.Context()
	if err := l.Add(ctx, Root{Name: "Famille", Path: dir, Kind: "video"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := l.Scan(ctx); err != nil {
			t.Fatal(err)
		}
	}
	files, err := l.Files(ctx, "video")
	if err != nil || len(files) != 1 || probes != 1 {
		t.Fatalf("files=%+v probes=%d err=%v", files, probes, err)
	}
	f := files[0]
	if f.Name != "holiday" || !IsKey(core.LocalKey(f.ID)) {
		t.Fatal(f)
	}
	src, _, err := l.Open(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 3)
	if _, err = src.ReadAt(b, 4); err != nil || string(b) != "456" {
		t.Fatal(string(b), err)
	}
	src.Close()
	// Replacing the indexed file with an outside-root symlink cannot expose bytes.
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(outside, "secret.mp4"), path); err != nil {
		t.Fatal(err)
	}
	if f, _, err := l.Open(ctx, files[0].ID); err == nil {
		f.Close()
		t.Fatal("escaped root")
	}
	// A failed walk retains the previous index rather than deleting a mount's data.
	moved := dir + "-offline"
	if err = os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(moved, dir)
	if err = l.Scan(ctx); err == nil {
		t.Fatal("offline scan accepted")
	}
	files, err = l.Files(ctx, "video")
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	roots, _ := l.Roots(ctx)
	if err = l.Remove(ctx, roots[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = l.Open(ctx, files[0].ID); !errors.Is(err, core.ErrNotAvailable) {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(moved, "holiday.mp4")); err != nil {
		t.Fatal("source removal touched files", err)
	}
}
func TestCorruptMediaNotImported(t *testing.T) {
	db := store.OpenTest(t)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "bad.mp4"), []byte("broken"), 0600)
	l := &Library{DB: db, Probe: func(context.Context, *os.File, string) (File, error) { return File{}, errors.New("bad") }}
	if err := l.Add(t.Context(), Root{Name: "Local", Path: dir, Kind: "video"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	files, _ := l.Files(t.Context(), "video")
	if len(files) != 0 || l.Status().Errors != 1 {
		t.Fatal(files, l.Status())
	}
}

func TestRealProbeReadsConfinedAudioDescriptor(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Docker Linux descriptor probe")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+16000))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(8000), uint32(16000), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16000))
	b.Write(make([]byte, 16000))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Silence.wav"), b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	l := &Library{DB: store.OpenTest(t)}
	if err := l.Add(t.Context(), Root{Name: "Audio", Path: dir, Kind: "audio"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Scan(t.Context()); err != nil {
		t.Fatal(err)
	}
	ff, err := l.Files(t.Context(), "audio")
	if err != nil || len(ff) != 1 {
		t.Fatal(ff, err, l.Status())
	}
	f := ff[0]
	if f.Duration != 1 || f.SampleRate != 8000 || f.Channels != 1 || f.Codec != "pcm_s16le" || f.MIME != "audio/wav" {
		t.Fatalf("probe: %+v", f)
	}
}
