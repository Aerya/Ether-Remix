package localmedia

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Health is an explicit administrator diagnostic, never a player response.
// Sizes are logical media sizes, not free disk space (hardlinks may share blocks).
type Health struct {
	Checked int64        `json:"checked"`
	Roots   []RootHealth `json:"roots"`
}
type RootHealth struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Kind       string       `json:"kind"`
	State      string       `json:"state"`
	Bytes      int64        `json:"bytes"`
	KnownBytes int64        `json:"knownBytes"`
	Files      []FileHealth `json:"files"`
}
type FileHealth struct {
	ID           int64  `json:"id"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	State        string `json:"state"`
	Size         int64  `json:"size"`
	Unidentified bool   `json:"unidentified"`
	Duplicate    bool   `json:"duplicate"`
}

func healthState(err error) string {
	switch {
	case err == nil:
		return "available"
	case errors.Is(err, fs.ErrPermission):
		return "permission"
	case errors.Is(err, fs.ErrNotExist):
		return "missing"
	default:
		return "inaccessible"
	}
}
func (l *Library) Health(ctx context.Context) (Health, error) {
	result := Health{Checked: time.Now().Unix(), Roots: []RootHealth{}}
	roots, err := l.Roots(ctx)
	if err != nil {
		return result, err
	}
	byRoot := map[string][]File{}
	for _, kind := range []string{"video", "audio"} {
		files, err := l.Files(ctx, kind)
		if err != nil {
			return result, err
		}
		for _, f := range files {
			byRoot[f.RootID] = append(byRoot[f.RootID], f)
		}
	}
	for _, r := range roots {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		h := RootHealth{ID: r.ID, Name: r.Name, Kind: r.Kind, Files: []FileHealth{}}
		root, err := os.OpenRoot(r.Path)
		if err == nil {
			dir, e := root.Open(".")
			if e == nil {
				_, e = dir.ReadDir(1)
				dir.Close()
				if errors.Is(e, io.EOF) {
					e = nil
				}
			}
			err = e
		}
		h.State = healthState(err)
		// Candidates only: editions and identical names do not prove identical content.
		groups := map[string][]int{}
		for _, f := range byRoot[r.ID] {
			if err := ctx.Err(); err != nil {
				if root != nil {
					root.Close()
				}
				return result, err
			}
			fh := FileHealth{ID: f.ID, Path: f.Path, Name: f.Name, Size: f.Size, State: "unavailable", Unidentified: f.Kind == "video" && f.TitleKey == 0}
			h.KnownBytes += f.Size
			if h.State == "available" {
				st, e := root.Stat(f.Path)
				if e == nil && !st.Mode().IsRegular() {
					e = fs.ErrInvalid
				}
				var file *os.File
				if e == nil {
					file, e = root.Open(f.Path)
				}
				fh.State = healthState(e)
				if e == nil {
					st, e := file.Stat()
					file.Close()
					fh.State = healthState(e)
					if e == nil {
						if !st.Mode().IsRegular() {
							fh.State = "inaccessible"
						} else {
							fh.Size = st.Size()
							h.Bytes += st.Size()
							if st.Size() != f.Size || st.ModTime().UnixNano() != f.Modified {
								fh.State = "changed"
							}
						}
					}
				}
			}
			h.Files = append(h.Files, fh)
			if fh.State == "available" {
				key := strings.ToLower(filepath.Base(f.Path))
				// A matched title is stronger evidence than a filename, but remains a candidate.
				if f.TitleKey != 0 {
					key = f.TitleKey.String()
				}
				groups[key] = append(groups[key], len(h.Files)-1)
			}
		}
		for _, indices := range groups {
			if len(indices) > 1 {
				for _, i := range indices {
					h.Files[i].Duplicate = true
				}
			}
		}
		if root != nil {
			root.Close()
		}
		result.Roots = append(result.Roots, h)
	}
	return result, nil
}
