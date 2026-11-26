package transcode

import (
	"io"
	"os"
	"testing"
)

func TestLiveDVRBudgetEvictsOldestAcrossOutputs(t *testing.T) {
	cache := NewLiveDVRCache(t.TempDir(), func() int64 { return 8 })
	first, second := &LiveDVRStore{cache: cache}, &LiveDVRStore{cache: cache}
	t.Cleanup(func() {
		for _, store := range []*LiveDVRStore{first, second} {
			store.reap(true)
			encoderCleanup.dirs.Delete(store.dir)
		}
	})
	write := func(store *LiveDVRStore, seq int64) *LiveDVRFile {
		data := []byte("test")
		file := &LiveDVRFile{store: store, seq: seq, size: int64(len(data)), order: cache.serial.Add(1)}
		store.write(liveDVRWrite{file: file, data: data})
		return file
	}
	old := write(first, 7)
	other := write(second, 1)
	newest := write(first, 8)

	if cache.bytes != 8 || old.Ready() || first.Floor() != 8 || second.Floor() != 0 {
		t.Fatal("budget did not evict the oldest output prefix")
	}
	if _, err := os.Stat(old.path); !os.IsNotExist(err) {
		t.Fatal("evicted bytes remain on disk", err)
	}
	for _, saved := range []*LiveDVRFile{other, newest} {
		file, err := saved.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil || string(data) != "test" {
			t.Fatal("retained segment was unreadable", err)
		}
	}
}
