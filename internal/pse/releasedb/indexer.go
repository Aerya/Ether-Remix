package releasedb

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/seerrcatalog"
)

// Settings règle l'indexeur (variables d'environnement PSE_INDEXER_*).
type Settings struct {
	// Enabled : PSE_INDEXER=on. Désactivé par défaut.
	Enabled bool
	// Sources : PSE_INDEXER_SOURCES, identifiants d'indexeurs séparés par
	// des virgules. Vide : tous les addons Stremio activés. Un indexeur
	// Newznab/Torznab n'est interrogé que s'il est listé ici.
	Sources []string
	// Daily : PSE_INDEXER_DAILY, requêtes maximum par source et par jour
	// UTC (défaut 1000). Pour un indexeur Newznab/Torznab, la limite est
	// aussi plafonnée à 5 % de sa limite quotidienne déclarée (50 si
	// inconnue), en plus de la part que la veille d'Ether s'accorde.
	Daily int
	// Pace : PSE_INDEXER_PACE, délai minimum entre deux requêtes (défaut
	// 10 s, minimum 2 s).
	Pace time.Duration
	// Types : PSE_INDEXER_TYPES, "movie", "show" ou les deux (défaut).
	Types []core.MediaType
	// SFR : import depuis une base StreamFusion Reborn (sfr.go).
	SFR SFRSettings
}

// LoadSettings lit la configuration depuis l'environnement.
func LoadSettings() Settings {
	s := Settings{Daily: 1000, Pace: 10 * time.Second, Types: []core.MediaType{core.TypeMovie, core.TypeShow}, SFR: LoadSFRSettings()}
	switch strings.ToLower(strings.TrimSpace(pseenv.Get("PSE_INDEXER"))) {
	case "on", "1", "true", "yes", "oui":
		s.Enabled = true
	}
	for _, id := range strings.Split(pseenv.Get("PSE_INDEXER_SOURCES"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			s.Sources = append(s.Sources, id)
		}
	}
	if n, err := strconv.Atoi(strings.TrimSpace(pseenv.Get("PSE_INDEXER_DAILY"))); err == nil && n >= 0 {
		s.Daily = n
	}
	if d, err := time.ParseDuration(strings.TrimSpace(pseenv.Get("PSE_INDEXER_PACE"))); err == nil {
		s.Pace = d
	}
	if s.Pace < 2*time.Second {
		s.Pace = 2 * time.Second
	}
	if v := strings.TrimSpace(pseenv.Get("PSE_INDEXER_TYPES")); v != "" {
		s.Types = nil
		for _, t := range strings.Split(v, ",") {
			switch strings.ToLower(strings.TrimSpace(t)) {
			case "movie", "movies", "film", "films":
				s.Types = append(s.Types, core.TypeMovie)
			case "show", "shows", "series", "série", "séries":
				s.Types = append(s.Types, core.TypeShow)
			}
		}
	}
	return s
}

// Deps relie l'indexeur à l'application.
type Deps struct {
	DB     *sql.DB
	Config func() config.Config
	Browse func(context.Context, core.BrowseQuery) (core.Page, error)
	Search func(context.Context, core.SearchRequest) ([]core.Release, error)
	Active func() bool // une lecture est en cours : l'indexation attend
	// Priority renvoie les titres à indexer avant le reste du catalogue
	// (bibliothèques à thème, priorités de la console) ; facultatif.
	Priority func(context.Context, core.MediaType) []PriorityKey
	Now      func() time.Time
	Logger   *slog.Logger
	Settings Settings
}

// Service indexe le catalogue en arrière-plan.
type Service struct {
	activity activityLog
	d        Deps
	st       *Store
	mu       sync.Mutex
	queue    map[core.MediaType][]core.Title
	prio     map[core.MediaType][]core.Title
	prioAt   map[core.MediaType]time.Time
	levels   map[core.RatingKey]int
	urgent   map[core.MediaType][]core.Title // « Chercher maintenant »
	turn     int
	done     int
	stop     context.CancelFunc
	sfrStop  context.CancelFunc
	openSFR  func(context.Context, string) (sfrReader, error)
	wg       sync.WaitGroup
}

const (
	pageSize       = 50
	sourceTimeout  = 45 * time.Second
	refreshFound   = 14 * 24 * time.Hour
	refreshMissing = 7 * 24 * time.Hour
	refreshRecent  = 2 * 24 * time.Hour
	refreshFailed  = 24 * time.Hour // panne : nouvel essai le lendemain
	maintainEvery  = time.Hour
	recentWindow   = 60 * 24 * time.Hour
	priorityEvery  = 30 * time.Minute
	priorityMax    = 2000
	riskEvery      = 2 * time.Minute // relecture des titres risqués (risk.go)
)

// New construit le service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	st := NewStore(d.DB)
	st.now = d.Now
	return &Service{d: d, st: st, queue: map[core.MediaType][]core.Title{},
		prio: map[core.MediaType][]core.Title{}, prioAt: map[core.MediaType]time.Time{},
		levels: map[core.RatingKey]int{}, urgent: map[core.MediaType][]core.Title{}}
}

// Store donne accès aux releases indexées.
func (s *Service) Store() *Store { return s.st }

// Start prépare le catalogue « lisible uniquement » puis lance la boucle
// d'indexation si elle est activée.
func (s *Service) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.syncPlayable(context.Background())
		s.maintain(context.Background())
		s.refreshRisk(context.Background())
	}()
	if cfg := s.d.Settings.SFR; cfg.Enabled {
		ctx, cancel := context.WithCancel(context.Background())
		s.sfrStop = cancel
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.watchSFRCache(ctx) }()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runSFR(ctx, cfg)
		}()
	}
	if !s.d.Settings.Enabled || len(s.d.Settings.Types) == 0 {
		s.d.Logger.Info("pse release indexer disabled (set PSE_INDEXER=on to enable)")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.stop = cancel
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.d.Logger.Info("pse release indexer started", "pace", s.d.Settings.Pace.String(), "daily", s.d.Settings.Daily)
		t := time.NewTicker(s.d.Settings.Pace)
		defer t.Stop()
		lastMaintain, lastRisk := s.d.Now(), s.d.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			if now := s.d.Now(); now.Sub(lastMaintain) >= maintainEvery {
				lastMaintain = now
				s.maintain(ctx)
			}
			if now := s.d.Now(); now.Sub(lastRisk) >= riskEvery {
				lastRisk = now
				s.refreshRisk(ctx)
			}
			if s.d.Active != nil && s.d.Active() {
				continue
			}
			if err := s.Step(ctx); err != nil && ctx.Err() == nil && !errors.Is(err, errNothingToDo) {
				s.d.Logger.Warn("pse release indexer step failed", "err", err)
			}
		}
	}()
}

func (s *Service) syncPlayable(ctx context.Context) {
	p := Playable()
	if !p.Enabled {
		if err := s.st.ForgetPlayable(ctx); err != nil {
			s.d.Logger.Warn("pse playable reset failed", "err", err)
		}
		return
	}
	n, err := s.st.SyncPlayable(ctx, p)
	if err != nil {
		s.d.Logger.Warn("pse playable rebuild failed", "err", err)
		return
	}
	s.d.Logger.Info("pse playable-only catalogue enabled", "min", p.Min, "filter", p.raw, "pending", p.Pending, "rebuilt", n)
}

// activeSourceIDs liste les indexeurs activés dans Ether : les releases
// d'une source supprimée ou désactivée ne comptent plus.
func (s *Service) activeSourceIDs() []string {
	if s.d.Config == nil {
		return nil
	}
	ids := []string{}
	for _, ix := range s.d.Config().Indexers {
		if ix.Enabled && ix.ID != "" {
			ids = append(ids, ix.ID)
		}
	}
	if s.d.Settings.SFR.Enabled {
		ids = append(ids, SFRSourceID)
	}
	return ids
}

// maintain purge les observations expirées et applique la liste des
// sources actives (freshness.go).
func (s *Service) maintain(ctx context.Context) {
	_, _ = s.st.db.ExecContext(ctx, "DELETE FROM pse_index_activity WHERE day<?", s.d.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02"))
	res, err := s.st.Maintain(ctx, s.d.Now(), s.activeSourceIDs(), Playable())
	if err != nil {
		if ctx.Err() == nil {
			s.d.Logger.Warn("pse release maintenance failed", "err", err)
		}
		return
	}
	if res.Expired > 0 || res.Recomputed > 0 {
		s.d.Logger.Info("pse release maintenance", "expired", res.Expired, "recomputed", res.Recomputed)
	}
}

// Close arrête la boucle.
func (s *Service) Close() {
	if s.sfrStop != nil {
		s.sfrStop()
	}
	if s.stop != nil {
		s.stop()
	}
	s.wg.Wait()
}

var errNothingToDo = errors.New("nothing to index")

// Step indexe le prochain titre à échéance (un seul par appel).
func (s *Service) Step(ctx context.Context) error {
	sources := s.sources()
	if len(sources) == 0 {
		return errNothingToDo
	}
	t, err := s.next(ctx)
	if err != nil {
		return err
	}
	return s.IndexTitle(ctx, t, sources)
}

type source struct {
	id, name string
	limit    int
}

// sources liste les sources interrogées et leur limite quotidienne.
func (s *Service) sources() []source {
	cfg := s.d.Config()
	explicit := map[string]bool{}
	for _, id := range s.d.Settings.Sources {
		explicit[id] = true
	}
	var out []source
	for _, ix := range cfg.Indexers {
		if !ix.Enabled || ix.ID == "" {
			continue
		}
		stremio := ix.Kind == config.IndexerKindStremio
		if len(explicit) > 0 && !explicit[ix.ID] {
			continue
		}
		if len(explicit) == 0 && !stremio {
			continue
		}
		limit := s.d.Settings.Daily
		if !stremio {
			share := 50
			if ix.DailyLimit > 0 {
				share = max(1, ix.DailyLimit*5/100)
			}
			limit = min(limit, share)
		}
		if limit > 0 {
			out = append(out, source{id: ix.ID, name: ix.Name, limit: limit})
		}
	}
	return out
}

// next choisit le prochain titre à échéance, en alternant films et séries
// et en parcourant le catalogue par popularité décroissante.
func (s *Service) next(ctx context.Context) (core.Title, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	types := s.d.Settings.Types
	now := s.d.Now()
	for range types {
		typ := types[s.turn%len(types)]
		s.turn++
		if t, ok, err := s.nextPriority(ctx, typ, now); err != nil {
			return core.Title{}, err
		} else if ok {
			return t, nil
		}
		for scanned := 0; scanned < 4*pageSize; scanned++ {
			if len(s.queue[typ]) == 0 {
				if err := s.fill(ctx, typ); err != nil {
					return core.Title{}, err
				}
				if len(s.queue[typ]) == 0 {
					break
				}
			}
			t := s.queue[typ][0]
			s.queue[typ] = s.queue[typ][1:]
			if t.Key.Kind() == core.KindLocal {
				continue
			}
			due, err := s.st.dueAt(ctx, t.Key)
			if err != nil {
				return core.Title{}, err
			}
			if due.IsZero() || !due.After(now) {
				return t, nil
			}
		}
	}
	return core.Title{}, errNothingToDo
}

// nextPriority renvoie le prochain titre prioritaire à échéance. La liste
// est relue au plus toutes les 30 minutes.
func (s *Service) nextPriority(ctx context.Context, typ core.MediaType, now time.Time) (core.Title, bool, error) {
	for len(s.urgent[typ]) > 0 {
		t := s.urgent[typ][0]
		s.urgent[typ] = s.urgent[typ][1:]
		if t.Key.Kind() == core.KindLocal {
			continue
		}
		return t, true, nil
	}
	if s.d.Priority == nil {
		return core.Title{}, false, nil
	}
	if len(s.prio[typ]) == 0 && now.Sub(s.prioAt[typ]) >= priorityEvery {
		s.prioAt[typ] = now
		keys, levels := orderPriority(s.d.Priority(ctx, typ))
		for k, l := range levels {
			s.levels[k] = l
		}
		if len(keys) > 0 {
			page, err := s.d.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: typ, IncludeKeys: keys}, Sort: core.SortPopularity, Desc: true, Limit: len(keys), NoTotal: true})
			if err != nil {
				return core.Title{}, false, err
			}
			// Le catalogue renvoie par popularité : les niveaux passent avant.
			sort.SliceStable(page.Items, func(i, j int) bool { return levels[page.Items[i].Key] > levels[page.Items[j].Key] })
			s.prio[typ] = page.Items
		}
	}
	for len(s.prio[typ]) > 0 {
		t := s.prio[typ][0]
		s.prio[typ] = s.prio[typ][1:]
		if t.Key.Kind() == core.KindLocal {
			continue
		}
		due, err := s.st.dueFor(ctx, t.Key, s.levels[t.Key])
		if err != nil {
			return core.Title{}, false, err
		}
		if due.IsZero() || !due.After(now) {
			return t, true, nil
		}
	}
	return core.Title{}, false, nil
}

// fill charge la page suivante du catalogue ; en fin de catalogue, le
// parcours reprend au début.
func (s *Service) fill(ctx context.Context, typ core.MediaType) error {
	off, err := s.st.cursor(ctx, typ)
	if err != nil {
		return err
	}
	page, err := s.d.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: typ}, Sort: core.SortPopularity, Desc: true, Offset: off, Limit: pageSize, NoTotal: true})
	if err != nil {
		return err
	}
	next := off + len(page.Items)
	if len(page.Items) < pageSize {
		next = 0
	}
	if err := s.st.setCursor(ctx, typ, next); err != nil {
		return err
	}
	s.queue[typ] = append(s.queue[typ], page.Items...)
	return nil
}

// IndexTitle interroge chaque source pour un titre et enregistre ce qui
// est trouvé.
func (s *Service) IndexTitle(ctx context.Context, t core.Title, sources []source) error {
	// Local files, including personal videos, must never be sent to external indexers.
	if t.Key.Kind() == core.KindLocal {
		return nil
	}
	// A recent SeerrCatalog availability replaces this duplicate catalogue check.
	if seerrcatalog.Trusted(ctx, s.st.db, t.Key) {
		return nil
	}
	req := core.SearchRequest{Anime: t.IsAnime(), AbsoluteEpisode: t.AbsoluteEpisode, Type: t.Type, IDs: t.IDs, Title: t.Title, OriginalTitle: t.OriginalTitle, Year: t.Year, AlternativeTitles: t.AlternativeTitles, Released: t.Released}
	season, episode := 0, 0
	if t.Type == core.TypeShow {
		// Une série est représentée par son premier épisode.
		season, episode = 1, 1
		req.Type, req.Season, req.Episode, req.AbsoluteEpisode = core.TypeEpisode, 1, 1, 1
	}
	now := s.d.Now()
	// Issue de chaque source : réussie (complète, éventuellement sans
	// résultat), partielle (erreur avec des résultats), en panne ou sans
	// budget. Seule une recherche réussie efface les releases disparues.
	found, asked, denied, failed, partial := 0, 0, 0, 0, 0
	for _, src := range sources {
		event := s.beginSearch(t, src)
		sctx, cancel := context.WithTimeout(s.budgetContext(ctx, src), sourceTimeout)
		rels, err := s.d.Search(sctx, req)
		cancel()
		if ctx.Err() != nil {
			s.finishSearch(ctx, event, len(rels), ctx.Err())
		} else {
			s.finishSearch(ctx, event, len(rels), err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && len(rels) == 0 {
			if errors.Is(err, core.ErrBudgetExhausted) {
				denied++
			} else {
				failed++
			}
			continue
		}
		asked++
		// Une source qui marque le cache (⚡, +…) sur au moins une release
		// le marque sur toutes : ses flux non marqués ne sont pas en cache.
		marks := false
		for _, r := range rels {
			marks = marks || len(r.DebridCached) > 0 || len(r.DebridUncached) > 0
		}
		var batch []Release
		for _, r := range rels {
			cached := r.DebridCached
			if !marks {
				cached = cachedHints(r)
			}
			if src.id == XDCCSourceID {
				cached = nil // un paquet XDCC se télécharge d'abord : jamais « en cache »
			}
			batch = append(batch, FromCore(t, season, episode, src.id, r, cached))
		}
		if err != nil {
			// Résultats partiels : on garde aussi les anciennes releases.
			partial++
			if err := s.st.Save(ctx, batch, now); err != nil {
				return err
			}
		} else if _, err := s.st.Replace(ctx, Scope{SourceID: src.id, TitleKey: t.Key, Season: season, Episode: episode}, batch, now); err != nil {
			return err
		}
		found += len(batch)
	}
	if p := Playable(); p.Enabled {
		if err := s.st.RefreshPlayable(ctx, t.Key, p); err != nil {
			return err
		}
	}
	if asked == 0 && denied > 0 {
		// Budgets du jour épuisés : le titre reste à échéance.
		return core.ErrBudgetExhausted
	}
	next := now.Add(refreshMissing)
	status := "none"
	switch {
	case !t.Released.IsZero() && now.Sub(t.Released) < recentWindow:
		next = now.Add(refreshRecent)
	case found > 0:
		next = now.Add(refreshFound)
	}
	if found > 0 {
		status = "found"
	}
	if failed > 0 || partial > 0 {
		// Une source n'a pas (entièrement) répondu : nouvel essai bientôt,
		// sans attendre le cycle normal.
		if status == "none" {
			status = "failed"
		} else {
			status = "partial"
		}
		next = now.Add(refreshFailed)
	}
	if asked == 0 {
		status = "failed"
	}
	if err := s.st.markIndexed(ctx, t, found, status, now, next); err != nil {
		return err
	}
	s.mu.Lock()
	s.done++
	done := s.done
	s.mu.Unlock()
	if done%100 == 0 {
		if st, err := s.st.Stats(ctx, now); err == nil {
			s.d.Logger.Info("pse release indexer progress", "titles", st.Titles, "withReleases", st.TitlesWithReleases, "releases", st.Releases)
		}
	}
	return nil
}

// budgetContext limite la recherche à une source et à son budget PSE.
// Seules les requêtes de recherche sont admises : aucun téléchargement de
// NZB, aucune lecture d'octets, aucune vérification debrid (donc aucun
// appel à l'API AllDebrid depuis l'indexeur).
func (s *Service) budgetContext(ctx context.Context, src source) context.Context {
	skip := map[string]bool{}
	for _, ix := range s.d.Config().Indexers {
		if ix.ID != src.id {
			skip[ix.ID] = true
		}
	}
	ctx = core.WithIndexerSkip(ctx, skip)
	ctx = core.WithUnattendedSearch(ctx)
	ctx = core.WithIndexerPurpose(ctx, core.IndexerPurposeAvailability)
	return core.WithBackgroundBudget(ctx, func(ctx context.Context, resource, sourceID string, n int64) error {
		if resource != "hit" {
			return core.ErrBudgetExhausted
		}
		if sourceID != "" && sourceID != src.id {
			return core.ErrBudgetExhausted
		}
		ok, err := s.st.spend(ctx, src.id, int(n), src.limit, s.d.Now())
		if err != nil {
			return err
		}
		if !ok {
			return core.ErrBudgetExhausted
		}
		return nil
	})
}

// cachedHints renvoie les services debrid qui ont la release en cache,
// pour une source qui ne marque pas le cache. Un flux HTTP d'addon Stremio
// (AIOStreams…) y est un lien déjà débridé, lisible tout de suite : il
// compte comme en cache (« direct ») sauf s'il est marqué hors cache.
func cachedHints(r core.Release) []string {
	if len(r.DebridCached) > 0 || r.Protocol != core.ProtocolHTTP || len(r.DebridUncached) > 0 {
		return r.DebridCached
	}
	return []string{"direct"}
}
