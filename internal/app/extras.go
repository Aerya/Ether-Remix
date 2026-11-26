package app

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extras"
	"github.com/braedonsaunders/ether/internal/plexguid"
)

// buildExtras wires the trailers/extras service. On by default whenever a
// playable source (Plex Discover or yt-dlp/resolverCommand) is available.
func (a *App) buildExtras() {
	var dl *extras.Downloader
	if a.onair != nil {
		dl = a.onair.Downloader()
	}
	cacheDir := ""
	if root := a.cfg.Read().Cache.Dir; root != "" {
		cacheDir = filepath.Join(root, "trailers")
	}
	plexToken := func() string {
		cfg := a.cfg.Read()
		if !cfg.Plex.Claimed || (cfg.Frontends != nil && !cfg.Frontends["plex"].Enabled) {
			return ""
		}
		return strings.TrimSpace(cfg.Plex.Token)
	}
	clientID := func() string {
		cfg := a.cfg.Read()
		return plexguid.ClientIdentifier(cfg.Plex.ClientIdentifier, cfg.Server.MachineIdentifier)
	}
	discover := extras.NewPlexDiscover(extras.PlexDiscoverOptions{
		HTTP:     a.opts.plextvHTTP,
		Token:    plexToken,
		ClientID: clientID,
		ResolveGUID: func(ctx context.Context, key core.RatingKey) (string, error) {
			if plexToken() == "" {
				return "", nil
			}
			a.plexMetaMu.Lock()
			pg := a.plexGUID
			a.plexMetaMu.Unlock()
			if pg == nil {
				_ = a.ensurePlexAccountServices()
				a.plexMetaMu.Lock()
				pg = a.plexGUID
				a.plexMetaMu.Unlock()
			}
			if pg == nil {
				return "", nil
			}
			if g := pg.Cached(ctx, key); g != "" {
				return g, nil
			}
			return pg.Resolve(ctx, key)
		},
		Logger: a.log.With("component", "extras"),
	})
	a.extras = extras.New(extras.Options{
		DB: a.db,
		ThemeFFmpeg: func() string {
			if a.transcoder != nil {
				return a.transcoder.FFmpegPath()
			}
			return ""
		},
		Videos:     a.tmdb,
		Discover:   discover,
		CacheDir:   cacheDir,
		Config:     func() config.Extras { return a.cfg.Read().Extras },
		Logger:     a.log.With("component", "extras"),
		ToolsDir:   filepath.Join(a.opts.DataDir, "tools"),
		Downloader: dl,
	})
	a.closers = append(a.closers, a.extras.Close)
	if reason := a.extras.DisabledReason(); reason != "" {
		a.log.Info("extras off", "reason", reason)
	} else {
		a.log.Info("extras on", "discover", a.extras.DiscoverAvailable(), "command", a.extras.CommandAvailable())
	}
}

// themeTitle only reads the local catalogue and current inherited access policy.
func (a *App) themeTitle(ctx context.Context, viewer *core.Viewer, key core.RatingKey) (*core.Title, error) {
	if viewer == nil || a.catalog == nil {
		return nil, core.ErrNotFound
	}
	switch key.Kind() {
	// Movies authorize optional empty theme listings too; the theme service
	// only schedules audio for shows.
	case core.KindMovie, core.KindShow, core.KindSeason, core.KindEpisode:
	default:
		return nil, core.ErrNotFound
	}
	vis := experienceVisibility(a.safety, a.catalog.CachedGet, nil, nil, a.configuredLibraries())
	if a.libraries != nil {
		return a.librarySessionTitle(ctx, viewer, key, vis, true, false)
	}
	title, err := a.catalog.CachedGet(ctx, key)
	if err != nil {
		return nil, err
	}
	if !vis.Playable(ctx, viewer, title) {
		return nil, core.ErrNotFound
	}
	return title, nil
}
