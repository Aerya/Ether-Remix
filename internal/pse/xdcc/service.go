package xdcc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/datafs"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_xdcc", 1, `CREATE TABLE pse_xdcc_tasks(packet TEXT PRIMARY KEY, data TEXT NOT NULL);`)
}

// États d'une tâche, côté Remix.
const (
	StateQueued      = "queued"      // demandé, en attente du bot
	StateDownloading = "downloading" // transfert en cours
	StateCompleted   = "completed"   // fichier complet chez Grabscher
	StateFailed      = "failed"      // échec signalé par Grabscher
	StateCancelled   = "cancelled"   // arrêté depuis la console
	StateRemoved     = "removed"     // fichier du cache effacé par la politique de conservation
)

// Task est un téléchargement XDCC lancé par Remix. Un paquet n'a qu'une
// tâche : plusieurs spectateurs d'un même paquet la partagent.
type Task struct {
	Packet   string    `json:"packet"`
	NZO      string    `json:"nzo"`
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Bot      string    `json:"bot"`
	BotGUID  string    `json:"botGuid"`
	Keep     bool      `json:"keep"`
	Manual   bool      `json:"manual,omitempty"`
	State    string    `json:"state"`
	Fail     string    `json:"fail,omitempty"`
	Storage  string    `json:"storage,omitempty"` // chemin final vu par Grabscher
	Created  time.Time `json:"created"`
	Done     time.Time `json:"done,omitempty"`
	LastRead time.Time `json:"lastRead,omitempty"`
}

func (t *Task) active() bool { return t.State == StateQueued || t.State == StateDownloading }

// live : progression lue chez Grabscher, gardée en mémoire seulement.
type live struct {
	at        time.Time
	current   int64
	speed     int64
	eta       time.Duration
	transfer  bool
	botOnline bool
	botState  int
	queuePos  int
	botMsg    string
}

// Service est la source XDCC active.
type Service struct {
	Settings Settings
	Client   *Client
	DB       *sql.DB
	Log      *slog.Logger
	Now      func() time.Time
	// Protected indique un fichier (chemin vu par Remix) appartenant à une
	// bibliothèque locale : jamais effacé.
	Protected func(local string) bool
	// Usage mesure la place libre (datafs.Usage par défaut).
	Usage func(path string) (total, free int64, err error)

	mu       sync.Mutex
	tasks    map[string]*Task
	lives    map[string]*live
	readers  map[string]int
	fails    map[string]failure // paquet ou « bot:<nom> »
	bots     map[string]botCache
	complete string // dossier de téléchargement vu par Grabscher
	compAt   time.Time
	jobsAt   time.Time
	jobsErr  error
	lastErr  string
	lastErrT time.Time
	cleanAt  time.Time
	loaded   bool

	server *streamServer
	stop   chan struct{}
	done   chan struct{}
	kick   chan struct{}
}

type failure struct {
	at     time.Time
	reason string
}

type botCache struct {
	at  time.Time
	bot Bot
	err error
}

// Délais internes.
const (
	failMemory    = 24 * time.Hour   // paquet ou bot en échec ignoré par la recherche
	retryAfter    = 15 * time.Minute // une lecture ne relance pas un échec plus tôt
	botTTL        = 2 * time.Minute
	jobsTTL       = time.Second
	completeTTL   = 10 * time.Minute
	cleanEvery    = 5 * time.Minute
	readingWindow = 10 * time.Minute // un fichier lu récemment n'est pas effacé
)

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

// Start charge les tâches enregistrées et lance le suivi.
func (s *Service) Start() {
	s.mu.Lock()
	s.init()
	s.mu.Unlock()
	s.load(context.Background())
	s.stop, s.done, s.kick = make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	go s.loop()
}

func (s *Service) init() {
	if s.tasks == nil {
		s.tasks, s.lives, s.readers = map[string]*Task{}, map[string]*live{}, map[string]int{}
		s.fails, s.bots = map[string]failure{}, map[string]botCache{}
	}
	if s.server == nil {
		s.server = &streamServer{svc: s}
	}
}

// Close arrête le suivi et le serveur local. Les téléchargements continuent
// chez Grabscher ; ils sont repris au prochain démarrage.
func (s *Service) Close() {
	if s.stop != nil {
		close(s.stop)
		<-s.done
		s.stop = nil
	}
	s.mu.Lock()
	srv := s.server
	s.mu.Unlock()
	if srv != nil {
		srv.Close()
	}
}

func (s *Service) wake() {
	if s.kick != nil {
		select {
		case s.kick <- struct{}{}:
		default:
		}
	}
}

func (s *Service) loop() {
	defer close(s.done)
	for {
		wait := 30 * time.Second
		if s.hasActive() {
			wait = 3 * time.Second
		}
		select {
		case <-s.stop:
			return
		case <-s.kick:
		case <-time.After(wait):
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_ = s.refresh(ctx, true)
		s.clean(ctx, false)
		cancel()
	}
}

func (s *Service) hasActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tasks {
		if t.active() {
			return true
		}
	}
	return false
}

// --- persistance ---

func (s *Service) load(ctx context.Context) {
	if s.DB == nil {
		return
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT data FROM pse_xdcc_tasks")
	if err != nil {
		s.log().Warn("tâches XDCC illisibles", "err", err)
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
		if json.Unmarshal([]byte(raw), &t) == nil && t.Packet != "" {
			s.tasks[t.Packet] = &t
		}
	}
	s.loaded = true
}

func (s *Service) save(ctx context.Context, t Task) {
	if s.DB == nil {
		return
	}
	raw, _ := json.Marshal(t)
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO pse_xdcc_tasks(packet,data) VALUES(?,?) ON CONFLICT(packet) DO UPDATE SET data=excluded.data", t.Packet, string(raw)); err != nil {
		s.log().Warn("tâche XDCC non enregistrée", "err", err)
	}
}

// --- état chez Grabscher ---

// refresh relit la file et l'historique de Grabscher (au plus une fois par
// seconde, sauf force), puis la progression des tâches actives.
func (s *Service) refresh(ctx context.Context, force bool) error {
	s.mu.Lock()
	if !force && s.now().Sub(s.jobsAt) < jobsTTL {
		err := s.jobsErr
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	jobs, err := s.Client.Jobs(ctx)
	s.mu.Lock()
	s.jobsAt, s.jobsErr = s.now(), err
	if err != nil {
		s.noteErr(err)
		s.mu.Unlock()
		return err
	}
	var changed []Task
	var actives []*Task
	for _, t := range s.tasks {
		if !t.active() || t.NZO == "" {
			continue
		}
		j, ok := jobs[t.NZO]
		prev := *t
		switch {
		case !ok:
			t.State, t.Fail = StateFailed, "tâche disparue de Grabscher"
			t.Done = s.now()
		case j.Status == "Completed":
			t.State, t.Storage, t.Done = StateCompleted, j.Storage, s.now()
			if !j.Done.IsZero() {
				t.Done = j.Done
			}
			if j.Size > 0 {
				t.Size = j.Size
			}
		case j.Status == "Failed":
			t.State, t.Fail, t.Done = StateFailed, j.Fail, s.now()
			s.rememberFailure(t, j.Fail)
		case j.Status == "Downloading" || j.Status == "Moving":
			t.State = StateDownloading
		default:
			t.State = StateQueued
		}
		if prev.State != t.State || prev.Storage != t.Storage {
			changed = append(changed, *t)
			if t.State == StateCompleted {
				s.log().Info("téléchargement XDCC terminé", "name", t.Name, "bot", t.Bot)
			} else if t.State == StateFailed {
				s.log().Warn("téléchargement XDCC en échec", "name", t.Name, "bot", t.Bot, "reason", clip(t.Fail, 200))
			}
		}
		if t.active() {
			actives = append(actives, t)
		}
	}
	type probe struct{ packet, bot string }
	var probes []probe
	for _, t := range actives {
		probes = append(probes, probe{t.Packet, t.BotGUID})
	}
	s.mu.Unlock()
	for _, t := range changed {
		s.save(ctx, t)
	}
	for _, p := range probes {
		l := &live{at: s.now()}
		if pk, err := s.Client.Packet(ctx, p.packet); err == nil {
			l.current, l.speed, l.transfer = pk.CurrentSize, pk.Speed, pk.Connected
			l.eta = time.Duration(pk.TimeMissing) * time.Second
			// Le bot annonce une taille arrondie (« 45M ») ; Grabscher passe
			// à la taille exacte de l'offre DCC dès le début du transfert.
			if pk.Size > 0 && (pk.Connected || pk.CurrentSize > 0) {
				s.mu.Lock()
				t := s.tasks[p.packet]
				var cp Task
				exact := t != nil && t.Size != pk.Size
				if exact {
					t.Size = pk.Size
					cp = *t
				}
				s.mu.Unlock()
				if exact {
					s.save(ctx, cp)
				}
			}
		}
		if b, err := s.bot(ctx, p.bot, true); err == nil {
			l.botOnline, l.botState, l.queuePos, l.botMsg = b.Connected, b.State, b.QueuePosition, b.LastMessage
		}
		s.mu.Lock()
		s.lives[p.packet] = l
		s.mu.Unlock()
	}
	return nil
}

func (s *Service) noteErr(err error) {
	s.lastErr, s.lastErrT = err.Error(), s.now()
}

// rememberFailure écarte le paquet de la recherche, et le bot entier quand
// l'échec vient de lui (DCC passif impossible, silence).
func (s *Service) rememberFailure(t *Task, reason string) {
	s.fails[t.Packet] = failure{at: s.now(), reason: reason}
	r := strings.ToLower(reason)
	if t.Bot != "" && (strings.Contains(r, "passive") || strings.Contains(r, "did not answer") || strings.Contains(r, "offline") || strings.Contains(r, "denied")) {
		s.fails["bot:"+strings.ToLower(t.Bot)] = failure{at: s.now(), reason: reason}
	}
}

func (s *Service) failed(key string) (failure, bool) {
	f, ok := s.fails[key]
	if !ok || s.now().Sub(f.at) > failMemory {
		return failure{}, false
	}
	return f, true
}

// bot lit un bot, avec un court cache.
func (s *Service) bot(ctx context.Context, guid string, fresh bool) (Bot, error) {
	if guid == "" {
		return Bot{}, ErrNotFound
	}
	s.mu.Lock()
	c, ok := s.bots[guid]
	s.mu.Unlock()
	ttl := botTTL
	if fresh {
		ttl = 2 * time.Second
	}
	if ok && s.now().Sub(c.at) < ttl {
		return c.bot, c.err
	}
	b, err := s.Client.Bot(ctx, guid)
	s.mu.Lock()
	if len(s.bots) > 5000 {
		s.bots = map[string]botCache{}
	}
	s.bots[guid] = botCache{at: s.now(), bot: b, err: err}
	s.mu.Unlock()
	return b, err
}

// completeDir renvoie le dossier de téléchargement de Grabscher.
func (s *Service) completeDir(ctx context.Context) (string, error) {
	s.mu.Lock()
	if s.complete != "" && s.now().Sub(s.compAt) < completeTTL {
		d := s.complete
		s.mu.Unlock()
		return d, nil
	}
	s.mu.Unlock()
	d, err := s.Client.CompleteDir(ctx)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.complete, s.compAt = d, s.now()
	s.mu.Unlock()
	return d, nil
}

// tempDir : dossier temporaire de Grabscher, vu par Remix.
func (s *Service) tempDir(ctx context.Context) (string, bool) {
	if s.Settings.TempDir != "" {
		return s.Settings.TempDir, true
	}
	d, err := s.completeDir(ctx)
	if err != nil || d == "" {
		return "", false
	}
	return s.Settings.Local(parentTmp(d))
}

// parentTmp : Grabscher range ses fichiers partiels dans « tmp », à côté
// de son dossier de téléchargement « dl » (réglages par défaut, Docker
// compris : /config/.config/XG/{dl,tmp}).
func parentTmp(complete string) string {
	return path.Join(path.Dir(path.Clean(complete)), "tmp")
}

// streamable indique si la lecture progressive est possible maintenant :
// mode qui l'autorise et dossier temporaire visible depuis Remix.
func (s *Service) streamable(ctx context.Context) bool {
	if !s.Settings.Progressive() {
		return false
	}
	d, ok := s.tempDir(ctx)
	return ok && isDir(d)
}

// --- lancement ---

// Errors de lancement (enveloppées dans une notice pour le spectateur).
var (
	errDaily  = errors.New("xdcc: plafond de lancements du jour atteint")
	errActive = errors.New("xdcc: trop de téléchargements XDCC en cours")
	errSpace  = errors.New("xdcc: place libre insuffisante")
	errCache  = errors.New("xdcc: cache XDCC plein")
)

// startOptions décrit un lancement.
type startOptions struct {
	keep   bool
	manual bool
	name   string // nom attendu (contrôle anti-réutilisation des numéros)
	size   int64
}

// ensure renvoie la tâche du paquet, en la créant si allow le permet.
func (s *Service) ensure(ctx context.Context, packet string, allow bool, o startOptions) (*Task, error) {
	s.mu.Lock()
	s.init()
	t := s.tasks[packet]
	if t != nil {
		switch {
		case t.active():
			if o.keep && !t.Keep {
				t.Keep = true
				cp := *t
				s.mu.Unlock()
				s.save(ctx, cp)
				return t, nil
			}
			s.mu.Unlock()
			return t, nil
		case t.State == StateCompleted:
			if p, ok := s.Settings.Local(t.Storage); ok && localSize(p) == t.Size {
				s.mu.Unlock()
				return t, nil
			}
		case t.State == StateFailed && !o.manual && s.now().Sub(t.Done) < retryAfter:
			cp := *t
			s.mu.Unlock()
			return &cp, nil
		}
	}
	if !allow {
		s.mu.Unlock()
		return nil, fmt.Errorf("xdcc: un téléchargement ne démarre que pour une lecture : %w", core.ErrForegroundOnly)
	}
	if err := s.admit(o); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()

	pk, err := s.Client.Packet(ctx, packet)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, notice(fmt.Errorf("xdcc: paquet retiré du bot : %w", core.ErrSourceUnavailable),
				"Ce fichier n'est plus proposé par le bot XDCC.", "This file is no longer offered by the XDCC bot.")
		}
		return nil, fmt.Errorf("%w: %w", core.ErrSourceUnavailable, err)
	}
	if (o.name != "" && pk.Name != o.name) || (o.size > 0 && !sameSize(pk.Size, o.size)) {
		return nil, notice(fmt.Errorf("xdcc: le bot propose un autre fichier sous ce numéro : %w", core.ErrSourceUnavailable),
			"Le bot XDCC propose désormais un autre fichier sous ce numéro.", "The XDCC bot now offers another file under this number.")
	}
	b, err := s.bot(ctx, pk.BotGUID, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", core.ErrSourceUnavailable, err)
	}
	if !b.Connected {
		return nil, notice(fmt.Errorf("xdcc: bot %s hors ligne : %w", b.Name, core.ErrSourceUnavailable),
			"Le bot XDCC "+b.Name+" est hors ligne.", "The XDCC bot "+b.Name+" is offline.")
	}
	if s.Settings.Excluded(b.Name) {
		return nil, fmt.Errorf("xdcc: bot %s exclu : %w", b.Name, core.ErrReleaseRejected)
	}
	if err := s.checkSpace(ctx, pk.Size, o.keep); err != nil {
		return nil, err
	}
	cat := CategoryCache
	if o.keep {
		cat = CategoryKeep
	}
	nzo, err := s.Client.Grab(ctx, packet, cat)
	if err != nil {
		s.mu.Lock()
		s.noteErr(err)
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %w", core.ErrDownloadRefused, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur := s.tasks[packet]; cur != nil && cur.active() && cur.NZO == nzo {
		return cur, nil // un autre spectateur l'a lancé entre-temps
	}
	t = &Task{Packet: packet, NZO: nzo, Name: pk.Name, Size: pk.Size, Bot: b.Name, BotGUID: b.GUID, Keep: o.keep, Manual: o.manual,
		State: StateQueued, Created: s.now()}
	s.tasks[packet] = t
	delete(s.fails, packet)
	s.log().Info("téléchargement XDCC demandé", "name", t.Name, "bot", t.Bot, "keep", t.Keep, "manual", t.Manual)
	cp := *t
	go s.save(context.WithoutCancel(ctx), cp)
	s.wake()
	return t, nil
}

// sameSize compare deux tailles d'un même paquet : celle annoncée par le
// bot est arrondie (« 1.4G »), l'exacte n'est connue qu'au transfert.
func sameSize(a, b int64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= max(a, b)/20 // 5 %
}

// admit applique les plafonds (verrou tenu).
func (s *Service) admit(o startOptions) error {
	active, today := 0, 0
	day := s.now().Format("2006-01-02")
	for _, t := range s.tasks {
		if t.active() {
			active++
		}
		if t.Created.Format("2006-01-02") == day {
			today++
		}
	}
	if active >= s.Settings.MaxActive {
		return notice(fmt.Errorf("%w (%d) : %w", errActive, active, core.ErrDownloadLimit),
			fmt.Sprintf("Déjà %d téléchargement(s) XDCC en cours : réessayez une fois l'un d'eux terminé.", active),
			fmt.Sprintf("%d XDCC download(s) already running: try again once one finishes.", active))
	}
	if !o.manual && today >= s.Settings.MaxPerDay {
		return notice(fmt.Errorf("%w (%d) : %w", errDaily, today, core.ErrDownloadLimit),
			"Plafond de téléchargements XDCC du jour atteint.", "Today's XDCC download limit is reached.")
	}
	return nil
}

// checkSpace contrôle la place libre et la taille du cache.
func (s *Service) checkSpace(ctx context.Context, size int64, keep bool) error {
	if d, err := s.completeDir(ctx); err == nil {
		if local, ok := s.Settings.Local(d); ok && s.Settings.MinFree > 0 {
			usage := s.Usage
			if usage == nil {
				usage = datafs.Usage
			}
			if _, free, err := usage(local); err == nil && free-size < s.Settings.MinFree {
				return notice(fmt.Errorf("%w : %w", errSpace, core.ErrLocalNoSpace),
					"Plus assez de place pour le téléchargement XDCC : il n'a pas été lancé.", "Not enough free space for the XDCC download: it was not started.")
			}
		}
	}
	if keep || s.Settings.CacheBytes <= 0 {
		return nil
	}
	if s.cacheUsed()+size > s.Settings.CacheBytes {
		s.clean(ctx, true) // libère d'abord les fichiers expirés ou les plus anciens
		if s.cacheUsed()+size > s.Settings.CacheBytes {
			return notice(fmt.Errorf("%w : %w", errCache, core.ErrDownloadLimit),
				"Le cache XDCC est plein (fichiers en cours de lecture) : réessayez plus tard.", "The XDCC cache is full (files being read): try again later.")
		}
	}
	return nil
}

// cacheUsed : octets des tâches en cache (en cours ou complètes).
func (s *Service) cacheUsed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, t := range s.tasks {
		if !t.Keep && (t.active() || t.State == StateCompleted) {
			n += t.Size
		}
	}
	return n
}

// --- console ---

// Grab lance un téléchargement depuis la console (gardé ou non).
func (s *Service) Grab(ctx context.Context, packet string, keep bool) (*Task, error) {
	return s.ensure(ctx, packet, true, startOptions{keep: keep, manual: true})
}

// Cancel arrête une tâche en cours ; Grabscher efface le fichier partiel.
func (s *Service) Cancel(ctx context.Context, packet string) error {
	s.mu.Lock()
	t := s.tasks[packet]
	if t == nil || !t.active() {
		s.mu.Unlock()
		return errors.New("xdcc: aucun téléchargement en cours pour ce paquet")
	}
	if s.readers[packet] > 0 {
		s.mu.Unlock()
		return errors.New("xdcc: fichier en cours de lecture")
	}
	nzo := t.NZO
	s.mu.Unlock()
	if err := s.Client.Cancel(ctx, nzo); err != nil {
		return err
	}
	s.mu.Lock()
	t.State, t.Done, t.Fail = StateCancelled, s.now(), ""
	cp := *t
	s.mu.Unlock()
	s.save(ctx, cp)
	s.log().Info("téléchargement XDCC arrêté depuis la console", "name", cp.Name)
	return nil
}

// Keep garde un fichier du cache : il n'est plus jamais effacé par Remix.
func (s *Service) Keep(ctx context.Context, packet string) error {
	s.mu.Lock()
	t := s.tasks[packet]
	if t == nil {
		s.mu.Unlock()
		return errors.New("xdcc: paquet inconnu")
	}
	t.Keep = true
	cp := *t
	s.mu.Unlock()
	s.save(ctx, cp)
	return nil
}

// --- conservation du cache ---

// clean (toutes les cleanEvery, ou tout de suite avec force) efface les
// fichiers du cache expirés, puis les plus anciens tant
// que le cache dépasse sa taille. Seulement des tâches Remix non gardées,
// complètes, hors lecture, hors bibliothèque ; l'effacement passe par
// Grabscher, qui n'efface que le fichier de la tâche dans son dossier.
func (s *Service) clean(ctx context.Context, force bool) {
	s.mu.Lock()
	every := cleanEvery
	if force {
		every = time.Second
	}
	if s.now().Sub(s.cleanAt) < every {
		s.mu.Unlock()
		return
	}
	s.cleanAt = s.now()
	now := s.now()
	var cands []*Task
	var used int64
	for _, t := range s.tasks {
		if t.Keep || (t.State != StateCompleted && !t.active()) {
			continue
		}
		used += t.Size
		if t.State != StateCompleted || s.readers[t.Packet] > 0 || now.Sub(t.LastRead) < readingWindow {
			continue
		}
		cands = append(cands, t)
	}
	sort.Slice(cands, func(i, j int) bool { return lastUse(cands[i]).Before(lastUse(cands[j])) })
	var drop []Task
	for _, t := range cands {
		expired := now.Sub(lastUse(t)) > s.Settings.Retention
		over := s.Settings.CacheBytes > 0 && used > s.Settings.CacheBytes
		if !expired && !over {
			continue
		}
		used -= t.Size
		drop = append(drop, *t)
	}
	s.mu.Unlock()
	for _, t := range drop {
		local, ok := s.Settings.Local(t.Storage)
		if !ok || (s.Protected != nil && s.Protected(local)) {
			s.log().Info("fichier XDCC gardé : il appartient à une bibliothèque ou est hors des dossiers partagés", "name", t.Name)
			s.markKept(ctx, t.Packet)
			continue
		}
		if err := s.Client.Forget(ctx, t.NZO, true); err != nil {
			s.log().Warn("fichier XDCC du cache non effacé", "name", t.Name, "err", err)
			continue
		}
		s.mu.Lock()
		if cur := s.tasks[t.Packet]; cur != nil && cur.NZO == t.NZO {
			cur.State = StateRemoved
			t = *cur
		}
		s.mu.Unlock()
		s.save(ctx, t)
		s.log().Info("fichier XDCC du cache effacé", "name", t.Name)
	}
}

func (s *Service) markKept(ctx context.Context, packet string) {
	_ = s.Keep(ctx, packet)
}

func lastUse(t *Task) time.Time {
	if t.LastRead.After(t.Done) {
		return t.LastRead
	}
	return t.Done
}
