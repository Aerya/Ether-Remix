package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/scrobview"
	"github.com/braedonsaunders/ether/internal/pse/console"
	"github.com/braedonsaunders/ether/internal/pse/frenchcat"
	"github.com/braedonsaunders/ether/internal/pse/music"
	"github.com/braedonsaunders/ether/internal/pse/notifyfr"
	"github.com/braedonsaunders/ether/internal/pse/releasedb"
	"github.com/braedonsaunders/ether/internal/pse/scrobpush"
	"github.com/braedonsaunders/ether/internal/pse/statistics"
	"github.com/braedonsaunders/ether/internal/pse/themes"
)

// pseRuntime regroupe l'état des ajouts PSE d'une instance d'App, sans
// ajouter de champ à la structure d'Ether.
type pseRuntime struct {
	addons        music.AddonRegistry
	statistics    statistics.Service
	once          sync.Once
	mu            sync.Mutex
	indexer       *releasedb.Service
	music         atomic.Pointer[music.Server]
	musicConf     music.Settings
	musicAccounts [32]byte
	scrob         atomic.Pointer[scrobpush.Pusher]
	plOnce        sync.Once
	playlists     *music.PlaylistSync
	thOnce        sync.Once
	themes        *themes.Service
	// exemptKick relance la mise à jour des exceptions de langue.
	exemptKick chan struct{}
	lfmOnce    sync.Once
	lastfm     *music.Scrobbler
}

// scrobbler prépare l'envoi des écoutes à Last.fm.
func (rt *pseRuntime) scrobbler(a *App) *music.Scrobbler {
	rt.lfmOnce.Do(func() {
		rt.lastfm = &music.Scrobbler{
			DB:      a.db,
			Library: music.NewLibrary(a.db),
			Sealer:  a.cfg,
			Key:     music.LastFMKey,
			Secret:  music.LastFMSecret,
			Log:     a.log.With("component", "pse-lastfm"),
		}
	})
	return rt.lastfm
}

// playlistSync prépare l'import des playlists Deezer et Last.fm.
func (rt *pseRuntime) playlistSync(a *App) *music.PlaylistSync {
	rt.plOnce.Do(func() {
		rt.playlists = &music.PlaylistSync{
			Store:     &music.Playlists{DB: a.db},
			Deezer:    music.NewDeezerSource(nil, nil),
			LastFMKey: music.LastFMKey,
			Log:       a.log.With("component", "pse-playlists"),
		}
	})
	return rt.playlists
}

var pseRuntimes sync.Map // *App -> *pseRuntime

func pseRuntimeOf(a *App) *pseRuntime {
	v, _ := pseRuntimes.LoadOrStore(a, &pseRuntime{})
	return v.(*pseRuntime)
}

// prepare lit une fois les réglages enregistrés depuis la console.
func (rt *pseRuntime) prepare(a *App) {
	rt.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := console.LoadOverrides(ctx, a.db); err != nil {
			a.log.Warn("réglages Ether-Remix illisibles, variables d'environnement seules", "err", err)
		}
		if err := rt.addons.Load(ctx, a.db); err != nil {
			a.log.Warn("cache du registre musical illisible", "err", err)
		}
		rt.statistics.HTTP = egress.Client(egress.CapMetadata, 90*time.Second)
		if err := rt.statistics.Load(ctx, a.db); err != nil {
			a.log.Warn("cache des totaux TMDB illisible")
		}
		frenchcat.Override(frenchcat.Load())
		releasedb.OverridePlayable(releasedb.LoadPlayable())
	})
}

func (rt *pseRuntime) stopIndexer() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.indexer != nil {
		rt.indexer.Close()
		rt.indexer = nil
	}
}

func (rt *pseRuntime) restartIndexer(a *App) {
	if a.catalog == nil {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.indexer != nil {
		rt.indexer.Close()
	}
	rt.indexer = a.newPSEIndexer()
	rt.indexer.Start()
}

func (rt *pseRuntime) reloadMusic(a *App) {
	cfg := music.LoadSettings()
	accounts, _ := json.Marshal(a.cfg.Get().Debrid.Providers)
	fingerprint := sha256.Sum256(accounts)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.music.Load() != nil && cfg == rt.musicConf && fingerprint == rt.musicAccounts {
		return
	}
	rt.musicConf, rt.musicAccounts = cfg, fingerprint
	if !cfg.Enabled() {
		rt.music.Store(nil)
		frontend.SetPSEMusic(nil)
		return
	}
	srv := a.newPSEMusic(cfg)
	rt.music.Store(srv)
	if srv != nil { // bibliothèque Musique des clients Jellyfin et Plex
		frontend.SetPSEMusic(srv)
	} else {
		frontend.SetPSEMusic(nil)
	}
	a.log.Info("API Subsonic Ether-Remix active", "path", "/rest/", "source", cfg.Mode())
}

// apply rend effectifs les réglages enregistrés depuis la console.
func (rt *pseRuntime) apply(a *App) {
	frenchcat.Override(frenchcat.Load())
	releasedb.OverridePlayable(releasedb.LoadPlayable())
	rt.reloadMusic(a)
	go rt.restartIndexer(a) // Close attend la fin du titre en cours
}

// pseHandlers ajoute l'API de la page PSE de la console (/ether/api/pse/).
func (a *App) pseHandlers(m map[string]http.Handler) {
	if a.db == nil {
		return
	}
	rt := pseRuntimeOf(a)
	rt.prepare(a)
	scrob := scrobpush.Handler(scrobpush.HandlerDeps{
		Links:  scrobpush.Links{DB: a.db},
		Pusher: func() *scrobpush.Pusher { return rt.scrob.Load() },
		Viewers: func(ctx context.Context) ([]scrobpush.Viewer, error) {
			vs, err := a.pseViewers(ctx)
			out := make([]scrobpush.Viewer, 0, len(vs))
			for _, v := range vs {
				out = append(out, scrobpush.Viewer{ID: v.ID, Name: v.Name})
			}
			return out, err
		},
	})
	playlistAPI := &music.PlaylistAPI{Sync: rt.playlistSync(a), Viewers: a.pseViewers}
	playlists := playlistAPI.Handler()
	lastfm := rt.scrobbler(a).Handler(playlistAPI.Known)
	stats := music.StatsHandler(music.NewLibrary(a.db))
	indexDetails := a.pseIndexDetailsHandler()
	diagnose := a.psePlaylistDiagnose()
	settings := console.Handler(console.Deps{
		DB:    a.db,
		Apply: func(context.Context) { rt.apply(a) },
		Status: func(ctx context.Context) map[string]any {
			return a.pseStatus(ctx, rt)
		},
		Sources: func() []console.Option {
			var out []console.Option
			for _, ix := range a.cfg.Get().Indexers {
				if ix.ID == "" {
					continue
				}
				name := ix.Name
				if name == "" {
					name = ix.ID
				}
				kind := "Stremio"
				if ix.Kind != config.IndexerKindStremio {
					kind = "Newznab/Torznab"
				}
				if !ix.Enabled {
					kind += ", désactivé"
				}
				label := name + " (" + kind + ")"
				out = append(out, console.Option{Value: ix.ID, Label: console.Text{FR: label, EN: label}})
			}
			sort.Slice(out, func(i, j int) bool { return out[i].Label.FR < out[j].Label.FR })
			return out
		},
		TestMusic: func(ctx context.Context, key, value string) (string, error) {
			if music.IsCommunityKey(key) {
				key = "PSE_MUSIC_ECLIPSE_URL"
			}
			switch key {
			case "PSE_MUSIC_ALLDEBRID":
				return a.testPSEMusicDebrid(ctx, config.DebridAllDebrid)
			case "PSE_MUSIC_DMM":
				return a.testPSEMusicDebrid(ctx, music.DMMSource)
			case "PSE_MUSIC_REALDEBRID":
				return a.testPSEMusicDebrid(ctx, config.DebridRealDebrid)
			case "PSE_MUSIC_TORBOX":
				return a.testPSEMusicDebrid(ctx, config.DebridTorBox)
			case "PSE_MUSIC_ECLIPSE_URL":
				e := music.NewEclipse(music.ParseEclipseURLs(value), nil)
				if err := e.Check(ctx); err != nil {
					return "", err
				}
				_, err := music.NewDeezerSource(e, nil).SearchTracks(ctx, "test", 1, 0)
				return "Addon Eclipse et catalogue Deezer joignables", err
			}
			_, err := music.NewClient(value, nil).SearchTracks(ctx, "test", 1, 0)
			return "La HiFi API répond", err
		},
	})
	var themeAPI http.Handler = http.NotFoundHandler()
	if th := a.pseThemes(); th != nil {
		var creator themes.Creator
		if a.collections != nil {
			creator = a.collections
		}
		th.SetPriorityHooks(a.psePriorityHooks(rt))
		themeAPI = th.Handler(creator, a.pseLangExemptChanged)
	}
	indexActivity := a.pseIndexActivityHandler(false)
	indexSources := a.pseIndexActivityHandler(true)
	indexHistory := a.pseIndexHistoryHandler()
	m["pse"] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/themes" || strings.HasPrefix(r.URL.Path, "/themes/") {
			http.StripPrefix("/themes", themeAPI).ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/scrob" || strings.HasPrefix(r.URL.Path, "/scrob/") {
			http.StripPrefix("/scrob", scrob).ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/music/lastfm" || strings.HasPrefix(r.URL.Path, "/music/lastfm/") {
			http.StripPrefix("/music/lastfm", lastfm).ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/indexer/activity" {
			indexActivity.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/indexer/sources" {
			indexSources.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/indexer/history" {
			indexHistory.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/indexer/details" {
			indexDetails.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/music/addons" {
			rt.addons.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/music/diagnose" {
			diagnose.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/music/stats" {
			stats.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/music/playlists" || strings.HasPrefix(r.URL.Path, "/music/playlists/") {
			http.StripPrefix("/music/playlists", playlists).ServeHTTP(w, r)
			return
		}
		settings.ServeHTTP(w, r)
	})
}

func (a *App) pseStatus(ctx context.Context, rt *pseRuntime) map[string]any {
	lang := "en"
	if notifyfr.Enabled() {
		lang = "fr"
	}
	out := map[string]any{
		"notify": map[string]any{"language": lang},
		"french": map[string]any{"active": frenchcat.Current().Enabled},
		"scrob":  map[string]any{"active": scrobview.Enabled() || scrobpush.Enabled(), "views": scrobview.Enabled(), "push": scrobpush.Enabled()},
	}
	idx := releasedb.LoadSettings()
	playable := releasedb.Playable()
	indexer := map[string]any{"active": idx.Enabled}
	if st, err := releasedb.NewStore(a.db).ConsoleStats(ctx, playable.Min); err == nil {
		indexer["stats"] = st
		out["playable"] = map[string]any{"active": playable.Enabled, "titles": st.Playable, "min": playable.Min}
	} else {
		out["playable"] = map[string]any{"active": playable.Enabled}
	}
	out["indexer"] = indexer
	m := map[string]any{"active": rt.music.Load() != nil, "path": "/rest/", "source": music.LoadSettings().Mode()}
	if totals, err := music.NewLibrary(a.db).Totals(ctx); err == nil {
		m["library"] = totals
	}
	out["music"] = m
	return out
}

// pseViewers liste le propriétaire et les spectateurs, pour les réglages
// par spectateur de la page PSE.
func (a *App) pseViewers(ctx context.Context) ([]music.Viewer, error) {
	if a.users == nil {
		return nil, nil
	}
	infos, err := a.users.AllUsers(ctx)
	if err != nil {
		return nil, err
	}
	users := make([]core.User, 0, len(infos)+1)
	if o, err := a.users.Owner(ctx); err == nil && o != nil {
		users = append(users, *o)
	}
	for _, u := range infos {
		users = append(users, u.User)
	}
	seen := map[int64]bool{}
	out := make([]music.Viewer, 0, len(users))
	for _, u := range users {
		if u.ID <= 0 || seen[u.ID] {
			continue
		}
		seen[u.ID] = true
		name := u.Title
		if name == "" {
			name = u.Username
		}
		if name == "" {
			name = "#" + strconv.FormatInt(u.ID, 10)
		}
		out = append(out, music.Viewer{ID: u.ID, Name: name})
	}
	return out, nil
}
