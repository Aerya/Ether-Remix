package veladist

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/updater"
)

type File struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type inventory struct {
	SchemaVersion int    `json:"schemaVersion"`
	Version       string `json:"version"`
	Engine        string `json:"engine"`
	BasePath      string `json:"basePath"`
	Files         []File `json:"files"`
}
type Status struct {
	InstalledVersion  string `json:"installedVersion"`
	AvailableVersion  string `json:"availableVersion"`
	Installed         bool   `json:"installed"`
	RollbackAvailable bool   `json:"rollbackAvailable"`
	Busy              bool   `json:"busy"`
	Phase             string `json:"phase"`
	Downloaded        int64  `json:"downloaded"`
	Total             int64  `json:"total"`
	Error             string `json:"error,omitempty"`
}
type Manager struct {
	mu         sync.Mutex
	dir, ether string
	channel    func() string
	keys       []ed25519.PublicKey
	client     *http.Client
	state      Status
	release    *Release
}

func New(dir, ether string, channel func() string) *Manager {
	return &Manager{dir: dir, ether: ether, channel: channel, keys: pinnedKeys(), client: &http.Client{Timeout: 15 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 10 {
			return errors.New("unsafe release redirect")
		}
		return nil
	}}}
}
func (m *Manager) live() string { return filepath.Join(m.dir, "vela") }
func (m *Manager) installedVersion() string {
	b, e := os.ReadFile(filepath.Join(m.live(), ".ether-release.json"))
	if e != nil {
		b, _ = os.ReadFile(filepath.Join(m.live(), "app.version.json"))
		if len(b) == 0 {
			matches, _ := filepath.Glob(filepath.Join(m.live(), "vela-*.manifest.json"))
			if len(matches) == 1 {
				b, _ = os.ReadFile(matches[0])
			}
		}
	}
	var r struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(b, &r)
	return r.Version
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state
	s.InstalledVersion = m.installedVersion()
	st, e := os.Stat(filepath.Join(m.live(), "index.html"))
	s.Installed = e == nil && st.Mode().IsRegular()
	s.RollbackAvailable = len(m.rollbacks()) > 0
	return s
}
func (m *Manager) begin(phase string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Busy {
		return errors.New("another Vela action is running")
	}
	m.state.Busy = true
	m.state.Phase = phase
	m.state.Error = ""
	m.state.Downloaded = 0
	m.state.Total = 0
	return nil
}
func (m *Manager) finish(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Busy = false
	m.state.Phase = ""
	if err != nil {
		m.state.Error = err.Error()
	}
}
func (m *Manager) progress(phase string, n, total int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Phase = phase
	m.state.Downloaded = n
	m.state.Total = total
}
func (m *Manager) source() (string, bool) {
	u := ""
	if m.channel != nil {
		u = m.channel()
	}
	if u == "" {
		return StableURL, false
	}
	return u, strings.HasPrefix(u, "file://")
}
func (m *Manager) Check(ctx context.Context) (err error) {
	if err = m.begin("checking"); err != nil {
		return
	}
	defer func() { m.finish(err) }()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	address, local := m.source()
	raw, e := readSource(ctx, m.client, address, local, maxMetadata)
	if e != nil {
		return e
	}
	sig, e := readSource(ctx, m.client, address+".sig", local, 1024)
	if e != nil {
		return e
	}
	// Verify without the installed comparison so an up-to-date release remains
	// a successful check; Install always applies the strict monotonic check.
	r, e := Verify(raw, sig, m.keys, m.ether, "", time.Now())
	if e != nil {
		return e
	}
	installed := m.installedVersion()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.release = nil
	m.state.AvailableVersion = ""
	if installed != "" {
		n, e := compare(r.Version, installed)
		if e != nil {
			return e
		}
		if n <= 0 {
			return nil
		}
	}
	m.release = r
	m.state.AvailableVersion = r.Version
	return nil
}
func compare(a, b string) (int, error) { return updater.CompareVersions(a, b) }

func (m *Manager) Install(ctx context.Context) (err error) {
	if err = m.begin("downloading"); err != nil {
		return
	}
	defer func() { m.finish(err) }()
	m.mu.Lock()
	r := m.release
	m.mu.Unlock()
	if r == nil {
		return errors.New("check for a Vela release first")
	}
	if !r.ExpiresAt.After(time.Now()) {
		return errors.New("release has expired; check again")
	}
	installed := m.installedVersion()
	if installed != "" {
		n, e := compare(r.Version, installed)
		if e != nil || n <= 0 {
			return errors.New("release is not newer than the installed version")
		}
	}
	// Unknown manually installed versions must not be silently downgraded.
	if st, e := os.Stat(filepath.Join(m.live(), "index.html")); e == nil && st.Mode().IsRegular() && installed == "" {
		return errors.New("installed Vela version is unknown; add its build inventory before updating")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	_, local := m.source()
	raw, e := metadata(ctx, m.client, r.Manifest, local)
	if e != nil {
		return e
	}
	var inv inventory
	if e = json.Unmarshal(raw, &inv); e != nil || inv.SchemaVersion != 1 || inv.Version != r.Version || len(inv.Files) == 0 {
		return errors.New("invalid build inventory")
	}
	expected, total, e := inventoryMap(inv.Files)
	if e != nil {
		return e
	}
	engines := map[string]inventory{}
	for id, asset := range r.Engines {
		b, e := metadata(ctx, m.client, asset.Manifest, local)
		if e != nil {
			return e
		}
		var engine inventory
		if e = json.Unmarshal(b, &engine); e != nil || engine.SchemaVersion != 1 || engine.Version != r.Version || engine.Engine != id || engine.BasePath != "/vela/engines/"+id+"/"+r.Version+"/" || len(engine.Files) == 0 {
			return errors.New("invalid engine inventory")
		}
		files, _, e := inventoryMap(engine.Files)
		if e != nil {
			return e
		}
		prefix := "engines/" + id + "/" + r.Version + "/"
		count := 0
		for p, f := range expected {
			if strings.HasPrefix(p, prefix) {
				count++
				ef, ok := files[strings.TrimPrefix(p, prefix)]
				if !ok || ef.Bytes != f.Bytes || !strings.EqualFold(ef.SHA256, f.SHA256) {
					return errors.New("engine and build inventories disagree")
				}
			}
		}
		if count != len(files) {
			return errors.New("engine inventory is incomplete")
		}
		engines[id] = engine
	}
	// Require inventory coverage of every shipped engine.
	for p := range expected {
		parts := strings.Split(p, "/")
		if len(parts) > 3 && parts[0] == "engines" {
			if _, ok := engines[parts[1]]; !ok {
				return errors.New("missing engine inventory")
			}
		}
	}
	if e = os.MkdirAll(m.dir, 0700); e != nil {
		return errors.New("cannot create Vela client directory")
	}
	stage := filepath.Join(m.dir, ".vela-staging-"+r.Version)
	if e = os.Mkdir(stage, 0700); e != nil {
		return errors.New("Vela staging directory already exists or is unavailable")
	}
	defer os.RemoveAll(stage)
	archive, e := os.CreateTemp(m.dir, ".vela-download-*")
	if e != nil {
		return errors.New("cannot stage Vela download")
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	body, e := openSource(ctx, m.client, r.Archive.URL, local)
	if e != nil {
		return e
	}
	defer body.Close()
	h := sha256.New()
	buf := make([]byte, 64<<10)
	var n int64
	limited := io.LimitReader(body, r.Archive.Bytes+1)
	for {
		if e = ctx.Err(); e != nil {
			return errors.New("Vela install cancelled")
		}
		read, re := limited.Read(buf)
		if read > 0 {
			n += int64(read)
			if n > r.Archive.Bytes {
				return errors.New("archive exceeds signed size")
			}
			if _, e = io.MultiWriter(archive, h).Write(buf[:read]); e != nil {
				return errors.New("cannot write Vela archive")
			}
			m.progress("downloading", n, r.Archive.Bytes)
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			return errors.New("Vela download interrupted")
		}
	}
	if n != r.Archive.Bytes || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), r.Archive.SHA256) {
		return errors.New("archive size or digest mismatch")
	}
	if _, e = archive.Seek(0, 0); e != nil {
		return e
	}
	m.progress("verifying", n, n)
	if e = extract(ctx, archive, stage, total); e != nil {
		return e
	}
	if e = verifyFiles(ctx, stage, inv.Files); e != nil {
		return e
	}
	for id, engine := range engines {
		if e = verifyFiles(ctx, filepath.Join(stage, "engines", id, r.Version), engine.Files); e != nil {
			return e
		}
	}
	if s, e := os.Stat(filepath.Join(stage, "index.html")); e != nil || !s.Mode().IsRegular() {
		return errors.New("release has no player entry point")
	}
	if e = preserve(ctx, m.live(), stage); e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(stage, ".ether-inventory.json"), raw, 0600); e != nil {
		return errors.New("cannot record installed inventory")
	}
	b, _ := json.Marshal(r)
	if e = os.WriteFile(filepath.Join(stage, ".ether-release.json"), b, 0600); e != nil {
		return errors.New("cannot record installed release")
	}
	if e = ctx.Err(); e != nil {
		return errors.New("Vela install cancelled")
	}
	if !r.ExpiresAt.After(time.Now()) {
		return errors.New("release has expired; check again")
	}
	m.progress("installing", n, n)
	if e = m.swap(stage); e != nil {
		return e
	}
	m.mu.Lock()
	m.release = nil
	m.state.AvailableVersion = ""
	m.mu.Unlock()
	return nil
}
func inventoryMap(files []File) (map[string]File, int64, error) {
	out := map[string]File{}
	var total int64
	for _, f := range files {
		d, e := hex.DecodeString(f.SHA256)
		if !safePath(f.Path) || (f.Path == ".ether-release.json" || f.Path == ".ether-inventory.json") || f.Bytes < 0 || f.Bytes > maxExtracted || e != nil || len(d) != 32 {
			return nil, 0, errors.New("invalid inventory entry")
		}
		if _, ok := out[f.Path]; ok {
			return nil, 0, errors.New("duplicate inventory entry")
		}
		total += f.Bytes
		if total > maxExtracted {
			return nil, 0, errors.New("build exceeds extraction limit")
		}
		out[f.Path] = f
	}
	return out, total, nil
}
func safePath(p string) bool {
	return p != "" && p != "." && !strings.ContainsAny(p, "\\\x00:") && !path.IsAbs(p) && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}
func extract(ctx context.Context, src io.Reader, dir string, limit int64) error {
	gz, e := gzip.NewReader(src)
	if e != nil {
		return errors.New("invalid release archive")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	for {
		if e = ctx.Err(); e != nil {
			return errors.New("Vela install cancelled")
		}
		h, e := tr.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return errors.New("invalid tar archive")
		}
		p := strings.TrimSuffix(strings.TrimPrefix(h.Name, "./"), "/")
		if p == "" && h.Typeflag == tar.TypeDir && h.Name == "./" {
			continue
		}
		if p == "." && h.Typeflag == tar.TypeDir {
			continue
		}
		if !safePath(p) {
			return errors.New("unsafe archive path")
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		switch h.Typeflag {
		case tar.TypeDir:
			if e = os.MkdirAll(target, 0755); e != nil {
				return errors.New("cannot create archive directory")
			}
		case tar.TypeReg: // tar.Reader normalizes the legacy zero type to TypeReg.
			if h.Size < 0 || h.Size > limit-total {
				return errors.New("archive exceeds extracted size limit")
			}
			total += h.Size
			if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
				return e
			}
			f, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
			if e != nil {
				return errors.New("duplicate or invalid archive file")
			}
			_, e = io.CopyN(f, tr, h.Size)
			ce := f.Close()
			if e != nil || ce != nil {
				return errors.New("cannot extract archive file")
			}
		default:
			return errors.New("archive contains a link or unsupported file type")
		}
	}
}
func verifyFiles(ctx context.Context, root string, files []File) error {
	for _, entry := range files {
		if ctx.Err() != nil {
			return errors.New("Vela install cancelled")
		}
		f, e := os.Open(filepath.Join(root, filepath.FromSlash(entry.Path)))
		if e != nil {
			return errors.New("release inventory file is missing")
		}
		s, e := f.Stat()
		if e != nil || !s.Mode().IsRegular() || s.Size() != entry.Bytes {
			f.Close()
			return errors.New("release inventory size mismatch")
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), entry.SHA256) {
			return errors.New("release inventory digest mismatch")
		}
	}
	return nil
}
func (m *Manager) rollbacks() []string {
	paths, _ := filepath.Glob(filepath.Join(m.dir, ".vela-rollback-*"))
	type entry struct {
		path     string
		modified time.Time
	}
	var entries []entry
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err == nil && info.IsDir() {
			entries = append(entries, entry{p, info.ModTime()})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].modified.Equal(entries[j].modified) {
			return entries[i].path > entries[j].path
		}
		return entries[i].modified.After(entries[j].modified)
	})
	paths = nil
	for _, e := range entries {
		paths = append(paths, e.path)
	}
	return paths
}
func (m *Manager) swap(stage string) error {
	backup := filepath.Join(m.dir, fmt.Sprintf(".vela-rollback-%020d", time.Now().UnixNano()))
	hadLive := false
	if _, e := os.Lstat(m.live()); e == nil {
		if e = os.Rename(m.live(), backup); e != nil {
			return errors.New("cannot retain previous Vela build")
		}
		// Rename keeps a directory's old mtime. Stamp the new rollback so
		// retention measures when it was taken, independent of its name.
		now := time.Now()
		if e = os.Chtimes(backup, now, now); e != nil {
			if restore := os.Rename(backup, m.live()); restore != nil {
				return errors.New("cannot date rollback; previous build is retained in the rollback directory")
			}
			return errors.New("cannot date previous Vela build")
		}
		hadLive = true
	} else if !os.IsNotExist(e) {
		return errors.New("cannot inspect installed Vela")
	}
	if e := os.Rename(stage, m.live()); e != nil {
		if hadLive {
			if restore := os.Rename(backup, m.live()); restore != nil {
				return errors.New("install failed; previous build is retained in the rollback directory")
			}
		}
		return errors.New("cannot activate Vela build")
	}
	// Always retain the rollback just created, even if an older directory
	// has a future mtime or the filesystem rounds timestamps to a tie.
	kept := 0
	if hadLive {
		kept = 1
	}
	for _, p := range m.rollbacks() {
		if hadLive && p == backup {
			continue
		}
		if kept < 2 {
			kept++
			continue
		}
		_ = os.RemoveAll(p)
	}
	return nil
}
func (m *Manager) Rollback(ctx context.Context) (err error) {
	if err = m.begin("rollback"); err != nil {
		return
	}
	defer func() { m.finish(err) }()
	paths := m.rollbacks()
	if len(paths) == 0 {
		return errors.New("no previous Vela build is available")
	}
	if ctx.Err() != nil {
		return errors.New("Vela rollback cancelled")
	}
	if err = preserve(ctx, m.live(), paths[0]); err != nil {
		return
	}
	if ctx.Err() != nil {
		return errors.New("Vela rollback cancelled")
	}
	if err = m.swap(paths[0]); err == nil {
		m.mu.Lock()
		m.release = nil
		m.state.AvailableVersion = ""
		m.mu.Unlock()
	}
	return
}

// Preserve only owner-staged additive families, without replacing release files.
func preserve(ctx context.Context, old, stage string) error {
	// Do not follow an owner-staged parent link outside the prior live tree.
	for _, p := range []string{old, filepath.Join(old, "universal"), filepath.Join(old, "engines"), filepath.Join(old, "engines", "cinematic")} {
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return errors.New("cannot inspect owner Vela directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("owner Vela directory contains a symbolic link")
		}
	}

	buildJSON, err := buildRootJSON(old)
	if err != nil {
		return err
	}
	patterns := []string{"diagnostic-core-*", "universal/*", "engines/cinematic/*", "core-app-*.html", "*.json"}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(filepath.Join(old, pattern))
		for _, src := range matches {
			rel, _ := filepath.Rel(old, src)
			if pattern == "*.json" {
				if strings.HasPrefix(rel, ".") || buildJSON[rel] || (strings.HasPrefix(rel, "vela-") && strings.HasSuffix(rel, ".manifest.json")) {
					continue
				}
				info, err := os.Lstat(src)
				if err != nil {
					return errors.New("cannot inspect owner Vela JSON")
				}
				if info.IsDir() {
					continue
				}
			}
			dest := filepath.Join(stage, rel)
			if _, e := os.Lstat(dest); e == nil {
				continue
			}
			if e := copyAdditive(ctx, src, dest); e != nil {
				return e
			}
		}
	}
	return preserveImmutable(ctx, old, stage)
}

// Open tabs can load chunks, fonts and player workers after an install or
// rollback. Carry their immutable URLs forward, but never carry mutable entry
// points (index, service worker, routes or release metadata) into another build.
func preserveImmutable(ctx context.Context, old, stage string) error {
	return filepath.WalkDir(old, func(src string, entry fs.DirEntry, err error) error {
		if err != nil {
			if src == old && os.IsNotExist(err) {
				return nil // First installation has no outgoing assets.
			}
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(old, src)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		family := strings.Split(rel, "/")[0]
		assetTree := family == "_expo" || family == "assets" || family == "player" || family == "skins"
		if entry.IsDir() {
			if !assetTree {
				return filepath.SkipDir
			}
			return nil
		}
		if !immutableAsset(rel) {
			// A linked asset tree must not silently lose its open-tab assets.
			if assetTree && entry.Type()&os.ModeSymlink != 0 {
				return errors.New("Vela asset directory contains a symbolic link")
			}
			return nil
		}
		// Rollback destinations may contain owner additions. Never follow a
		// destination parent link while copying an otherwise safe relative URL.
		parent := stage
		for _, part := range strings.Split(path.Dir(rel), "/") {
			if part == "." {
				continue
			}
			parent = filepath.Join(parent, part)
			info, e := os.Lstat(parent)
			if os.IsNotExist(e) {
				break
			}
			if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("unsafe Vela asset destination")
			}
		}
		dest := filepath.Join(stage, filepath.FromSlash(rel))
		if info, e := os.Lstat(dest); e == nil {
			// One immutable URL must serve the same bytes to both old and new
			// tabs. A signed release can still accidentally reuse an old URL.
			if !info.Mode().IsRegular() || !entry.Type().IsRegular() {
				return errors.New("unsafe Vela asset collision")
			}
			previous, e := assetDigest(ctx, src)
			if e != nil {
				return e
			}
			next, e := assetDigest(ctx, dest)
			if e != nil {
				return e
			}
			if previous != next {
				return fmt.Errorf("Vela immutable asset URL has different content: %s", rel)
			}
			return nil
		} else if !os.IsNotExist(e) {
			return e
		}
		return copyAdditive(ctx, src, dest)
	})
}

func assetDigest(ctx context.Context, filename string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	f, err := os.Open(filename)
	if err != nil {
		return digest, err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return digest, err
		}
		n, err := f.Read(buf)
		h.Write(buf[:n])
		if err == io.EOF {
			copy(digest[:], h.Sum(nil))
			return digest, nil
		}
		if err != nil {
			return digest, err
		}
	}
}

func immutableAsset(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) >= 3 && parts[0] == "player" && hexID(parts[1], 64) {
		return true
	}
	if len(parts) >= 4 && parts[0] == "skins" {
		if _, err := updater.ParseVersion(parts[2]); err == nil {
			return true
		}
	}
	if !(strings.HasPrefix(rel, "_expo/static/") || strings.HasPrefix(rel, "assets/") || strings.HasPrefix(rel, "skins/assets/") || (len(parts) == 1 && strings.HasPrefix(rel, "loader-"))) {
		return false
	}
	for _, token := range strings.FieldsFunc(path.Base(rel), func(r rune) bool { return r == '-' || r == '.' || r == '@' }) {
		if hexID(token, 16) {
			return true
		}
	}
	return false
}

func hexID(s string, minimum int) bool {
	if len(s) < minimum || len(s) > 64 || len(s)%2 != 0 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// New installs retain their verified full inventory for offline carry-over.
// Manual installs may have the original full manifest alongside their assets.
func buildRootJSON(dir string) (map[string]bool, error) {
	out := map[string]bool{"app.version.json": true, "engines.json": true, "manifest.json": true}
	manifests, err := filepath.Glob(filepath.Join(dir, "vela-*.manifest.json"))
	if err != nil {
		return nil, err
	}
	manifests = append([]string{filepath.Join(dir, ".ether-inventory.json")}, manifests...)
	for _, p := range manifests {
		f, err := os.Open(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, errors.New("cannot read installed Vela inventory")
		}
		b, err := io.ReadAll(io.LimitReader(f, maxMetadata+1))
		f.Close()
		if err != nil || len(b) > maxMetadata {
			return nil, errors.New("cannot read installed Vela inventory")
		}
		var inv inventory
		if json.Unmarshal(b, &inv) != nil || inv.SchemaVersion != 1 || len(inv.Files) == 0 {
			return nil, errors.New("invalid installed Vela inventory")
		}
		for _, entry := range inv.Files {
			if strings.HasSuffix(entry.Path, ".json") && !strings.Contains(entry.Path, "/") {
				out[entry.Path] = true
			}
		}
	}
	return out, nil
}

func copyAdditive(ctx context.Context, src, dest string) error {
	if ctx.Err() != nil {
		return errors.New("Vela install cancelled")
	}
	s, e := os.Lstat(src)
	if e != nil {
		return errors.New("cannot inspect owner Vela files")
	}
	if s.Mode()&os.ModeSymlink != 0 {
		return errors.New("owner Vela files contain a symbolic link")
	}
	if s.IsDir() {
		if e = os.MkdirAll(dest, 0755); e != nil {
			return errors.New("cannot preserve owner Vela directory")
		}
		entries, e := os.ReadDir(src)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if e = copyAdditive(ctx, filepath.Join(src, entry.Name()), filepath.Join(dest, entry.Name())); e != nil {
				return e
			}
		}
		return nil
	}
	if !s.Mode().IsRegular() {
		return errors.New("owner Vela files contain an unsupported file type")
	}
	if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
		return e
	}
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if os.IsExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	ce := out.Close()
	if e != nil {
		return e
	}
	return ce
}
