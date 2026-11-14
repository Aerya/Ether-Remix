package ftp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Service est la source FTP / SFTP active.
type Service struct {
	Settings Settings
	DB       *sql.DB
	Log      *slog.Logger
	Now      func() time.Time
	// Remote remplace la connexion (tests) ; nil : NewRemote(Settings).
	Remote Remote
	// Protected indique un fichier (chemin vu par Remix) rangé dans une
	// bibliothèque locale : jamais effacé.
	Protected func(local string) bool
	// Usage mesure la place libre (datafs.Usage par défaut).
	Usage func(path string) (total, free int64, err error)

	remote Remote
	server streamServer

	scanMu  sync.Mutex // un parcours à la fois
	queueMu sync.Mutex // réserve atomiquement noms et budget des copies

	mu          sync.Mutex
	files       map[string]*indexed
	tasks       map[string]*Task
	lives       map[string]*live
	readers     map[string]int
	scanning    bool
	scanStart   time.Time
	lastScan    time.Time
	lastScanRes ScanResult
	scanErr     string
	lastErr     string
	lastErrT    time.Time
	sem         chan struct{}

	saveMu sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Start ouvre la connexion (paresseusement), recharge l'index et les
// copies, puis lance le parcours périodique et le nettoyage.
func (s *Service) Start() error {
	s.remote = s.Remote
	if s.remote == nil {
		rm, err := NewRemote(s.Settings)
		if err != nil {
			return err
		}
		s.remote = rm
	}
	s.server.svc = s
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.mu.Lock()
	s.files, s.tasks, s.lives, s.readers = map[string]*indexed{}, map[string]*Task{}, map[string]*live{}, map[string]int{}
	s.sem = make(chan struct{}, s.Settings.MaxActive)
	s.mu.Unlock()
	s.loadIndex(s.ctx)
	s.loadTasks(s.ctx)
	s.wg.Add(1)
	go s.loop()
	return nil
}

// Close arrête les copies, le serveur local et la connexion.
func (s *Service) Close() {
	s.queueMu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.queueMu.Unlock()
	s.server.Close()
	if s.remote != nil {
		_ = s.remote.Close()
	}
	s.wg.Wait()
}

// loop parcourt le serveur au démarrage s'il le faut, puis à intervalles
// réguliers, et nettoie le cache des copies.
func (s *Service) loop() {
	defer s.wg.Done()
	s.resumeTasks()
	s.mu.Lock()
	empty := len(s.files) == 0
	s.mu.Unlock()
	next := s.Settings.ScanEvery
	if empty || s.lastIndexAge() > s.Settings.ScanEvery {
		next = 5 * time.Second
	}
	scanT := time.NewTimer(next)
	cleanT := time.NewTicker(10 * time.Minute)
	defer scanT.Stop()
	defer cleanT.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-scanT.C:
			ctx, cancel := context.WithTimeout(s.ctx, 2*time.Hour)
			if _, err := s.Rescan(ctx); err != nil && s.ctx.Err() == nil {
				s.log().Warn("parcours FTP impossible", "err", scrub(err, s.Settings))
			}
			cancel()
			scanT.Reset(s.Settings.ScanEvery)
		case <-cleanT.C:
			s.cleanup()
		}
	}
}

// lastIndexAge est l'âge du fichier vu le plus récemment.
func (s *Service) lastIndexAge() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	var last time.Time
	for _, f := range s.files {
		if f.Seen.After(last) {
			last = f.Seen
		}
	}
	if last.IsZero() {
		return 1 << 62
	}
	return s.now().Sub(last)
}

func (s *Service) noteErr(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	s.mu.Lock()
	s.lastErr, s.lastErrT = scrub(err, s.Settings), s.now()
	s.mu.Unlock()
}

// reading compte les lectures en cours d'un fichier (jamais effacé
// pendant qu'on le lit).
func (s *Service) reading(id string, d int) {
	s.mu.Lock()
	s.readers[id] += d
	if s.readers[id] <= 0 {
		delete(s.readers, id)
	}
	s.mu.Unlock()
}

func notice(err error, fr, en string) error {
	return &core.LocalNoticeError{FR: fr, EN: en, Err: err}
}

// errPreparing : la copie continue après l'attente du lecteur (mode
// téléchargement). Pour Ether, c'est un téléchargement en file.
var errPreparing = fmt.Errorf("ftp: %w; %w", core.ErrDownloadQueued, core.ErrPrepareSlow)

// prepareWait borne l'attente dans la sonde d'Ether (20 s).
const prepareWait = 12 * time.Second

// Prepare prépare une release FTP avant la sonde d'Ether : vérifie que le
// fichier est toujours là et donne un lien local à jeton. Une copie sur
// disque ne démarre que pour une vraie lecture (jamais pour l'indexation,
// une vérification ou un préchargement).
func (s *Service) Prepare(ctx context.Context, rel core.Release) (core.Release, bool, error) {
	id, ok := IDOf(rel)
	if !ok {
		return rel, false, nil
	}
	if s == nil || s.remote == nil {
		return rel, true, fmt.Errorf("ftp: source désactivée : %w", core.ErrSourceUnavailable)
	}
	f, ok := s.lookup(id)
	if !ok {
		return rel, true, notice(fmt.Errorf("ftp: fichier inconnu de l'index : %w", core.ErrSourceUnavailable),
			"Ce fichier n'est plus dans l'index FTP : il a sans doute été retiré du serveur.",
			"This file is no longer in the FTP index: it was probably removed from the server.")
	}
	play := core.OnDemandStreamsAllowed(ctx) && !core.PSEAvailabilityCheck(ctx) && !core.IsBackgroundCheck(ctx)
	if _, ok := s.localCopy(id); ok {
		return s.link(rel, id)
	}
	sctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	e, err := s.remote.Stat(sctx, f.Path)
	cancel()
	switch {
	case errors.Is(err, ErrNotFound):
		s.forget(id)
		return rel, true, notice(fmt.Errorf("ftp: %s absent du serveur : %w", f.Path, core.ErrSourceUnavailable),
			"Ce fichier a été retiré du serveur FTP.", "This file was removed from the FTP server.")
	case err != nil:
		s.noteErr(err)
		return rel, true, notice(fmt.Errorf("ftp: serveur injoignable : %w: %v", core.ErrSourceUnavailable, scrub(err, s.Settings)),
			"Le serveur FTP / SFTP ne répond pas pour l'instant.", "The FTP / SFTP server does not answer for now.")
	case e.Size != f.Size:
		s.forget(id)
		return rel, true, notice(fmt.Errorf("ftp: %s a changé sur le serveur : %w", f.Path, core.ErrSourceUnavailable),
			"Ce fichier a changé sur le serveur FTP : il sera repris au prochain parcours.", "This file changed on the FTP server: it will be picked up at the next scan.")
	}
	switch s.Settings.Mode {
	case ModeStream:
		return s.link(rel, id)
	case ModeBoth:
		if play {
			if _, err := s.ensure(ctx, f, true, false); err != nil && !IsNotice(err) {
				s.log().Warn("copie FTP non lancée", "err", err)
			}
		}
		return s.link(rel, id)
	}
	// Téléchargement seulement : la lecture attend la copie complète.
	if !play {
		return rel, true, fmt.Errorf("ftp: copie à lancer par une lecture : %w", core.ErrForegroundOnly)
	}
	if _, err := s.ensure(ctx, f, false, false); err != nil {
		return rel, true, err
	}
	wait := prepareWait
	if dl, ok := ctx.Deadline(); ok {
		wait = min(wait, time.Until(dl)-6*time.Second)
	}
	deadline := s.now().Add(max(wait, 0))
	for {
		if _, ok := s.localCopy(id); ok {
			return s.link(rel, id)
		}
		if err := s.taskErr(id); err != nil {
			return rel, true, err
		}
		if !s.now().Before(deadline) {
			return rel, true, s.pendingNotice(id)
		}
		select {
		case <-ctx.Done():
			return rel, true, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (s *Service) link(rel core.Release, id string) (core.Release, bool, error) {
	u, origin, err := s.server.URL(id)
	if err != nil {
		return rel, true, fmt.Errorf("ftp: serveur local : %w: %w", core.ErrSourceUnavailable, err)
	}
	rel.StreamURL, rel.StreamOrigin, rel.StreamHeaders = u, origin, nil
	return rel, true, nil
}

// IsNotice indique une erreur portant un message pour le spectateur.
func IsNotice(err error) bool {
	var n *core.LocalNoticeError
	return errors.As(err, &n)
}
