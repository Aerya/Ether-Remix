package app

import (
	"context"
	"path/filepath"

	"github.com/braedonsaunders/ether/internal/core"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/extras"
)

// buildExtras wires the trailers/extras service. It stays off (and says
// why, once) until the owner enables it and configures a playable source.
func (a *App) buildExtras() {
	var dl *extras.Downloader
	if a.onair != nil {
		dl = a.onair.Downloader()
	}
	cacheDir := ""
	if root := a.cfg.Read().Cache.Dir; root != "" {
		cacheDir = filepath.Join(root, "trailers")
	}
	a.extras = extras.New(extras.Options{
		ThemeFFmpeg: func() string {
			if a.transcoder != nil {
				return a.transcoder.FFmpegPath()
			}
			return ""
		},
		Videos:     a.tmdb,
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
		a.log.Info("extras on", "command", a.extras.CommandAvailable())
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
	title, err := a.catalog.CachedGet(ctx, key)
	if err != nil {
		return nil, err
	}
	vis := experienceVisibility(a.safety, a.catalog.CachedGet, nil, nil, a.configuredLibraries())
	if !vis.Playable(ctx, viewer, title) {
		return nil, core.ErrNotFound
	}
	return title, nil
}
