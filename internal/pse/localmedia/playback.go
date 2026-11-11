package localmedia

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func IsKey(k core.RatingKey) bool {
	return int64(k) >= int64(core.LocalKey(VideoBase)) && int64(k) < int64(core.LocalKey(VideoBase+Span))
}
func FileID(k core.RatingKey) int64 { return int64(k) - int64(core.LocalKey(0)) }
func KnownSQL(col string) string {
	return "EXISTS (SELECT 1 FROM pse_local_files lf JOIN pse_local_roots lr ON lr.id=lf.root_id WHERE lf.kind='video' AND lf.title_key=" + col + ")"
}
func Known(ctx context.Context, db *sql.DB, key core.RatingKey) bool {
	if db == nil || !IsKey(key) {
		return false
	}
	var n int
	return db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pse_local_files WHERE id=? AND kind='video'", FileID(key)).Scan(&n) == nil && n > 0
}
func Resolve(ctx context.Context, db *sql.DB, req core.ResolveRequest) (*core.Resolution, error) {
	if req.Title == nil || !IsKey(req.Title.Key) {
		return nil, core.ErrNotAvailable
	}
	id := FileID(req.Title.Key)
	rid := "remix-local:" + strconv.FormatInt(id, 10)
	for _, bad := range req.Exclude {
		if bad == rid {
			return nil, core.ErrNotAvailable
		}
	}
	if req.Prefer != "" && req.Prefer != rid {
		return nil, core.ErrNotAvailable
	}
	f, meta, err := (&Library{DB: db}).Open(ctx, id)
	if err != nil {
		return nil, err
	}
	f.Close()
	payload, _ := json.Marshal(localPayload{Version: 1, ID: id, Size: meta.Size, Modified: meta.Modified})
	return &core.Resolution{NZB: payload, Title: req.Title.Key, Release: core.Release{ID: rid, Name: meta.Name, Size: meta.Size, Indexer: "Fichiers locaux"}, ResolvedAt: time.Now()}, nil
}

// The playback core calls every transport envelope NZB. This contains no path.
type localPayload struct {
	Version            int
	ID, Size, Modified int64
}

type Engine struct {
	core.Engine
	Library *Library
}

func (e Engine) Open(ctx context.Context, id string, payload []byte) (core.MediaSource, error) {
	if !strings.HasPrefix(id, "remix-local:") {
		return e.Engine.Open(ctx, id, payload)
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(id, "remix-local:"), 10, 64)
	if err != nil || n < VideoBase || n >= VideoBase+Span {
		return nil, core.ErrNotAvailable
	}
	var binding localPayload
	if json.Unmarshal(payload, &binding) != nil || binding.Version != 1 || binding.ID != n {
		return nil, core.ErrNotAvailable
	}
	f, meta, err := e.Library.Open(ctx, n)
	if err != nil {
		return nil, err
	}
	if binding.Size != meta.Size || binding.Modified != meta.Modified {
		f.Close()
		return nil, core.ErrNotAvailable
	}
	return &Source{File: f, Meta: meta, ID: id}, nil
}
func (e Engine) Prefetch(ctx context.Context, id string, b []byte, offset, length int64) error {
	if strings.HasPrefix(id, "remix-local:") {
		return nil
	}
	return e.Engine.Prefetch(ctx, id, b, offset, length)
}

type Source struct {
	File *os.File
	Meta File
	ID   string
}

func (s *Source) ReadAt(p []byte, off int64) (int, error) { return s.File.ReadAt(p, off) }
func (s *Source) Size() int64                             { return s.Meta.Size }
func (s *Source) Name() string                            { return filepath.Base(s.Meta.Path) }
func (s *Source) ReleaseID() string                       { return s.ID }
func (s *Source) LocalFile() *os.File                     { return s.File }
func (s *Source) Close() error                            { return s.File.Close() }
