package alko

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/langtag"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// Publisher écrit les fichiers vérifiés dans les releases d'Ether
// (releasedb.AlkoPublisher).
type Publisher interface {
	Publish(ctx context.Context, files []releasedb.AlkoFile, langFR bool) (releasedb.AlkoResult, error)
	Refresh(ctx context.Context, keys []core.RatingKey) error
	Forget(ctx context.Context, gone []releasedb.AlkoGone) error
	ForgetAll(ctx context.Context) error
	Touch(ctx context.Context) error
	DropPostgresDuplicates(ctx context.Context) (int, error)
}

const (
	startDelay = 30 * time.Second
	pagePause  = 100 * time.Millisecond
	idleWait   = 5 * time.Second
	// Un journal invalide plus de deux cycles impose une relecture complète, au
	// plus une fois par jalon : une image SFR sans journal ne doit pas relire
	// 380 000 fichiers toutes les demi-heures.
	journalGapCycles = 2
	fullMinGap       = 6 * time.Hour
	touchEvery       = 12 * time.Hour
	dirtyFlushPages  = 40

	// Rattrapage des titres arrivés au catalogue après la synchronisation : un
	// titre jamais vérifié est repris d'abord. Chaque passage est borné.
	catchBatch   = 500
	catchBatches = 40
	catchBudget  = 2 * time.Minute
	catchRecheck = 30 * 24 * time.Hour
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// errRestart : l'époque a changé pendant la lecture, il faut la recommencer.
var errRestart = errors.New("relecture à recommencer")

// Service est la source Alkodiques active : miroir, synchronisation, recherche.
type Service struct {
	Settings  Settings
	DB        *sql.DB
	Log       *slog.Logger
	Now       func() time.Time
	Seal      func(string) (string, error)
	Open      func(string) (string, error)
	Publisher Publisher
	// Idle attend la fin des lectures en cours ; nil : jamais d'attente.
	Idle func(context.Context) error
	// HTTP remplace le client réseau (tests).
	HTTP *http.Client
	// Pause sépare deux pages ; nil : 100 ms.
	Pause *time.Duration
	// Delay précède la première synchronisation ; nil : 30 s.
	Delay *time.Duration

	syncMu sync.Mutex
	clMu   sync.Mutex
	cl     *Client
	mu     sync.Mutex
	prog   progress
	dirty  map[core.RatingKey]struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	wg     sync.WaitGroup
}

// progress est l'avancement affiché dans la console.
type progress struct {
	running bool
	phase   string // full, incremental, publish
	read    int
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

// st donne accès au miroir.
func (s *Service) st() Store { return Store{DB: s.DB} }

// client renvoie le client réseau du service, construit une fois (connexions
// réutilisées) et reconstruit seulement si l'adresse ou la clé changent.
func (s *Service) client() *Client {
	s.clMu.Lock()
	defer s.clMu.Unlock()
	if s.cl == nil || s.cl.Settings.URL != s.Settings.URL || s.cl.Settings.Key != s.Settings.Key {
		s.cl = &Client{Settings: s.Settings, HTTP: s.HTTP}
	}
	return s.cl
}

func (s *Service) pause(ctx context.Context) bool {
	d := pagePause
	if s.Pause != nil {
		d = *s.Pause
	}
	if d <= 0 {
		return ctx.Err() == nil
	}
	return sleep(ctx, d)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// idle attend la fin des lectures en cours : une lecture passe avant la synchronisation.
func (s *Service) idle(ctx context.Context) error {
	if s.Idle != nil {
		if err := s.Idle(ctx); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// Start lance la synchronisation périodique.
func (s *Service) Start() error {
	if s.DB == nil {
		return errors.New("base indisponible")
	}
	s.loadReady()
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.wake = make(chan struct{}, 1)
	s.wg.Add(1)
	go s.loop()
	return nil
}

// loadReady rétablit, après un redémarrage, l'état « miroir rempli » : au moins
// une lecture complète réussie, jamais effacée par un changement de serveur.
func (s *Service) loadReady() {
	ctx := context.Background()
	at, _ := s.st().int(ctx, "last_full")
	seq, _ := s.st().get(ctx, "seq")
	releasedb.SetAlkoMirrorReady(at > 0 && seq != "")
}

// Close arrête la synchronisation.
func (s *Service) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

// Wake demande une synchronisation immédiate.
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) loop() {
	defer s.wg.Done()
	wait := startDelay
	if s.Delay != nil {
		wait = *s.Delay
	}
	for {
		t := time.NewTimer(wait)
		select {
		case <-s.ctx.Done():
			t.Stop()
			return
		case <-s.wake:
			t.Stop()
		case <-t.C:
		}
		wait = s.Settings.Every
		if err := s.Sync(s.ctx); err != nil && s.ctx.Err() == nil {
			s.log().Warn("synchronisation Alkodiques impossible", "code", ErrorCode(err))
		}
	}
}

// ErrorCode donne un code générique (sans adresse, clé ni lien) pour la console.
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrKey):
		return "key"
	case errors.Is(err, ErrDisabled):
		return "old"
	case errors.Is(err, ErrViews):
		return "views"
	case errors.Is(err, ErrParams):
		return "params"
	case errors.Is(err, ErrProtocol):
		return "protocol"
	case errors.Is(err, ErrUnreachable):
		return "unreachable"
	}
	return "internal"
}

// Sync fait un passage : lecture complète si nécessaire, sinon suivi du
// journal ; puis rattrapage des titres du catalogue.
func (s *Service) Sync(ctx context.Context) (err error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.setRunning(true, "")
	defer func() {
		s.setRunning(false, "")
		_ = s.st().set(context.WithoutCancel(ctx), "error", ErrorCode(err))
		if err == nil {
			_ = s.st().setInt(context.WithoutCancel(ctx), "last", s.now().Unix())
		}
	}()
	if err = s.idle(ctx); err != nil {
		return err
	}
	cl := s.client()
	st, err := cl.State(ctx)
	if err != nil {
		return err
	}
	if err = s.checkSource(ctx); err != nil {
		return err
	}
	full, waiting, err := s.decide(ctx, st)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		switch {
		case full:
			err = s.fullRead(ctx, cl)
		case !waiting && st.LastSeq != s.seqOr(ctx, st.LastSeq):
			full, err = s.incremental(ctx, cl)
			if err == nil && full {
				continue // journal périmé (410, époque…) : tout relire
			}
		}
		if errors.Is(err, errRestart) && attempt == 0 {
			full, err = true, nil
			continue
		}
		break
	}
	if err != nil {
		return err
	}
	if err = s.catchUp(ctx); err != nil {
		return err
	}
	return s.finish(ctx, st)
}

func (s *Service) seqOr(ctx context.Context, def int64) int64 {
	v, _ := s.st().get(ctx, "seq")
	if v == "" {
		return def
	}
	n, _ := strconv.ParseInt(v, 10, 64)
	return n
}

func (s *Service) setRunning(on bool, phase string) {
	s.mu.Lock()
	s.prog.running, s.prog.phase = on, phase
	if !on {
		s.prog.read = 0
	}
	s.mu.Unlock()
}

func (s *Service) setPhase(phase string, read int) {
	s.mu.Lock()
	s.prog.phase, s.prog.read = phase, read
	s.mu.Unlock()
}

// checkSource repart de zéro quand le serveur lu a changé, et réévalue les
// titres quand les règles de publication ont changé.
func (s *Service) checkSource(ctx context.Context) error {
	sig, err := s.st().get(ctx, "sig")
	if err != nil {
		return err
	}
	want := s.Settings.sourceSignature()
	if sig != want {
		if sig != "" && s.Publisher != nil {
			if err := s.Publisher.ForgetAll(ctx); err != nil {
				return err
			}
		}
		if err := s.st().reset(ctx); err != nil {
			return err
		}
		releasedb.SetAlkoMirrorReady(false)
		if err := s.st().set(ctx, "sig", want); err != nil {
			return err
		}
	}
	pub, err := s.st().get(ctx, "pub_sig")
	if err != nil {
		return err
	}
	if pub != s.Settings.publishSignature() {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM pse_alko_checked`); err != nil {
			return err
		}
		return s.st().set(ctx, "pub_sig", s.Settings.publishSignature())
	}
	return nil
}

// decide choisit entre lecture complète, suivi du journal et attente.
func (s *Service) decide(ctx context.Context, st State) (full, waiting bool, err error) {
	seqStr, err := s.st().get(ctx, "seq")
	if err != nil {
		return false, false, err
	}
	inProgress, err := s.st().int(ctx, "full_gen")
	if err != nil {
		return false, false, err
	}
	seq, _ := strconv.ParseInt(seqStr, 10, 64)
	epoch, _ := s.st().int(ctx, "epoch")
	if seqStr == "" || inProgress != 0 || st.Epoch != epoch || st.PurgeSeq > seq || st.LastSeq < seq {
		return true, false, nil
	}
	if st.JournalValid() {
		return false, false, s.st().setInt(ctx, "gap", 0)
	}
	// Journal inactif ou en retard : des lots ont pu être appliqués sans
	// journal. Attendre ; si l'écart dure, relire tout (au plus toutes les 6 h).
	gap, _ := s.st().int(ctx, "gap")
	gap++
	if err := s.st().setInt(ctx, "gap", gap); err != nil {
		return false, false, err
	}
	lastFull, _ := s.st().int(ctx, "last_full")
	if gap > journalGapCycles && s.now().Sub(time.Unix(lastFull, 0)) >= fullMinGap {
		return true, false, nil
	}
	return false, true, nil
}

func (s *Service) finish(ctx context.Context, st State) error {
	if err := s.flushDirty(ctx); err != nil {
		return err
	}
	c, err := s.st().counts(ctx)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(c)
	if err := s.st().set(ctx, "counts", string(raw)); err != nil {
		return err
	}
	journal := "waiting"
	switch {
	case !st.Journal:
		journal = "inactive"
	case st.JournalValid():
		journal = "ok"
	}
	if err := s.st().set(ctx, "journal", journal); err != nil {
		return err
	}
	if s.Publisher != nil {
		if at, _ := s.st().int(ctx, "touch"); s.now().Sub(time.Unix(at, 0)) >= touchEvery {
			if err := s.Publisher.Touch(ctx); err != nil {
				return err
			}
			if err := s.st().setInt(ctx, "touch", s.now().Unix()); err != nil {
				return err
			}
		}
	}
	s.log().Info("synchronisation Alkodiques terminée", "fichiers", c.Files, "films", c.Movies, "épisodes", c.Episodes, "publiés", c.Published)
	return nil
}

// fullRead lit tout le catalogue, page par page, dans une nouvelle génération,
// puis retire les fichiers absents. Elle reprend là où elle s'est arrêtée.
func (s *Service) fullRead(ctx context.Context, cl *Client) error {
	st := s.st()
	gen, err := st.int(ctx, "full_gen")
	if err != nil {
		return err
	}
	after, _ := st.get(ctx, "full_after")
	if gen == 0 {
		// Une génération jamais utilisée : une tentative avortée a pu marquer des
		// lignes avec gen+1, qui ne seraient alors jamais purgées.
		cur, _ := st.int(ctx, "gen")
		var used int64
		if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(gen),0) FROM pse_alko_files`).Scan(&used); err != nil {
			return err
		}
		gen, after = max(cur, used)+1, ""
		for k, v := range map[string]string{"full_gen": strconv.FormatInt(gen, 10), "full_after": "", "full_state": "", "full_read": "0"} {
			if err := st.set(ctx, k, v); err != nil {
				return err
			}
		}
	}
	read, _ := st.int(ctx, "full_read")
	s.setPhase("full", int(read))
	for pages := 1; ; pages++ {
		if err := s.idle(ctx); err != nil {
			return err
		}
		page, err := cl.Catalogue(ctx, after, PageSize)
		if err != nil {
			return err
		}
		if after == "" {
			raw, _ := json.Marshal(page.State)
			if err := st.set(ctx, "full_state", string(raw)); err != nil {
				return err
			}
		} else {
			var first State
			if v, _ := st.get(ctx, "full_state"); json.Unmarshal([]byte(v), &first) != nil || first.Epoch != page.State.Epoch {
				_ = st.setInt(ctx, "full_gen", 0)
				return errRestart // restauration complète côté SFR pendant la lecture
			}
		}
		if err := s.apply(ctx, page.Elements, gen); err != nil {
			return err
		}
		read += int64(len(page.Elements))
		last := ""
		if page.Next != nil {
			last = strings.ToLower(*page.Next)
		}
		if err := st.setInt(ctx, "full_read", read); err != nil {
			return err
		}
		if err := st.set(ctx, "full_after", last); err != nil {
			return err
		}
		s.setPhase("full", int(read))
		if pages%dirtyFlushPages == 0 {
			if err := s.flushDirty(ctx); err != nil {
				return err
			}
		}
		if last == "" || len(page.Elements) == 0 {
			break
		}
		after = last
		if !s.pause(ctx) {
			return ctx.Err()
		}
	}
	// Fichiers absents du catalogue : supprimés côté SFR.
	for {
		if err := s.idle(ctx); err != nil {
			return err
		}
		stale, err := st.stale(ctx, gen, 500)
		if err != nil {
			return err
		}
		if len(stale) == 0 {
			break
		}
		ids := make([]string, len(stale))
		var gone []releasedb.AlkoGone
		for i, r := range stale {
			ids[i] = r.ID
			if r.RelID != "" {
				gone = append(gone, releasedb.AlkoGone{PlaybackID: r.ID, ReleaseID: r.RelID, TitleKey: core.RatingKey(r.TitleKey)})
			}
		}
		if s.Publisher != nil {
			if err := s.Publisher.Forget(ctx, gone); err != nil {
				return err
			}
		}
		if err := st.delete(ctx, ids); err != nil {
			return err
		}
	}
	var first State
	if v, _ := st.get(ctx, "full_state"); json.Unmarshal([]byte(v), &first) != nil {
		return errRestart
	}
	for k, v := range map[string]int64{"gen": gen, "seq": first.LastSeq, "epoch": first.Epoch, "full_gen": 0, "gap": 0, "last_full": s.now().Unix()} {
		if err := st.setInt(ctx, k, v); err != nil {
			return err
		}
	}
	_ = st.set(ctx, "full_after", "")
	releasedb.SetAlkoMirrorReady(true)
	// Le miroir est rempli : les anciennes lignes PostgreSQL des Alkodiques sont
	// des doublons. Les torrents SFR ne sont pas touchés.
	if s.Publisher != nil {
		if n, err := s.Publisher.DropPostgresDuplicates(ctx); err != nil {
			return err
		} else if n > 0 {
			s.log().Info("anciennes lignes PostgreSQL des Alkodiques retirées", "releases", n)
		}
	}
	return nil
}

// incremental suit le journal depuis le repère. full : il faut tout relire.
func (s *Service) incremental(ctx context.Context, cl *Client) (full bool, err error) {
	st := s.st()
	since := s.seqOr(ctx, 0)
	epoch, _ := st.int(ctx, "epoch")
	gen, _ := st.int(ctx, "gen")
	s.setPhase("incremental", 0)
	for {
		if err := s.idle(ctx); err != nil {
			return false, err
		}
		page, err := cl.Modifications(ctx, since, ModificationsPage)
		if errors.Is(err, ErrFullReread) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if page.State.Epoch != epoch || page.State.PurgeSeq > since || page.State.LastSeq < since {
			return true, nil
		}
		var ids []string
		seen := map[string]bool{}
		last := since
		for _, m := range page.Modifications {
			last = max(last, m.Seq)
			id := strings.ToLower(m.PlaybackID)
			if uuidRE.MatchString(id) && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if err := s.reread(ctx, cl, ids, gen); err != nil {
			return false, err
		}
		if page.Next != nil {
			since = *page.Next
			if err := st.setInt(ctx, "seq", since); err != nil {
				return false, err
			}
			if !s.pause(ctx) {
				return false, ctx.Err()
			}
			continue
		}
		// Dernière page : le repère ne saute à last_seq que si le journal est
		// valide dans ce même cliché ; sinon il reste au dernier seq lu.
		next := last
		if page.State.JournalValid() {
			next = page.State.LastSeq
		}
		return false, st.setInt(ctx, "seq", next)
	}
}

// reread relit des fichiers par identifiant : présent, il est mis à jour ;
// absent, il est supprimé (jamais de suppression aveugle sur un « D »).
func (s *Service) reread(ctx context.Context, cl *Client, ids []string, gen int64) error {
	for chunk := range slices.Chunk(ids, FilesBatch) {
		_, elems, err := cl.Files(ctx, chunk)
		if err != nil {
			return err
		}
		present := map[string]bool{}
		for _, e := range elems {
			if r, ok := toRow(e); ok {
				present[r.ID] = true
			}
		}
		if err := s.apply(ctx, elems, gen); err != nil {
			return err
		}
		var absent []string
		for _, id := range chunk {
			if !present[id] {
				absent = append(absent, id)
			}
		}
		if err := s.remove(ctx, absent); err != nil {
			return err
		}
	}
	return nil
}

// remove retire des fichiers du miroir et leur publication.
func (s *Service) remove(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	prior, err := s.st().prior(ctx, ids)
	if err != nil {
		return err
	}
	var gone []releasedb.AlkoGone
	for id, p := range prior {
		if p.RelID != "" {
			gone = append(gone, releasedb.AlkoGone{PlaybackID: id, ReleaseID: p.RelID, TitleKey: core.RatingKey(p.TitleKey)})
		}
	}
	if s.Publisher != nil {
		if err := s.Publisher.Forget(ctx, gone); err != nil {
			return err
		}
	}
	return s.st().delete(ctx, ids)
}

// toRow valide un élément de l'API. Un élément invalide est traité comme absent.
func toRow(e Element) (row, bool) {
	id := strings.ToLower(e.PlaybackID)
	if !uuidRE.MatchString(id) || (e.Kind != "movie" && e.Kind != "episode") || e.TMDB <= 0 || e.Filesize <= 0 ||
		strings.TrimSpace(e.Filename) == "" || !core.ValidAllDebridFileURL(e.SourceURL) {
		return row{}, false
	}
	r := row{ID: id, Kind: e.Kind, TMDB: e.TMDB, Filename: e.Filename, Size: e.Filesize, Fingerprint: e.Fingerprint, SyncedAt: e.SyncedAt}
	if e.Kind == "episode" {
		if e.Season != nil {
			r.Season = *e.Season
		}
		if e.Episode != nil {
			r.Episode = *e.Episode
		}
	}
	r.Info, r.Langs = parseMediaInfo(e.MediaInfo)
	r.Hash = contentHash(Element{PlaybackID: id, Kind: e.Kind, TMDB: e.TMDB, Filename: e.Filename, Filesize: e.Filesize, Fingerprint: e.Fingerprint, SourceURL: e.SourceURL, MediaInfo: e.MediaInfo}, r.Season, r.Episode)
	return r, true
}

// apply écrit des éléments (nouveaux ou modifiés), puis publie ceux dont le
// titre est au catalogue. Un fichier inchangé n'est ni rechiffré ni republié.
func (s *Service) apply(ctx context.Context, elems []Element, gen int64) error {
	rows := make([]row, 0, len(elems))
	urls := make(map[string]string, len(elems))
	ids := make([]string, 0, len(elems))
	for _, e := range elems {
		r, ok := toRow(e)
		if !ok {
			continue
		}
		if _, dup := urls[r.ID]; dup {
			continue
		}
		rows, ids = append(rows, r), append(ids, r.ID)
		urls[r.ID] = e.SourceURL
	}
	prior, err := s.st().prior(ctx, ids)
	if err != nil {
		return err
	}
	var same []string
	var changed, retry []row
	sig := s.Settings.publishSignature()
	for _, r := range rows {
		if p, ok := prior[r.ID]; ok && p.Hash == r.Hash {
			same = append(same, r.ID)
			// Une publication interrompue ou jugée selon d'autres règles reprend ici.
			if p.Pub == pubPending || p.PubSig != sig {
				retry = append(retry, r)
			}
			continue
		}
		if s.Seal == nil {
			return errors.New("chiffrement indisponible")
		}
		sealed, err := s.Seal(urls[r.ID])
		if err != nil {
			return errors.New("chiffrement du lien impossible")
		}
		r.Sealed = sealed
		changed = append(changed, r)
	}
	if err := s.st().seen(ctx, same, gen); err != nil {
		return err
	}
	if err := s.st().upsert(ctx, changed, gen); err != nil {
		return err
	}
	changed = append(changed, retry...)
	if s.Publisher == nil || len(changed) == 0 {
		return nil
	}
	files := make([]releasedb.AlkoFile, len(changed))
	for i, r := range changed {
		files[i] = releasedb.AlkoFile{ID: r.ID, Kind: r.Kind, Name: r.Filename, URL: urls[r.ID], Size: r.Size, TMDB: r.TMDB, Season: r.Season, Episode: r.Episode}
	}
	return s.publish(ctx, files, prior)
}

// publish publie des fichiers et retire les anciennes publications qui ne
// valent plus (autre titre ou épisode, fichier écarté).
func (s *Service) publish(ctx context.Context, files []releasedb.AlkoFile, prior map[string]row) error {
	res, err := s.Publisher.Publish(ctx, files, s.Settings.LangFR)
	if err != nil {
		return err
	}
	var upd []published
	var gone []releasedb.AlkoGone
	for _, f := range files {
		p := prior[f.ID]
		state, judged := res.States[f.ID]
		if p.RelID != "" && (!judged || state.State != releasedb.AlkoPublished || state.ReleaseID != p.RelID) {
			gone = append(gone, releasedb.AlkoGone{PlaybackID: f.ID, ReleaseID: p.RelID, TitleKey: core.RatingKey(p.TitleKey)})
		}
		switch {
		case !judged:
			if p.RelID != "" {
				upd = append(upd, published{ID: f.ID, Pub: pubPending})
			}
		case state.State == releasedb.AlkoPublished:
			upd = append(upd, published{ID: f.ID, Pub: pubPublished, RelID: state.ReleaseID, TitleKey: int64(state.TitleKey)})
		default:
			upd = append(upd, published{ID: f.ID, Pub: pubSkipped})
		}
	}
	if err := s.Publisher.Forget(ctx, gone); err != nil {
		return err
	}
	if err := s.st().setPublished(ctx, s.Settings.publishSignature(), upd); err != nil {
		return err
	}
	s.mu.Lock()
	if s.dirty == nil {
		s.dirty = map[core.RatingKey]struct{}{}
	}
	for _, k := range res.Touched {
		s.dirty[k] = struct{}{}
	}
	s.mu.Unlock()
	return nil
}

// flushDirty recalcule le catalogue « lisible uniquement » des titres touchés,
// une fois par titre plutôt qu'à chaque page.
func (s *Service) flushDirty(ctx context.Context) error {
	s.mu.Lock()
	keys := make([]core.RatingKey, 0, len(s.dirty))
	for k := range s.dirty {
		keys = append(keys, k)
	}
	s.dirty = nil
	s.mu.Unlock()
	if len(keys) == 0 || s.Publisher == nil {
		return nil
	}
	if err := s.Publisher.Refresh(ctx, keys); err != nil {
		s.mu.Lock()
		if s.dirty == nil {
			s.dirty = map[core.RatingKey]struct{}{}
		}
		for _, k := range keys {
			s.dirty[k] = struct{}{}
		}
		s.mu.Unlock()
		return err
	}
	return nil
}

type catalogTitle struct {
	Key  int64
	TMDB int64
	Show bool
}

// unchecked liste les titres du catalogue à vérifier : ceux jamais vérifiés
// d'abord, puis les plus anciennement vérifiés.
func (s *Service) unchecked(ctx context.Context, before time.Time, limit int) ([]catalogTitle, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.key,c.tmdb_id,c.type FROM catalog_titles c
LEFT JOIN pse_alko_checked k ON k.title_key=c.key
WHERE c.type IN ('movie','show') AND c.tmdb_id>0 AND COALESCE(k.checked_at,0)<?
ORDER BY COALESCE(k.checked_at,0),c.popularity DESC,c.key LIMIT ?`, before.Unix(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []catalogTitle
	for rows.Next() {
		var t catalogTitle
		var typ string
		if err := rows.Scan(&t.Key, &t.TMDB, &typ); err != nil {
			return nil, err
		}
		t.Show = typ == "show"
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Service) markChecked(ctx context.Context, titles []catalogTitle, now time.Time) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range titles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pse_alko_checked(title_key,checked_at) VALUES(?,?)
ON CONFLICT(title_key) DO UPDATE SET checked_at=excluded.checked_at`, t.Key, now.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// catchUp publie les fichiers des titres entrés au catalogue après leur
// synchronisation. Un lot n'est marqué vérifié qu'une fois lu en entier.
func (s *Service) catchUp(ctx context.Context) error {
	if s.Publisher == nil {
		return nil
	}
	s.setPhase("publish", 0)
	sig := s.Settings.publishSignature()
	var spent time.Duration
	checked, saved := 0, 0
	for range catchBatches {
		if spent >= catchBudget {
			break
		}
		if err := s.idle(ctx); err != nil {
			return err
		}
		now := s.now()
		start := time.Now()
		titles, err := s.unchecked(ctx, now.Add(-catchRecheck), catchBatch)
		if err != nil {
			return err
		}
		if len(titles) == 0 {
			break
		}
		var movies, shows []int64
		for _, t := range titles {
			if t.Show {
				shows = append(shows, t.TMDB)
			} else {
				movies = append(movies, t.TMDB)
			}
		}
		for kind, list := range map[string][]int64{"movie": movies, "episode": shows} {
			if len(list) == 0 {
				continue
			}
			after := ""
			for {
				if err := s.idle(ctx); err != nil {
					return err
				}
				rows, err := s.st().forPublish(ctx, kind, list, sig, after, 1000)
				if err != nil {
					return err
				}
				if len(rows) == 0 {
					break
				}
				files := make([]releasedb.AlkoFile, 0, len(rows))
				prior := make(map[string]row, len(rows))
				for _, r := range rows {
					prior[r.ID] = r
					url, err := s.openURL(r.Sealed)
					if err != nil {
						continue
					}
					files = append(files, releasedb.AlkoFile{ID: r.ID, Kind: r.Kind, Name: r.Filename, URL: url, Size: r.Size, TMDB: r.TMDB, Season: r.Season, Episode: r.Episode})
				}
				if err := s.publish(ctx, files, prior); err != nil {
					return err
				}
				saved += len(files)
				after = rows[len(rows)-1].ID
				if len(rows) < 1000 {
					break
				}
			}
		}
		if err := s.flushDirty(ctx); err != nil {
			return err
		}
		if err := s.markChecked(ctx, titles, now); err != nil {
			return err
		}
		spent += time.Since(start)
		checked += len(titles)
		if len(titles) < catchBatch {
			break
		}
		if !s.pause(ctx) {
			return ctx.Err()
		}
	}
	if saved > 0 {
		s.log().Info("rattrapage Alkodiques terminé", "titres", checked, "fichiers", saved)
	}
	return nil
}

func (s *Service) openURL(sealed string) (string, error) {
	if s.Open == nil {
		return "", errors.New("déchiffrement indisponible")
	}
	return s.Open(sealed)
}

// parseMediaInfo décode la sortie ffprobe brute de SFR en core.MediaInfo compact
// (info_json) et en langues des pistes audio normalisées (langs). Une valeur
// absente ou invalide donne NULL et une liste vide, jamais une erreur.
func parseMediaInfo(raw *string) (sql.NullString, string) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return sql.NullString{}, ""
	}
	m, err := transcode.ParseProbe([]byte(*raw))
	if err != nil || m == nil {
		return sql.NullString{}, ""
	}
	m.Chapters = nil
	var langs []string
	for _, st := range m.Streams {
		if st.Type != "audio" {
			continue
		}
		if l := langtag.Normalize(st.Language); l != "" && !slices.Contains(langs, l) {
			langs = append(langs, l)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return sql.NullString{}, ""
	}
	return sql.NullString{String: string(b), Valid: true}, strings.Join(langs, ",")
}
