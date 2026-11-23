package importer

import (
	"compress/gzip"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/braedonsaunders/ether/internal/vault"
)

type capture struct {
	vault.Store
	got vault.PutRequest
}

func (c *capture) Put(_ context.Context, r vault.PutRequest) (vault.Key, error) {
	c.got = r
	return "key", nil
}

func TestSABBackupUsesHistoryWithoutExposingPassword(t *testing.T) {
	root := t.TempDir()
	admin := filepath.Join(root, "admin")
	backup := filepath.Join(admin, "backup")
	if err := os.MkdirAll(backup, 0700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(admin, "history1.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("CREATE TABLE history(nzb_name TEXT,name TEXT,status TEXT,completed INTEGER); INSERT INTO history VALUES('backup.nzb','Movie {{secret}}','Completed',1)")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(backup, "backup.nzb.gz"))
	if err != nil {
		t.Fatal(err)
	}
	z := gzip.NewWriter(f)
	if _, err = z.Write([]byte("<nzb/>")); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	s := new(capture)
	var complete bool
	err = Run(t.Context(), s, root, func() error { return nil }, func(ok, completed bool) { complete = ok && completed })
	if err != nil || !complete || s.got.Source != "import:sabnzbd" || s.got.Release.Name != "Movie" || string(s.got.NZB) != "<nzb/>" || !s.got.Private {
		t.Fatal("SAB import lost history, privacy, or payload", err)
	}
}
