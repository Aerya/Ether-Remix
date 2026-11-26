package app

import (
	"context"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/segcache"
)

// validateExtra is the app-level config validation.
func (a *App) validateExtra(c config.Config) []admin.FieldError {
	errs := append(validatePlaybackConfig(c), a.validateIndexerSites(c)...)
	errs = append(errs, a.validateQualityRuleRefs(c)...)
	if a.resolver != nil {
		for i, p := range c.Quality.Profiles {
			for step, msg := range a.resolver.ValidateRankingConfig(p) {
				errs = append(errs, admin.FieldError{Field: fmt.Sprintf("quality.profiles[%d].pluginScores.%s", i, step), Message: msg})
			}
		}
	}
	if c.Cache.Storage == "shared" {
		if err := segcache.ValidateSharedMount(c.Cache.Dir, a.db); err != nil {
			errs = append(errs, admin.FieldError{Field: "cache.dir", Message: err.Error()})
		}
	}
	return errs
}

// validateQualityRuleRefs verifies that every library and viewer named by a
// quality rule exists.
func (a *App) validateQualityRuleRefs(c config.Config) []admin.FieldError {
	if len(c.Quality.Rules) == 0 {
		return nil
	}
	needsLibs, needsViewers := false, false
	for _, r := range c.Quality.Rules {
		if len(r.Libraries) > 0 {
			needsLibs = true
		}
		if len(r.Viewers) > 0 {
			needsViewers = true
		}
	}
	if !needsLibs && !needsViewers {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var errs []admin.FieldError
	var libIDs map[int64]bool
	if needsLibs && a.libraries != nil {
		if libs, err := a.libraries.List(ctx); err == nil {
			libIDs = make(map[int64]bool, len(libs))
			for _, lib := range libs {
				libIDs[int64(lib.ID)] = true
			}
		}
	}
	var viewerIDs map[int64]bool
	if needsViewers && a.users != nil {
		if users, err := a.users.AllUsers(ctx); err == nil {
			viewerIDs = make(map[int64]bool, len(users))
			for _, u := range users {
				viewerIDs[u.ID] = true
			}
		}
	}
	for i, r := range c.Quality.Rules {
		f := fmt.Sprintf("quality.rules[%d]", i)
		if libIDs != nil {
			for _, id := range r.Libraries {
				if id <= 0 || !libIDs[id] {
					errs = append(errs, admin.FieldError{Field: f + ".libraries", Message: fmt.Sprintf("names a library (%d) that does not exist", id)})
					break
				}
			}
		}
		if viewerIDs != nil {
			for _, id := range r.Viewers {
				if id <= 0 || !viewerIDs[id] {
					errs = append(errs, admin.FieldError{Field: f + ".viewers", Message: fmt.Sprintf("names a viewer (%d) that does not exist", id)})
					break
				}
			}
		}
	}
	_ = core.LibraryMovies
	return errs
}

// validateIndexerSites keeps password-type settings of built-in sites out
// of the plain settings map, where they would be shown unredacted.
func (a *App) validateIndexerSites(c config.Config) []admin.FieldError {
	var errs []admin.FieldError
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i, ix := range c.Indexers {
		if !ix.IsBuiltin() || len(ix.Settings) == 0 {
			continue
		}
		def, err := a.sites.Definition(ctx, ix.Definition)
		if err != nil {
			continue // a missing definition surfaces when the indexer is searched
		}
		for _, s := range def.Settings {
			if _, plain := ix.Settings[s.Name]; plain && s.Secret() {
				errs = append(errs, admin.FieldError{Field: fmt.Sprintf("indexers[%d].settings.%s", i, s.Name), Message: "is a password; store it in secretSettings"})
			}
		}
	}
	return errs
}
