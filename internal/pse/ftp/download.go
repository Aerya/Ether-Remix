package ftp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/datafs"
)

// États d'une copie.
const (
	StateQueued      = "queued"
	StateDownloading = "downloading"
	StateDone        = "done"
	StateFailed      = "failed"
	StateCancelled   = "cancelled"
	StateRemoved     = "removed"
)

// Task est une copie d'un fichier du serveur dans le dossier de
// téléchargement. Remix n'efface jamais que les fichiers qu'il a écrits
// lui-même, et jamais un fichier gardé ou rangé dans une bibliothèque.
type Task struct {
	ID       string    `json:"id"`
	Path     string    `json:"path"`
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Local    string    `json:"local"`
	State    string    `json:"state"`
	Keep     bool      `json:"keep"`
	Manual   bool      `json:"manual,omitempty"`
	Created  time.Time `json:"created"`
	Finished time.Time `json:"finished,omitzero"`
	LastRead time.Time `json:"lastRead,omitzero"`
	Fail     string    `json:"fail,omitempty"`
}

// live est l'avancement en mémoire d'une copie en cours.
type live struct {
	done   int64
	speed  int64
	cancel context.CancelFunc
}

var errSpace = errors.New("place libre insuffisante")

// ensure lance (ou rejoint) la copie d'un fichier.
func (s *Service) ensure(ctx context.Context, f File, keep, manual bool) (Task, error) {
	s.queueMu.Lock()
	defer s.queueMu.Unlock()
	if !s.Settings.Writes() {
		return Task{}, errors.New("mode streaming sans téléchargement")
	}
	if err := s.ctx.Err(); err != nil {
		return Task{}, err
	}
	if s.Settings.DownloadDir == "" {
		return Task{}, notice(fmt.Errorf("ftp: dossier de téléchargement non réglé : %w", core.ErrSourceUnavailable),
			"Aucun dossier de téléchargement FTP n'est réglé (Sources → FTP / SFTP).", "No FTP download folder is set (Sources → FTP / SFTP).")
	}
	s.mu.Lock()
	if t := s.tasks[f.ID]; t != nil {
		switch t.State {
		case StateQueued, StateDownloading, StateDone:
			if t.State == StateDone {
				if st, err := os.Stat(t.Local); err != nil || st.Size() != t.Size {
					t.State = StateRemoved
					break
				}
			}
			if keep && !t.Keep {
				t.Keep = true
				cp := *t
				s.mu.Unlock()
				s.save(cp)
				return cp, nil
			}
			cp := *t
			s.mu.Unlock()
			return cp, nil
		}
	}
	if s.lives[f.ID] != nil {
		s.mu.Unlock()
		return Task{}, errors.New("la copie précédente est encore en cours d’arrêt")
	}
	s.mu.Unlock()
	if err := s.checkSpace(f.Size, keep); err != nil {
		return Task{}, err
	}
	local, err := s.localPath(f)
	if err != nil {
		return Task{}, err
	}
	t := &Task{ID: f.ID, Path: f.Path, Name: f.Name, Size: f.Size, Local: local, State: StateQueued, Keep: keep, Manual: manual, Created: s.now()}
	s.mu.Lock()
	if cur := s.tasks[f.ID]; cur != nil && (cur.State == StateQueued || cur.State == StateDownloading || cur.State == StateDone) {
		cp := *cur
		s.mu.Unlock()
		return cp, nil // lancée entre-temps par une autre lecture
	}
	s.tasks[f.ID] = t
	cp := *t
	s.mu.Unlock()
	s.save(cp)
	s.start(cp)
	return cp, nil
}

// localPath choisit le fichier de destination, toujours dans le dossier de
// téléchargement (nom nettoyé, suffixe « (1) » si le nom est pris).
func (s *Service) localPath(f File) (string, error) {
	dir := filepath.Clean(s.Settings.DownloadDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", notice(fmt.Errorf("ftp: dossier de téléchargement : %w: %w", core.ErrSourceUnavailable, err),
			"Remix ne peut pas écrire dans le dossier de téléchargement FTP : vérifiez le volume.", "Remix cannot write to the FTP download folder: check the volume.")
	}
	base := safeName(filepath.Base(f.Path))
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	s.mu.Lock()
	defer s.mu.Unlock()
	taken := map[string]bool{}
	for _, t := range s.tasks {
		if t.ID != f.ID && t.State != StateRemoved && t.State != StateCancelled {
			taken[t.Local] = true
		}
	}
	for i := 0; i < 1000; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := filepath.Join(dir, name)
		if taken[p] {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			continue // fichier déjà là, qui n'est pas à nous : jamais écrasé
		}
		part, err := os.OpenFile(p+".part", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := part.Close(); err != nil {
			return "", err
		}
		return p, nil
	}
	return "", errors.New("ftp: aucun nom de fichier libre")
}

func safeName(n string) string {
	n = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, n)
	n = strings.TrimLeft(strings.TrimSpace(n), ".")
	if n == "" {
		n = "video"
	}
	if len(n) > 200 {
		ext := filepath.Ext(n)
		n = n[:200-len(ext)] + ext
	}
	return n
}

func (s *Service) checkSpace(size int64, keep bool) error {
	usage := s.Usage
	if usage == nil {
		usage = datafs.Usage
	}
	if s.Settings.MinFree > 0 {
		dir := s.Settings.DownloadDir
		if _, err := os.Stat(dir); err != nil {
			dir = filepath.Dir(dir)
		}
		if _, free, err := usage(dir); err == nil && free-size < s.Settings.MinFree {
			return notice(fmt.Errorf("%w : %w", errSpace, core.ErrLocalNoSpace),
				"Plus assez de place pour la copie FTP : elle n'a pas été lancée.", "Not enough free space for the FTP copy: it was not started.")
		}
	}
	if keep || s.Settings.CacheBytes <= 0 {
		return nil
	}
	if s.cacheUsed()+size > s.Settings.CacheBytes {
		s.cleanupFor(size) // libère la place nécessaire
		if s.cacheUsed()+size > s.Settings.CacheBytes {
			return notice(fmt.Errorf("%w : cache FTP plein : %w", errSpace, core.ErrDownloadLimit),
				"Le cache des copies FTP est plein : la copie n'a pas été lancée.", "The FTP copy cache is full: the copy was not started.")
		}
	}
	return nil
}

func (s *Service) start(t Task) {
	ctx, cancel := context.WithCancel(s.ctx)
	s.mu.Lock()
	if cur := s.tasks[t.ID]; cur == nil || cur.State != StateQueued || s.ctx.Err() != nil {
		s.mu.Unlock()
		cancel()
		return
	}
	s.lives[t.ID] = &live{cancel: cancel}
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		select {
		case s.sem <- struct{}{}:
		case <-ctx.Done():
			s.finishTask(t.ID, ctx.Err())
			return
		}
		defer func() { <-s.sem }()
		s.finishTask(t.ID, s.copy(ctx, t))
	}()
}

// copy écrit le fichier dans « nom.part », reprend une copie interrompue,
// puis renomme le fichier une fois complet.
func (s *Service) copy(ctx context.Context, t Task) error {
	s.setState(t.ID, StateDownloading, "")
	part := t.Local + ".part"
	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	st, err := out.Stat()
	if err != nil {
		return err
	}
	off := min(st.Size(), t.Size)
	if _, err := out.Seek(off, io.SeekStart); err != nil {
		return err
	}
	if err := out.Truncate(off); err != nil {
		return err
	}
	for attempt := 0; off < t.Size; attempt++ {
		rd, err := s.remote.Open(ctx, t.Path, off)
		if err != nil {
			if errors.Is(err, ErrNoResume) && off > 0 { // FTP sans reprise : on recommence
				off = 0
				if err := out.Truncate(0); err != nil {
					return err
				}
				if _, err := out.Seek(0, io.SeekStart); err != nil {
					return err
				}
				continue
			}
			if attempt >= 5 || ctx.Err() != nil || errors.Is(err, ErrNotFound) {
				return err
			}
			if !sleepCtx(ctx, time.Duration(attempt+1)*3*time.Second) {
				return ctx.Err()
			}
			continue
		}
		n, err := s.pump(ctx, t.ID, out, rd, off, t.Size-off)
		_ = rd.Close()
		off += n
		if err != nil && off < t.Size {
			if attempt >= 5 || ctx.Err() != nil {
				return err
			}
			if n > 0 {
				attempt = 0 // ça avançait : nouvelle série d'essais
			}
			if !sleepCtx(ctx, 2*time.Second) {
				return ctx.Err()
			}
		}
	}
	if err := out.Sync(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Link publie sans écraser un fichier créé entre-temps.
	if err := os.Link(part, t.Local); err != nil {
		return err
	}
	return os.Remove(part)
}

// pump copie n octets au plus en mettant à jour l'avancement.
func (s *Service) pump(ctx context.Context, id string, w io.Writer, r io.Reader, start, n int64) (int64, error) {
	buf := make([]byte, copyBuffer)
	var done int64
	mark, markAt := int64(0), time.Now()
	for done < n {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		k, err := r.Read(buf[:min(int64(len(buf)), n-done)])
		if k > 0 {
			if _, werr := w.Write(buf[:k]); werr != nil {
				return done, werr
			}
			done += int64(k)
			if el := time.Since(markAt); el >= time.Second {
				s.mu.Lock()
				if l := s.lives[id]; l != nil {
					l.done, l.speed = start+done, int64(float64(done-mark)/el.Seconds())
				}
				s.mu.Unlock()
				mark, markAt = done, time.Now()
			}
		}
		if err != nil {
			if err == io.EOF && done < n {
				return done, io.ErrUnexpectedEOF
			}
			if err == io.EOF {
				return done, nil
			}
			return done, err
		}
	}
	return done, nil
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func (s *Service) setState(id, state, fail string) {
	s.mu.Lock()
	t := s.tasks[id]
	if t == nil || t.State == StateCancelled {
		s.mu.Unlock()
		return
	}
	t.State, t.Fail = state, fail
	cp := *t
	s.mu.Unlock()
	s.save(cp)
}

func (s *Service) finishTask(id string, err error) {
	s.mu.Lock()
	t := s.tasks[id]
	delete(s.lives, id)
	if t == nil {
		s.mu.Unlock()
		return
	}
	switch {
	case err == nil:
		t.State, t.Fail, t.Finished = StateDone, "", s.now()
	case t.State == StateCancelled:
	case errors.Is(err, context.Canceled) && s.ctx.Err() != nil:
		t.State = StateQueued // arrêt de Remix : reprise au démarrage
	default:
		t.State, t.Fail = StateFailed, scrub(err, s.Settings)
	}
	cp := *t
	s.mu.Unlock()
	if cp.State == StateCancelled || cp.State == StateFailed && errors.Is(err, ErrNotFound) {
		_ = os.Remove(cp.Local + ".part")
	}
	s.save(cp)
	if err != nil && cp.State == StateFailed {
		s.log().Warn("copie FTP en échec", "fichier", cp.Name, "err", cp.Fail)
	}
}

// localCopy renvoie la copie complète d'un fichier, s'il y en a une.
func (s *Service) localCopy(id string) (string, bool) {
	s.mu.Lock()
	t := s.tasks[id]
	if t == nil || t.State != StateDone {
		s.mu.Unlock()
		return "", false
	}
	local, size := t.Local, t.Size
	s.mu.Unlock()
	if st, err := os.Stat(local); err != nil || st.Size() != size {
		return "", false
	}
	return local, true
}

func (s *Service) touch(id string) {
	s.mu.Lock()
	t := s.tasks[id]
	if t == nil {
		s.mu.Unlock()
		return
	}
	save := s.now().Sub(t.LastRead) > time.Minute
	t.LastRead = s.now()
	cp := *t
	s.mu.Unlock()
	if save {
		s.save(cp)
	}
}

// taskErr renvoie le message d'une copie en échec ou arrêtée.
func (s *Service) taskErr(id string) error {
	s.mu.Lock()
	t := s.tasks[id]
	var cp Task
	if t != nil {
		cp = *t
	}
	s.mu.Unlock()
	switch cp.State {
	case StateFailed:
		return notice(fmt.Errorf("ftp: copie en échec : %s : %w", cp.Fail, core.ErrSourceUnavailable),
			"La copie depuis le serveur FTP a échoué : "+cp.Fail, "The copy from the FTP server failed: "+cp.Fail)
	case StateCancelled, StateRemoved:
		return notice(fmt.Errorf("ftp: copie arrêtée : %w", core.ErrSourceUnavailable),
			"La copie FTP a été arrêtée.", "The FTP copy was stopped.")
	}
	return nil
}

// pendingNotice décrit au spectateur l'avancement d'une copie.
func (s *Service) pendingNotice(id string) error {
	s.mu.Lock()
	t := s.tasks[id]
	var size int64
	if t != nil {
		size = t.Size
	}
	var lv live
	if l := s.lives[id]; l != nil {
		lv = *l
	}
	s.mu.Unlock()
	if lv.done <= 0 || size <= 0 {
		return notice(errPreparing, "Copie depuis le serveur FTP lancée. Relancez la lecture une fois terminée.",
			"Copy from the FTP server started. Play again once it finishes.")
	}
	pct := min(int(lv.done*100/size), 99)
	fr, en := fmt.Sprintf("Copie depuis le serveur FTP en cours : %d %%", pct), fmt.Sprintf("Copy from the FTP server running: %d%%", pct)
	if lv.speed > 0 {
		eta := time.Duration((size-lv.done)/lv.speed) * time.Second
		fr += ", encore " + roundDuration(eta) + " environ"
		en += ", about " + roundDuration(eta) + " left"
	}
	return notice(errPreparing, fr+". Relancez la lecture une fois terminée.", en+". Play again once it finishes.")
}

func roundDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "1 min"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Round(time.Minute)/time.Minute))
	default:
		return fmt.Sprintf("%d h %02d", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
}

// Grab lance une copie depuis la console (gardée).
func (s *Service) Grab(ctx context.Context, id string) (Task, error) {
	if !s.Settings.Writes() {
		return Task{}, errors.New("le mode Streaming n'écrit rien sur le disque : choisissez un mode avec téléchargement")
	}
	f, ok := s.lookup(id)
	if !ok {
		return Task{}, errors.New("fichier inconnu de l'index")
	}
	return s.ensure(ctx, f, true, true)
}

// Cancel arrête une copie en cours (le fichier partiel est effacé).
func (s *Service) Cancel(id string) error {
	s.mu.Lock()
	t := s.tasks[id]
	if t == nil || (t.State != StateQueued && t.State != StateDownloading) {
		s.mu.Unlock()
		return errors.New("aucune copie en cours pour ce fichier")
	}
	t.State = StateCancelled
	cp := *t
	l := s.lives[id]
	s.mu.Unlock()
	if l != nil && l.cancel != nil {
		l.cancel()
	}
	s.save(cp)
	return nil
}

// Keep garde une copie : elle ne sera plus effacée par le nettoyage.
func (s *Service) Keep(id string) error {
	s.mu.Lock()
	t := s.tasks[id]
	if t == nil || t.State == StateRemoved || t.State == StateCancelled || t.State == StateFailed {
		s.mu.Unlock()
		return errors.New("aucune copie à garder pour ce fichier")
	}
	t.Keep = true
	cp := *t
	s.mu.Unlock()
	s.save(cp)
	return nil
}

func (s *Service) cacheUsed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, t := range s.tasks {
		if !t.Keep && (t.State == StateDone || t.State == StateDownloading || t.State == StateQueued) {
			n += t.Size
		}
	}
	return n
}

// cleanup efface les copies temporaires anciennes (conservation), puis les
// plus anciennes au-delà du plafond. Jamais une copie gardée, en cours de
// lecture, hors du dossier de téléchargement ou rangée dans une
// bibliothèque locale.
func (s *Service) cleanup() { s.cleanupFor(0) }

func (s *Service) cleanupFor(required int64) {
	now := s.now()
	s.mu.Lock()
	var cands []Task
	for _, t := range s.tasks {
		if t.State == StateDone && !t.Keep && s.readers[t.ID] == 0 {
			cands = append(cands, *t)
		}
	}
	s.mu.Unlock()
	last := func(t Task) time.Time {
		if t.LastRead.After(t.Finished) {
			return t.LastRead
		}
		return t.Finished
	}
	sort.Slice(cands, func(i, j int) bool { return last(cands[i]).Before(last(cands[j])) })
	used := s.cacheUsed()
	for _, t := range cands {
		if now.Sub(last(t)) < s.Settings.Retention && (s.Settings.CacheBytes <= 0 || used+required <= s.Settings.CacheBytes) {
			continue
		}
		if !s.removable(t.Local) {
			continue
		}
		s.mu.Lock()
		cur := s.tasks[t.ID]
		if cur == nil || cur.State != StateDone || cur.Keep || s.readers[t.ID] > 0 || cur.Local != t.Local || !cur.LastRead.Equal(t.LastRead) {
			s.mu.Unlock()
			continue
		}
		if err := os.Remove(cur.Local); err != nil && !os.IsNotExist(err) {
			s.mu.Unlock()
			s.log().Warn("copie FTP non effacée", "fichier", t.Name, "err", err)
			continue
		}
		used -= cur.Size
		cur.State = StateRemoved
		t = *cur
		s.mu.Unlock()
		s.save(t)
	}
}

// removable vérifie qu'un fichier est bien une copie de Remix, rangée dans
// son dossier de téléchargement et hors de toute bibliothèque locale.
func (s *Service) removable(local string) bool {
	dir := filepath.Clean(s.Settings.DownloadDir)
	p := filepath.Clean(local)
	if dir == "" || dir == "/" || !strings.HasPrefix(p, dir+string(filepath.Separator)) {
		return false
	}
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		if rd, err := filepath.EvalSymlinks(dir); err == nil && !strings.HasPrefix(rp, rd+string(filepath.Separator)) {
			return false
		}
		p = rp
	}
	if s.Protected != nil && (s.Protected(local) || s.Protected(p)) {
		return false
	}
	return true
}

// --- persistance ---

func (s *Service) loadTasks(ctx context.Context) {
	if s.DB == nil {
		return
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT data FROM pse_ftp_downloads")
	if err != nil {
		s.log().Warn("copies FTP illisibles", "err", err)
		return
	}
	defer rows.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			continue
		}
		var t Task
		if json.Unmarshal([]byte(raw), &t) == nil && t.ID != "" && t.ID == s.Settings.FileID(t.Path) {
			s.tasks[t.ID] = &t
		}
	}
}

// resumeTasks reprend les copies interrompues par un arrêt de Remix.
func (s *Service) resumeTasks() {
	if !s.Settings.Writes() || s.Settings.DownloadDir == "" {
		return
	}
	s.mu.Lock()
	var todo []Task
	for _, t := range s.tasks {
		if (t.State == StateQueued || t.State == StateDownloading) && s.Settings.FileID(t.Path) == t.ID && s.removable(t.Local) {
			t.State = StateQueued
			todo = append(todo, *t)
		}
	}
	s.mu.Unlock()
	for _, t := range todo {
		s.start(t)
	}
}

func (s *Service) save(t Task) {
	if s.DB == nil {
		return
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	if cur := s.tasks[t.ID]; cur != nil {
		t = *cur
	}
	s.mu.Unlock()
	raw, _ := json.Marshal(t)
	if _, err := s.DB.ExecContext(context.Background(), "INSERT INTO pse_ftp_downloads(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data", t.ID, string(raw)); err != nil {
		s.log().Warn("copie FTP non enregistrée", "err", err)
	}
}
