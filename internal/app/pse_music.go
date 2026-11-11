package app

import (
	"context"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/pse/music"
)

// mountPSEMusic monte l'API Subsonic de PSE sous /rest/. Elle répond 404
// tant qu'aucune source n'est réglée (PSE_MUSIC_ECLIPSE_URL ou
// PSE_MUSIC_HIFI_URL) ; la console PSE peut l'activer à chaud. Voir
// docs/pse/FORK.fr.md.
func (a *App) mountPSEMusic(mux *http.ServeMux) {
	if a.db == nil {
		return
	}
	rt := pseRuntimeOf(a)
	rt.prepare(a)
	rt.reloadMusic(a)
	a.startOwnedMedia()
	// Account and priority changes must invalidate music links as well.
	a.cfg.Subscribe(func(config.Config) { rt.reloadMusic(a) })
	a.startPSEPlaylists(rt)
	ctx, cancel := context.WithCancel(context.Background()) // envoi des écoutes à Last.fm
	a.closers = append(a.closers, cancel)
	go rt.scrobbler(a).Run(ctx)
	go rt.statistics.Run(ctx)
	go rt.addons.Run(ctx, func() bool { return rt.music.Load() != nil })
	mux.Handle("/rest/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv := rt.music.Load()
		if srv == nil {
			http.NotFound(w, r)
			return
		}
		srv.ServeHTTP(w, r)
	}))
}

// psePlaylistsSystem régénère les tops par pays et « Découvertes » de
// chaque spectateur (toutes les 12 h, ou dès que le réglage change).
func (a *App) psePlaylistsSystem(ctx context.Context, sync *music.PlaylistSync) {
	vs, err := a.pseViewers(ctx)
	if err != nil {
		a.log.Warn("playlists système : spectateurs illisibles", "err", err)
		return
	}
	ids := make([]int64, 0, len(vs))
	for _, v := range vs {
		ids = append(ids, v.ID)
	}
	sync.SyncSystemDue(ctx, ids, music.SystemOptions{Charts: music.ChartsSetting(), Discover: music.DiscoverSetting()}, music.SystemEvery)
}

func (a *App) newPSEMusic(cfg music.Settings) *music.Server {
	local, err := identity.NewLocal(a.db)
	if err != nil {
		a.log.Warn("musique Ether-Remix indisponible", "err", err)
		return nil
	}
	a.ownedMedia()
	rt := pseRuntimeOf(a)
	return music.NewServer(music.Options{
		Source:    &music.OwnedSource{Remote: cfg.NewSource(a.newPSEMusicDebrid()), DB: a.db, Local: rt.local, IB: rt.ib, Account: a.ibAccount},
		Library:   music.NewLibrary(a.db),
		Playlists: pseRuntimeOf(a).playlistSync(a).Store,
		Auth: func(ctx context.Context, username, password string) (int64, error) {
			viewer, _, err := local.Authenticate(ctx, username, password)
			return viewer, err
		},
		Quality: cfg.Quality,
		Logger:  a.log.With("component", "pse-music"),
	})
}

// startPSEPlaylists réimporte en arrière-plan les playlists Deezer et
// Last.fm des spectateurs, à la cadence PSE_MUSIC_PLAYLISTS_HOURS, tant que
// la musique est active.
func (a *App) startPSEPlaylists(rt *pseRuntime) {
	ctx, cancel := context.WithCancel(context.Background())
	a.closers = append(a.closers, cancel)
	sync := rt.playlistSync(a)
	go func() {
		wait := time.Minute // laisse le démarrage se terminer
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = 10 * time.Minute
			if rt.music.Load() != nil {
				sync.SyncDue(ctx, music.PlaylistEvery())
				a.psePlaylistsSystem(ctx, sync)
			}
		}
	}()

}
