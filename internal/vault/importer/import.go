// Package importer reads owner-selected NZB backups without contacting an indexer.
package importer

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/vault"
	_ "modernc.org/sqlite"
)

const maxNZB = 256 << 20

type historyRow struct {
	name      string
	completed bool
}

func stem(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	for _, suffix := range []string{".queued", ".gz", ".nzb"} {
		if strings.HasSuffix(strings.ToLower(name), suffix) {
			name = name[:len(name)-len(suffix)]
		}
	}
	return name
}
func history(ctx context.Context, path string) (map[string]historyRow, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		// A Windows path (C:/…) must follow an empty authority: file:///C:/…
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT nzb_name,name,status FROM history ORDER BY completed")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]historyRow{}
	for rows.Next() {
		var file, name string
		var status sql.NullString
		if err := rows.Scan(&file, &name, &status); err != nil {
			return nil, err
		}
		row := historyRow{name, strings.EqualFold(status.String, "completed")}
		m[strings.ToLower(stem(file))] = row
		m[strings.ToLower(stem(name))] = row
	}
	return m, rows.Err()
}
func supported(name string) bool {
	n := strings.TrimSuffix(strings.ToLower(name), ".queued")
	return strings.HasSuffix(n, ".nzb") || strings.HasSuffix(n, ".nzb.gz")
}
func put(ctx context.Context, s vault.Store, kind, name string, h historyRow, r io.Reader, guard func() error) error {
	if strings.HasSuffix(strings.TrimSuffix(strings.ToLower(name), ".queued"), ".gz") {
		z, err := gzip.NewReader(r)
		if err != nil {
			return err
		}
		defer z.Close()
		r = z
	}
	data, err := io.ReadAll(io.LimitReader(r, maxNZB+1))
	if err != nil {
		return err
	}
	if len(data) > maxNZB {
		return errors.New("oversized NZB")
	}
	title := stem(name)
	if h.name != "" {
		title = stem(h.name)
	}
	// SAB allows an archive password in the backup filename, enclosed in braces.
	if at := strings.IndexByte(title, '{'); at >= 0 {
		title = strings.TrimSpace(title[:at])
	}
	if err := guard(); err != nil {
		return err
	}
	_, err = s.Put(ctx, vault.PutRequest{Release: core.Release{Name: title}, NZB: data, Source: "import:" + kind, Private: true})
	return err
}
func directory(path string) bool { i, e := os.Stat(path); return e == nil && i.IsDir() }

// Run visits one member at a time. Check is the current leader lease guard.
// Progress receives only counts via these outcome flags, never file contents.
func Run(ctx context.Context, s vault.Store, path string, check func() error, progress func(success, completed bool)) error {
	guard := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return check()
	}
	if err := guard(); err != nil {
		return err
	}
	if strings.EqualFold(filepath.Ext(path), ".zip") {
		z, err := zip.OpenReader(path)
		if err != nil {
			return err
		}
		defer z.Close()
		for _, f := range z.File {
			if err := guard(); err != nil {
				return err
			}
			if !f.Mode().IsRegular() || !supported(f.Name) {
				continue
			}
			reader, err := f.Open()
			if err == nil {
				err = put(ctx, s, "zip", f.Name, historyRow{}, reader, guard)
				reader.Close()
			}
			if guardErr := guard(); guardErr != nil {
				return guardErr
			}
			progress(err == nil, false)
		}
		return nil
	}
	roots := []string{path}
	kind := "folder"
	var meta map[string]historyRow
	backup := filepath.Join(path, "admin", "backup")
	if filepath.Base(path) == "admin" {
		backup = filepath.Join(path, "backup")
	}
	if filepath.Base(path) == "backup" && filepath.Base(filepath.Dir(path)) == "admin" {
		backup = path
	}
	if directory(backup) {
		kind = "sabnzbd"
		roots = []string{backup}
		var err error
		meta, err = history(ctx, filepath.Join(filepath.Dir(backup), "history1.db"))
		if err != nil {
			return err
		}
	} else if directory(filepath.Join(path, "nzb")) || directory(filepath.Join(path, "queue")) {
		kind = "nzbget"
		roots = nil
		for _, d := range []string{"nzb", "queue"} {
			p := filepath.Join(path, d)
			if directory(p) {
				roots = append(roots, p)
			}
		}
	} else if filepath.Base(path) == "nzb" || filepath.Base(path) == "queue" {
		kind = "nzbget"
	}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if err := guard(); err != nil {
				return err
			}
			if !d.Type().IsRegular() || !supported(d.Name()) {
				return nil
			}
			row := meta[strings.ToLower(stem(p))]
			f, err := os.Open(p)
			if err == nil {
				err = put(ctx, s, kind, p, row, f, guard)
				f.Close()
			}
			if guardErr := guard(); guardErr != nil {
				return guardErr
			}
			progress(err == nil, row.completed)
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
