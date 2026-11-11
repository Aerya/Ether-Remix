// Package localmedia indexes explicitly shared, read-only media folders.
// No client-supplied path is ever opened during playback.
package localmedia

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

const VideoBase int64 = 3_000_000_000_000
const AudioBase int64 = 4_000_000_000_000
const Span int64 = 1_000_000_000_000

func init() {
	store.Register("pse_local_media", 1, `
CREATE TABLE pse_local_roots(id TEXT PRIMARY KEY, name TEXT NOT NULL, path TEXT NOT NULL UNIQUE, kind TEXT NOT NULL);
CREATE TABLE pse_local_files(id INTEGER PRIMARY KEY, root_id TEXT NOT NULL, path TEXT NOT NULL, kind TEXT NOT NULL, size INTEGER NOT NULL, modified INTEGER NOT NULL, data TEXT NOT NULL, title_key INTEGER NOT NULL DEFAULT 0, UNIQUE(root_id,path));
CREATE INDEX pse_local_files_root ON pse_local_files(root_id);
CREATE INDEX pse_local_files_key ON pse_local_files(title_key);
`)
}

type Root struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
}
type File struct {
	ID         int64  `json:"id"`
	RootID     string `json:"rootId"`
	Path       string `json:"-"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Album      string `json:"album,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Duration   int    `json:"duration"`
	Number     int    `json:"number,omitempty"`
	Disc       int    `json:"disc,omitempty"`
	Size       int64  `json:"size"`
	Modified   int64  `json:"modified"`
	MIME       string `json:"mime"`
	Container  string `json:"container,omitempty"`
	Codec      string `json:"codec,omitempty"`
	Channels   int    `json:"channels,omitempty"`
	SampleRate int    `json:"sampleRate,omitempty"`
}
type Status struct {
	Running   bool   `json:"running"`
	Scanned   int    `json:"scanned"`
	Imported  int    `json:"imported"`
	Errors    int    `json:"errors"`
	LastError string `json:"lastError,omitempty"`
	Finished  int64  `json:"finished"`
}
type Library struct {
	DB      *sql.DB
	Publish func(context.Context, Root, []File) error
	// Probe can be substituted by deterministic fixture metadata in tests.
	Probe  func(context.Context, *os.File, string) (File, error)
	opMu   sync.Mutex
	mu     sync.Mutex
	status Status
}

func ID(namespace, value string, base int64) int64 {
	h := sha256.Sum256([]byte(namespace + "\x00" + value))
	return base + int64(binary.BigEndian.Uint64(h[:8])%uint64(Span))
}
func (l *Library) Roots(ctx context.Context) ([]Root, error) {
	rows, err := l.DB.QueryContext(ctx, "SELECT id,name,path,kind FROM pse_local_roots ORDER BY name,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Root{}
	for rows.Next() {
		var r Root
		if err = rows.Scan(&r.ID, &r.Name, &r.Path, &r.Kind); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (l *Library) Add(ctx context.Context, r Root) error {
	l.opMu.Lock()
	defer l.opMu.Unlock()
	r.Name = strings.TrimSpace(r.Name)
	r.Path = filepath.Clean(strings.TrimSpace(r.Path))
	if !filepath.IsAbs(r.Path) || r.Path == "/" || len(r.Name) == 0 || len([]rune(r.Name)) > 60 || (r.Kind != "video" && r.Kind != "audio") {
		return errors.New("nom, dossier absolu et type vidéo/audio requis")
	}
	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return errors.New("dossier inaccessible depuis le conteneur")
	}
	root.Close()
	r.ID = strconv.FormatInt(ID("root", r.Path, 0), 10)
	var sameName int
	if err := l.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM pse_local_roots WHERE name=? AND id<>?", r.Name, r.ID).Scan(&sameName); err != nil {
		return err
	}
	if sameName > 0 {
		return errors.New("choisissez un nom distinct pour chaque source locale")
	}
	var oldKind string
	if l.DB.QueryRowContext(ctx, "SELECT kind FROM pse_local_roots WHERE id=?", r.ID).Scan(&oldKind) == nil && oldKind != r.Kind {
		return errors.New("retirez la source avant de changer son type")
	}
	_, err = l.DB.ExecContext(ctx, "INSERT INTO pse_local_roots(id,name,path,kind) VALUES(?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,kind=excluded.kind", r.ID, r.Name, r.Path, r.Kind)
	return err
}
func (l *Library) Remove(ctx context.Context, id string) error {
	l.opMu.Lock()
	defer l.opMu.Unlock()
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM pse_local_files WHERE root_id=?", id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM pse_local_roots WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
func (l *Library) Status() Status { l.mu.Lock(); defer l.mu.Unlock(); return l.status }
func (l *Library) Scan(ctx context.Context) error {
	l.mu.Lock()
	if l.status.Running {
		l.mu.Unlock()
		return errors.New("analyse déjà en cours")
	}
	l.status = Status{Running: true}
	l.mu.Unlock()
	defer func() { l.mu.Lock(); l.status.Running = false; l.status.Finished = time.Now().Unix(); l.mu.Unlock() }()
	roots, err := l.Roots(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, r := range roots {
		if err = l.scanRoot(ctx, r); err != nil {
			failures = append(failures, err)
			l.mu.Lock()
			l.status.Errors++
			l.status.LastError = r.Name + " : " + err.Error()
			l.mu.Unlock()
		}
	}
	return errors.Join(failures...)
}
func (l *Library) scanRoot(ctx context.Context, r Root) error {
	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return errors.New("dossier inaccessible ; catalogue précédent conservé")
	}
	defer root.Close()
	files := []File{}
	err = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if mediaKind(ext) != r.Kind {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return nil
		}
		l.mu.Lock()
		l.status.Scanned++
		l.mu.Unlock()
		base := VideoBase
		if r.Kind == "audio" {
			base = AudioBase
		}
		id := ID(r.ID, path, base)
		var raw string
		var f File
		if l.DB.QueryRowContext(ctx, "SELECT data FROM pse_local_files WHERE id=? AND root_id=? AND path=? AND size=? AND modified=?", id, r.ID, path, info.Size(), info.ModTime().UnixNano()).Scan(&raw) == nil && json.Unmarshal([]byte(raw), &f) == nil {
			f.Path = path
			files = append(files, f)
			return nil
		}
		fh, err := root.Open(path)
		if err != nil {
			return err
		}
		probe := l.Probe
		if probe == nil {
			probe = probeFile
		}
		f, err = probe(ctx, fh, ext)
		fh.Close()
		if err != nil {
			l.mu.Lock()
			l.status.Errors++
			l.status.LastError = "Fichier illisible : " + filepath.Base(path)
			l.mu.Unlock()
			return nil
		}
		f.ID, f.RootID, f.Path, f.Kind, f.Size, f.Modified = id, r.ID, path, r.Kind, info.Size(), info.ModTime().UnixNano()
		if f.Name == "" {
			f.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		if f.Album == "" {
			f.Album = filepath.Base(filepath.Dir(path))
			if f.Album == "." {
				f.Album = r.Name
			}
		}
		if f.Artist == "" {
			f.Artist = "Artiste inconnu"
		}
		files = append(files, f)
		return nil
	})
	if err != nil {
		return errors.New("analyse interrompue ; catalogue précédent conservé")
	}
	l.opMu.Lock()
	defer l.opMu.Unlock()
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Verify the root still exists: removing a source during a scan cannot recreate it.
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM pse_local_roots WHERE id=? AND path=? AND kind=?", r.ID, r.Path, r.Kind).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM pse_local_files WHERE root_id=?", r.ID); err != nil {
		return err
	}
	for _, f := range files {
		raw, _ := json.Marshal(f) // Relative path is stored separately, never sent to players.
		var key core.RatingKey
		if f.Kind == "video" {
			key = core.LocalKey(f.ID)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO pse_local_files(id,root_id,path,kind,size,modified,data,title_key) VALUES(?,?,?,?,?,?,?,?)", f.ID, r.ID, f.Path, f.Kind, f.Size, f.Modified, string(raw), key); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	l.mu.Lock()
	l.status.Imported += len(files)
	l.mu.Unlock()
	if l.Publish != nil {
		return l.Publish(ctx, r, files)
	}
	return nil
}
func (l *Library) Files(ctx context.Context, kind string) ([]File, error) {
	rows, err := l.DB.QueryContext(ctx, "SELECT data,path FROM pse_local_files WHERE kind=? ORDER BY id", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		var raw, path string
		if err = rows.Scan(&raw, &path); err != nil {
			return nil, err
		}
		var f File
		if err = json.Unmarshal([]byte(raw), &f); err != nil {
			return nil, err
		}
		f.Path = path
		out = append(out, f)
	}
	return out, rows.Err()
}
func (l *Library) Open(ctx context.Context, id int64) (*os.File, File, error) {
	var path, dir, raw string
	err := l.DB.QueryRowContext(ctx, "SELECT f.path,r.path,f.data FROM pse_local_files f JOIN pse_local_roots r ON r.id=f.root_id WHERE f.id=?", id).Scan(&path, &dir, &raw)
	if err != nil {
		return nil, File{}, core.ErrNotAvailable
	}
	var meta File
	if json.Unmarshal([]byte(raw), &meta) != nil {
		return nil, meta, core.ErrNotAvailable
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, meta, core.ErrNotAvailable
	}
	defer root.Close()
	f, err := root.Open(path)
	if err != nil {
		return nil, meta, core.ErrNotAvailable
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() != meta.Size || st.ModTime().UnixNano() != meta.Modified {
		f.Close()
		return nil, meta, core.ErrNotAvailable
	}
	return f, meta, nil
}
func mediaKind(ext string) string {
	switch ext {
	case ".mkv", ".mp4", ".m4v", ".avi", ".mov", ".webm", ".ts", ".m2ts", ".mpeg", ".mpg", ".wmv", ".flv", ".ogv", ".vob", ".3gp":
		return "video"
	case ".mp3", ".flac", ".m4a", ".aac", ".ogg", ".opus", ".wav", ".aiff", ".aif", ".ape", ".wma", ".alac":
		return "audio"
	}
	return ""
}
func probeFile(ctx context.Context, f *os.File, ext string) (File, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// ffprobe reads the already confined descriptor, not a path that can be swapped.
	probe := "ffprobe"
	if exe, e := os.Executable(); e == nil {
		p := filepath.Join(filepath.Dir(exe), "ffprobe")
		if st, e := os.Stat(p); e == nil && st.Mode().IsRegular() {
			probe = p
		}
	}
	cmd := exec.CommandContext(ctx, probe, "-v", "error", "-show_format", "-show_streams", "-of", "json", "/proc/self/fd/3")
	cmd.ExtraFiles = []*os.File{f}
	var output probeOutput
	cmd.Stdout = &output
	err := cmd.Run()
	out := output.Bytes()
	if err != nil {
		return File{}, errors.New("ffprobe n’a pas pu lire le média")
	}
	var p struct {
		Format struct {
			Duration string
			Tags     map[string]string
		}
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			Channels   int    `json:"channels"`
			SampleRate string `json:"sample_rate"`
		}
	}
	if err = json.Unmarshal(out, &p); err != nil {
		return File{}, err
	}
	valid := false
	codec := ""
	channels, sampleRate := 0, 0
	kind := mediaKind(ext)
	for _, s := range p.Streams {
		if s.CodecType == kind {
			valid = true
			if s.CodecType == "audio" {
				codec = s.CodecName
				channels = s.Channels
				sampleRate, _ = strconv.Atoi(s.SampleRate)
			}
		}
	}
	if !valid {
		return File{}, fmt.Errorf("aucune piste %s", kind)
	}
	tags := map[string]string{}
	for k, v := range p.Format.Tags {
		tags[strings.ToLower(k)] = v
	}
	d, _ := strconv.ParseFloat(p.Format.Duration, 64)
	track, _ := strconv.Atoi(strings.Split(tags["track"], "/")[0])
	disc, _ := strconv.Atoi(strings.Split(tags["disc"], "/")[0])
	mime := "audio/" + strings.TrimPrefix(ext, ".")
	switch ext {
	case ".mp3":
		mime = "audio/mpeg"
	case ".m4a", ".alac":
		mime = "audio/mp4"
	case ".opus", ".ogg":
		mime = "audio/ogg"
	case ".mkv":
		mime = "video/x-matroska"
	case ".mp4", ".m4v":
		mime = "video/mp4"
	}
	container := strings.TrimPrefix(ext, ".")
	if container == "alac" {
		container = "m4a"
	}
	return File{Container: container, Codec: codec, Channels: channels, SampleRate: sampleRate, Name: tags["title"], Artist: tags["artist"], Album: tags["album"], Duration: int(d), Number: track, Disc: disc, MIME: mime}, nil
}

// Limit untrusted metadata emitted by ffprobe.
type probeOutput struct{ bytes.Buffer }

func (b *probeOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4<<20 {
		return 0, errors.New("métadonnées trop volumineuses")
	}
	return b.Buffer.Write(p)
}
