package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/pse/localmedia"
	"github.com/braedonsaunders/ether/internal/pse/music"
	"github.com/braedonsaunders/ether/internal/pse/themes"
)

func (a *App) ownedMedia() {
	rt := pseRuntimeOf(a)
	rt.ownedOnce.Do(func() {
		rt.local = &localmedia.Library{DB: a.db, Publish: a.publishLocalMedia}
		rt.ib = &music.IBroadcast{DB: a.db, Sealer: a.cfg, HTTP: egress.Client(egress.CapMetadata, 90*time.Second)}
		rt.localKick = make(chan struct{}, 1)
		rt.ibKick = make(chan struct{}, 1)
	})
}
func (a *App) startOwnedMedia() {
	a.ownedMedia()
	rt := pseRuntimeOf(a)
	ctx, cancel := context.WithCancel(context.Background())
	a.closers = append(a.closers, cancel)
	run := func(kick <-chan struct{}, fn func(context.Context) error) {
		go func() {
			timer := time.NewTicker(time.Hour)
			defer timer.Stop()
			for {
				c, stop := context.WithTimeout(ctx, 45*time.Minute)
				err := fn(c)
				stop()
				if err != nil && ctx.Err() == nil {
					a.log.Warn("synchronisation des médias personnels", "err", err)
				}
				a.reloadOwnedMusic()
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				case <-kick:
				}
			}
		}()
	}
	run(rt.localKick, rt.local.Scan)
	run(rt.ibKick, func(ctx context.Context) error {
		if !rt.ib.Status(ctx).Connected {
			return nil
		}
		return rt.ib.Sync(ctx)
	})
}
func (a *App) reloadOwnedMusic() {
	rt := pseRuntimeOf(a)
	rt.mu.Lock()
	rt.musicConf.Quality = "reload-owned"
	rt.mu.Unlock()
	rt.reloadMusic(a)
}
func (a *App) hasOwnedMusic(ctx context.Context) bool {
	var n int
	return a.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pse_music_owned").Scan(&n) == nil && n > 0
}
func wakeOwned(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (a *App) publishLocalMedia(ctx context.Context, r localmedia.Root, files []localmedia.File) error {
	if r.Kind == "audio" {
		return music.ImportLocal(ctx, a.db, r, files)
	}
	for _, f := range files {
		key := core.LocalKey(f.ID)
		when := time.Unix(0, f.Modified)
		if when.After(time.Now()) {
			when = time.Now()
		}
		title := core.Title{Key: key, Type: core.TypeMovie, Title: f.Name, SortTitle: f.Name, Runtime: time.Duration(f.Duration) * time.Second, Released: when, AddedAt: when, HomeReleaseChecked: true, HomeReleased: when, Genres: []string{"Local · " + r.Name}, Summary: "Vidéo locale · " + r.Name}
		if err := a.catalog.UpsertDetailedTitle(ctx, title); err != nil {
			return err
		}
	}
	a.catalog.LocalMediaChanged()
	if a.collections == nil {
		return nil
	}
	specs, err := a.collections.ListSpecs(ctx)
	if err != nil {
		return err
	}
	var existing *core.CollectionSpec
	marker := "remix-local-" + r.ID
	for i := range specs {
		if specs[i].TemplateID == marker {
			existing = &specs[i]
			break
		}
	}
	genre, _ := json.Marshal("Local · " + r.Name)
	spec := core.CollectionSpec{Name: r.Name, MediaType: "movie", Sources: []core.CollectionSource{{Kind: "catalog"}}, Rules: []core.CollectionRule{{Field: "genre", Op: "is", Value: genre}}, Match: "all", Sort: "title", Refresh: "live", Enabled: true, LocalOnly: true, TemplateID: marker}
	var saved *core.CollectionSpec
	if existing != nil {
		spec = *existing
		spec.Sources = []core.CollectionSource{{Kind: "catalog"}}
		spec.Rules = []core.CollectionRule{{Field: "genre", Op: "is", Value: genre}}
		saved, err = a.collections.UpdateSpec(ctx, existing.ID, spec)
	} else {
		saved, err = a.collections.CreateSpec(ctx, spec)
	}
	if err != nil {
		return err
	}
	th := a.pseThemes()
	if th == nil {
		return nil
	}
	if err = th.Reload(ctx); err != nil {
		return err
	}
	for _, lib := range th.Libraries() {
		for _, id := range lib.Collections {
			if id == saved.ID {
				return nil
			}
		}
	}
	_, err = th.Create(ctx, themes.Library{Name: r.Name, Type: themes.TypeMovie, Collections: []string{saved.ID}, AllLanguages: true})
	return err
}
func (a *App) removeLocalMedia(ctx context.Context, id string) error {
	rt := pseRuntimeOf(a)
	if err := rt.local.Remove(ctx, id); err != nil {
		return err
	}
	if _, err := a.db.ExecContext(ctx, "DELETE FROM pse_music_owned WHERE provider=?", "local:"+id); err != nil {
		return err
	}
	a.catalog.LocalMediaChanged()
	// The dedicated library is removed only if it contains this source alone.
	if a.collections != nil {
		specs, err := a.collections.ListSpecs(ctx)
		if err != nil {
			return err
		}
		for _, sp := range specs {
			if sp.TemplateID != "remix-local-"+id {
				continue
			}
			if th := a.pseThemes(); th != nil {
				for _, lib := range th.Libraries() {
					if len(lib.Collections) == 1 && lib.Collections[0] == sp.ID {
						if err = th.Delete(ctx, lib.ID); err != nil {
							return err
						}
					}
				}
			}
			if err = a.collections.DeleteSpec(ctx, sp.ID); err != nil {
				return err
			}
		}
	}
	a.reloadOwnedMusic()
	return nil
}
func (a *App) ownedMediaAPI() http.Handler {
	a.ownedMedia()
	rt := pseRuntimeOf(a)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		fail := func(err error) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		}
		decode := func(v any) error { return json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(v) }
		switch r.Method + " " + r.URL.Path {
		case "GET /local":
			roots, err := rt.local.Roots(r.Context())
			if err != nil {
				fail(err)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"roots": roots, "status": rt.local.Status()})
			return
		case "POST /local":
			var root localmedia.Root
			if err := decode(&root); err != nil {
				fail(err)
				return
			}
			if err := rt.local.Add(r.Context(), root); err != nil {
				fail(err)
				return
			}
			wakeOwned(rt.localKick)
		case "POST /local/scan":
			wakeOwned(rt.localKick)
		case "DELETE /local":
			if err := a.removeLocalMedia(r.Context(), r.URL.Query().Get("id")); err != nil {
				fail(err)
				return
			}
		case "GET /ibroadcast":
			_ = json.NewEncoder(w).Encode(rt.ib.Status(r.Context()))
			return
		case "POST /ibroadcast/connect":
			var b struct {
				Client string `json:"clientId"`
			}
			if err := decode(&b); err != nil {
				fail(err)
				return
			}
			if err := rt.ib.Begin(r.Context(), b.Client); err != nil {
				fail(err)
				return
			}
			_ = json.NewEncoder(w).Encode(rt.ib.Status(r.Context()))
			return
		case "POST /ibroadcast/poll":
			connected, err := rt.ib.Poll(r.Context())
			if err != nil {
				fail(err)
				return
			}
			if connected {
				wakeOwned(rt.ibKick)
			}
			_ = json.NewEncoder(w).Encode(rt.ib.Status(r.Context()))
			return
		case "POST /ibroadcast/sync":
			if !rt.ib.Status(r.Context()).Connected {
				fail(errors.New("connectez d’abord le compte iBroadcast"))
				return
			}
			wakeOwned(rt.ibKick)
		case "DELETE /ibroadcast":
			if err := rt.ib.Disconnect(r.Context()); err != nil {
				fail(err)
				return
			}
			a.reloadOwnedMusic()
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
}
