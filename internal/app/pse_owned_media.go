package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
	"net/http"
	"strconv"
	"strings"
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
		accounts, err := a.ibAccounts(ctx, 0)
		if err != nil {
			return err
		}
		for _, ac := range accounts {
			ib, e := a.ibAccount(ctx, ac.ID)
			if e != nil {
				return e
			}
			if ib.Status(ctx).Connected {
				if e = ib.Sync(ctx); e != nil {
					a.log.Warn("synchronisation iBroadcast", "account", ac.ID, "err", e)
				}
			}
		}
		return nil
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
		key := f.TitleKey
		if key == 0 {
			key = a.catalog.MatchLocalVideo(ctx, f)
		}
		if key != 0 {
			f.TitleKey = key
			raw, _ := json.Marshal(f)
			if _, err := a.db.ExecContext(ctx, "UPDATE pse_local_files SET title_key=?,data=? WHERE id=?", key, string(raw), f.ID); err != nil {
				return err
			}
			continue
		}
		key = core.LocalKey(f.ID)
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

	spec := core.CollectionSpec{Name: r.Name, MediaType: "mixed", Sources: []core.CollectionSource{{Kind: "localFiles", Label: r.ID}}, Match: "all", Sort: "title", Refresh: "live", Enabled: true, LocalOnly: true, TemplateID: marker}
	var saved *core.CollectionSpec
	if existing != nil {
		spec = *existing
		spec.Sources = []core.CollectionSource{{Kind: "localFiles", Label: r.ID}}
		spec.Rules = nil
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
	_, err = th.Create(ctx, themes.Library{Name: r.Name, Type: themes.TypeMixed, Collections: []string{saved.ID}, AllLanguages: true})
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
		ctx, actor, admin := a.ownedActor(r)
		if actor <= 0 {
			http.Error(w, "authentification requise", 401)
			return
		}
		r = r.WithContext(ctx)
		if strings.HasPrefix(r.URL.Path, "/local") && !admin {
			http.Error(w, "administrateur requis", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		fail := func(err error) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "owned_media", "message": err.Error()}})
		}
		decode := func(v any) error { return json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(v) }
		var ib *music.IBroadcast
		if strings.HasPrefix(r.URL.Path, "/ibroadcast") {
			id, _ := strconv.ParseInt(r.URL.Query().Get("account"), 10, 64)
			policy, e := mediaaccess.Read(ctx, a.db, "ibroadcast:"+strconv.FormatInt(id, 10))
			if e != nil || policy.OwnerID != actor {
				http.Error(w, "compte introuvable", 404)
				return
			}
			ib, e = a.ibAccount(ctx, id)
			if e != nil {
				fail(e)
				return
			}
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /accounts":
			accounts, err := a.ibAccounts(ctx, actor)
			if err != nil {
				fail(err)
				return
			}
			json.NewEncoder(w).Encode(accounts)
			return
		case "POST /accounts":
			var b struct {
				Name string `json:"name"`
			}
			if err := decode(&b); err != nil {
				fail(err)
				return
			}
			b.Name = strings.TrimSpace(b.Name)
			if b.Name == "" || len(b.Name) > 120 {
				fail(errors.New("nom de compte requis"))
				return
			}
			id, err := a.createIBAccount(ctx, actor, b.Name)
			if err != nil {
				fail(err)
				return
			}
			json.NewEncoder(w).Encode(map[string]int64{"id": id})
			return
		case "PUT /sharing":
			var b struct {
				Source          string             `json:"source"`
				Policy          mediaaccess.Policy `json:"policy"`
				ImportPlaylists bool               `json:"importPlaylists"`
			}
			if err := decode(&b); err != nil {
				fail(err)
				return
			}
			if err := mediaaccess.Update(ctx, a.db, b.Source, actor, b.Policy); err != nil {
				fail(err)
				return
			}
			if strings.HasPrefix(b.Source, "ibroadcast:") {
				id, _ := strconv.ParseInt(strings.TrimPrefix(b.Source, "ibroadcast:"), 10, 64)
				if _, err := a.db.ExecContext(ctx, "UPDATE pse_ib_accounts SET import_playlists=? WHERE id=? AND owner_user_id=?", b.ImportPlaylists, id, actor); err != nil {
					fail(err)
					return
				}
				wakeOwned(rt.ibKick)
			}
			a.catalog.LocalMediaChanged()
			a.reloadOwnedMusic()
		case "GET /viewers":
			viewers, err := a.pseViewers(ctx)
			if err != nil {
				fail(err)
				return
			}
			out := []map[string]any{}
			for _, v := range viewers {
				out = append(out, map[string]any{"id": v.ID, "name": v.Name})
			}
			json.NewEncoder(w).Encode(out)
			return
		case "GET /local/health":
			health, err := rt.local.Health(ctx)
			if err != nil {
				fail(err)
				return
			}
			_ = json.NewEncoder(w).Encode(health)
			return
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
			_ = json.NewEncoder(w).Encode(ib.Status(r.Context()))
			return
		case "POST /ibroadcast/connect":
			var b struct {
				Client string `json:"clientId"`
			}
			if err := decode(&b); err != nil {
				fail(err)
				return
			}
			if err := ib.Begin(r.Context(), b.Client); err != nil {
				fail(err)
				return
			}
			_ = json.NewEncoder(w).Encode(ib.Status(r.Context()))
			return
		case "POST /ibroadcast/poll":
			connected, err := ib.Poll(r.Context())
			if err != nil {
				fail(err)
				return
			}
			if connected {
				wakeOwned(rt.ibKick)
			}
			_ = json.NewEncoder(w).Encode(ib.Status(r.Context()))
			return
		case "POST /ibroadcast/sync":
			if !ib.Status(r.Context()).Connected {
				fail(errors.New("connectez d’abord le compte iBroadcast"))
				return
			}
			wakeOwned(rt.ibKick)
		case "DELETE /ibroadcast":
			if err := ib.Disconnect(r.Context()); err != nil {
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
