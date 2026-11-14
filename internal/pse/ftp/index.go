package ftp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_ftp", 1, `CREATE TABLE pse_ftp_files(id TEXT PRIMARY KEY, data TEXT NOT NULL);
CREATE TABLE pse_ftp_downloads(id TEXT PRIMARY KEY, data TEXT NOT NULL);`)
}

// File est une vidéo trouvée sur le serveur.
type File struct {
	ID     string    `json:"id"`
	Server string    `json:"server"` // Settings.Server() lors du parcours
	Path   string    `json:"path"`
	Name   string    `json:"name"` // nom analysé : fichier, ou dossier parent
	Size   int64     `json:"size"`
	MTime  time.Time `json:"mtime,omitzero"`
	Seen   time.Time `json:"seen"`
}

type indexed struct {
	File
	info core.ReleaseInfo
}

// Limites d'un parcours : un serveur immense ou une boucle de liens ne
// bloque pas Remix.
const (
	maxDirs      = 50000
	maxFiles     = 300000
	minVideoSize = 32 << 20 // sous ce seuil, Ether refuse le flux (extrait, échantillon)
)

// VideoExt indique un fichier vidéo lisible tel quel (pas une archive).
func VideoExt(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".mkv", ".mp4", ".m4v", ".webm", ".ts", ".m2ts", ".avi", ".mov":
		return true
	}
	return false
}

// ReleaseName choisit le nom à analyser : celui du fichier, ou celui de son
// dossier quand le fichier ne dit pas assez (nom court ou anonymisé, comme
// « abc123.mkv » dans un dossier de release).
func ReleaseName(remote string) string {
	base := path.Base(remote)
	if useful(releaseparse.Parse(base)) {
		return base
	}
	dir := path.Base(path.Dir(remote))
	if dir == "/" || dir == "." || dir == "" {
		return base
	}
	cand := dir + path.Ext(base)
	if useful(releaseparse.Parse(cand)) {
		return cand
	}
	return base
}

func useful(i core.ReleaseInfo) bool {
	return strings.TrimSpace(i.Title) != "" && (i.Year > 0 || len(i.Episodes) > 0)
}

func sample(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "sample") || strings.Contains(n, "trailer") || strings.HasPrefix(n, "rarbg.com")
}

// ScanResult résume un parcours.
type ScanResult struct {
	Files  int
	Dirs   int
	Failed []string // dossiers illisibles (leurs anciens fichiers sont gardés)
	Capped bool
}

// scan parcourt les dossiers choisis, en largeur, jusqu'à la profondeur
// maximale.
func scan(ctx context.Context, rm Remote, s Settings, now time.Time) ([]File, ScanResult, error) {
	type item struct {
		dir   string
		depth int
	}
	var res ScanResult
	var out []File
	queue := make([]item, 0, len(s.Roots))
	for _, r := range s.Roots {
		queue = append(queue, item{r, 0})
	}
	seenDirs := map[string]bool{}
	rootsOK := 0
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, res, err
		}
		it := queue[0]
		queue = queue[1:]
		if seenDirs[it.dir] {
			continue
		}
		seenDirs[it.dir] = true
		if res.Dirs >= maxDirs || len(out) >= maxFiles {
			res.Capped = true
			break
		}
		res.Dirs++
		entries, err := rm.ReadDir(ctx, it.dir)
		if err != nil {
			if errors.Is(err, errClosed) || ctx.Err() != nil {
				return nil, res, err
			}
			res.Failed = append(res.Failed, it.dir)
			continue
		}
		if it.depth == 0 {
			rootsOK++
		}
		for _, e := range entries {
			name := path.Base(e.Path)
			if e.Dir {
				if it.depth+1 <= s.Depth && !s.Excluded(name) {
					queue = append(queue, item{e.Path, it.depth + 1})
				}
				continue
			}
			if !VideoExt(name) || e.Size < minVideoSize || sample(name) {
				continue
			}
			out = append(out, File{ID: s.FileID(e.Path), Server: s.Server(), Path: e.Path, Name: ReleaseName(e.Path), Size: e.Size, MTime: e.MTime, Seen: now})
		}
	}
	if rootsOK == 0 {
		return nil, res, fmt.Errorf("aucun dossier lisible sur le serveur (%s)", strings.Join(s.Roots, ", "))
	}
	res.Files = len(out)
	return out, res, nil
}

// --- index en mémoire et en base ---

func (s *Service) loadIndex(ctx context.Context) {
	if s.DB == nil {
		return
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT data FROM pse_ftp_files")
	if err != nil {
		s.log().Warn("index FTP illisible", "err", err)
		return
	}
	defer rows.Close()
	server := s.Settings.Server()
	var files []indexed
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		var f File
		if json.Unmarshal([]byte(raw), &f) == nil && f.ID != "" && f.Server == server {
			files = append(files, indexed{File: f, info: releaseparse.Parse(f.Name)})
		}
	}
	s.mu.Lock()
	s.files = byID(files)
	s.mu.Unlock()
}

func byID(files []indexed) map[string]*indexed {
	m := make(map[string]*indexed, len(files))
	for i := range files {
		m[files[i].ID] = &files[i]
	}
	return m
}

// Rescan relance un parcours complet (au plus un à la fois).
func (s *Service) Rescan(ctx context.Context) (ScanResult, error) {
	if !s.scanMu.TryLock() {
		return ScanResult{}, errors.New("un parcours est déjà en cours")
	}
	defer s.scanMu.Unlock()
	s.mu.Lock()
	s.scanning, s.scanStart = true, s.now()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.scanning = false
		s.mu.Unlock()
	}()
	now := s.now()
	found, res, err := scan(ctx, s.remote, s.Settings, now)
	s.mu.Lock()
	s.lastScan, s.lastScanRes = now, res
	if err != nil {
		s.scanErr = scrub(err, s.Settings)
		s.mu.Unlock()
		return res, err
	}
	s.scanErr = ""
	old := s.files
	s.mu.Unlock()
	// Fichiers sous un dossier illisible cette fois : gardés tels quels.
	next := make([]indexed, 0, len(found))
	for _, f := range found {
		next = append(next, indexed{File: f, info: releaseparse.Parse(f.Name)})
	}
	if len(res.Failed) > 0 || res.Capped {
		have := map[string]bool{}
		for _, f := range found {
			have[f.ID] = true
		}
		for _, o := range old {
			if !have[o.ID] && (res.Capped || under(o.Path, res.Failed)) {
				next = append(next, *o)
			}
		}
	}
	m := byID(next)
	s.mu.Lock()
	s.files = m
	s.mu.Unlock()
	s.saveIndex(ctx, m)
	s.log().Info("index FTP mis à jour", "fichiers", len(m), "dossiers", res.Dirs, "illisibles", len(res.Failed))
	return res, nil
}

func under(p string, dirs []string) bool {
	for _, d := range dirs {
		if p == d || strings.HasPrefix(p, strings.TrimRight(d, "/")+"/") {
			return true
		}
	}
	return false
}

// saveIndex remplace l'index enregistré.
func (s *Service) saveIndex(ctx context.Context, m map[string]*indexed) {
	if s.DB == nil {
		return
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		s.log().Warn("index FTP non enregistré", "err", err)
		return
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "DELETE FROM pse_ftp_files"); err != nil {
		s.log().Warn("index FTP non enregistré", "err", err)
		return
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		raw, _ := json.Marshal(m[id].File)
		if _, err := tx.ExecContext(ctx, "INSERT INTO pse_ftp_files(id,data) VALUES(?,?)", id, string(raw)); err != nil {
			s.log().Warn("index FTP non enregistré", "err", err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.log().Warn("index FTP non enregistré", "err", err)
	}
}

// forget retire un fichier disparu du serveur.
func (s *Service) forget(id string) {
	s.mu.Lock()
	delete(s.files, id)
	s.mu.Unlock()
	if s.DB != nil {
		_, _ = s.DB.ExecContext(context.Background(), "DELETE FROM pse_ftp_files WHERE id=?", id)
	}
}

// lookup renvoie un fichier de l'index.
func (s *Service) lookup(id string) (File, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.files[id]
	if f == nil {
		return File{}, false
	}
	return f.File, true
}
