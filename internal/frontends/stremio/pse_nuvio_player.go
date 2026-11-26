package stremio

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// Ressources Stremio « player » et « library » : Nuvio TV (≥ 1.2.0-beta.2)
// y signale la lecture (début, pause, arrêt, position) et les actions de
// bibliothèque (ajout, retrait, vu, non vu). Le jeton du lien identifie le
// spectateur, comme pour /stream. La réponse est toujours `{}`.
// Voir docs/pse/FORK.fr.md et
// https://stremio.github.io/stremio-addon-sdk/protocol.html.

// pseNuvioWriteEvery : au plus une écriture de position par titre dans ce
// délai ; une pause ou un arrêt est toujours écrit.
const pseNuvioWriteEvery = 30 * time.Second

// pseManifestResources ajoute les ressources player et library à celles du
// manifeste.
func pseManifestResources(base []string) []any {
	out := make([]any, 0, len(base)+2)
	for _, r := range base {
		out = append(out, r)
	}
	for _, name := range []string{"player", "library"} {
		out = append(out, map[string]any{"name": name, "types": []string{"movie", "series"}, "idPrefixes": []string{"tt"}})
	}
	return out
}

// pseNuvioExtra décode l'extra : segment de chemin « a=b&c=d.json », chaîne
// de requête et, pour un POST, corps JSON ou formulaire.
func pseNuvioExtra(r *http.Request) url.Values {
	out := url.Values{}
	add := func(v url.Values) {
		for k, vs := range v {
			if len(vs) > 0 {
				out.Set(k, vs[0])
			}
		}
	}
	add(r.URL.Query())
	if raw := strings.TrimSuffix(r.PathValue("extra"), ".json"); raw != "" {
		if v, err := url.ParseQuery(raw); err == nil {
			add(v)
		}
	}
	if r.Method == http.MethodPost && r.Body != nil {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 16<<10))
		var m map[string]any
		if json.Unmarshal(body, &m) == nil {
			for k, v := range m {
				switch x := v.(type) {
				case string:
					out.Set(k, x)
				case float64:
					out.Set(k, strconv.FormatInt(int64(x), 10))
				case []any:
					parts := make([]string, 0, len(x))
					for _, e := range x {
						if s, ok := e.(string); ok {
							parts = append(parts, s)
						}
					}
					out.Set(k, strings.Join(parts, ","))
				}
			}
		} else if v, err := url.ParseQuery(string(body)); err == nil {
			add(v)
		}
	}
	return out
}

type pseNuvioRateKey struct {
	viewer int64
	key    frontend.RatingKey
}

var pseNuvioRates = struct {
	sync.Mutex
	last map[pseNuvioRateKey]time.Time
}{last: map[pseNuvioRateKey]time.Time{}}

// pseNuvioAllow dit si une écriture de position peut avoir lieu maintenant.
func pseNuvioAllow(viewer int64, key frontend.RatingKey, now time.Time, force bool) bool {
	pseNuvioRates.Lock()
	defer pseNuvioRates.Unlock()
	if len(pseNuvioRates.last) > 4096 {
		for k, t := range pseNuvioRates.last {
			if now.Sub(t) > time.Hour {
				delete(pseNuvioRates.last, k)
			}
		}
	}
	k := pseNuvioRateKey{viewer, key}
	if !force && now.Sub(pseNuvioRates.last[k]) < pseNuvioWriteEvery {
		return false
	}
	pseNuvioRates.last[k] = now
	return true
}

func (i *instance) registerPSENuvioRoutes(prefix string, wrap func(http.HandlerFunc) http.HandlerFunc) error {
	for _, res := range []struct {
		name    string
		handler http.HandlerFunc
	}{{"player", i.pseNuvioPlayer}, {"library", i.pseNuvioLibrary}} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			for _, pattern := range []string{prefix + "/" + res.name + "/{type}/{id}", prefix + "/" + res.name + "/{type}/{id}/{extra}"} {
				if err := i.router.Handle(method, pattern, wrap(res.handler)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// pseNuvioStart identifie le spectateur et l'identifiant ; une réponse a
// déjà été écrite quand ok est faux.
func (i *instance) pseNuvioStart(w http.ResponseWriter, r *http.Request) (viewer *frontend.Viewer, device frontend.Device, typ, rawID string, ok bool) {
	credential := r.PathValue("credential")
	viewer, err := i.host.Identity.Resolve(r.Context(), credential)
	if err != nil || viewer == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return nil, frontend.Device{}, "", "", false
	}
	i.observeClient(r, credential, viewer)
	typ, rawID = r.PathValue("type"), r.PathValue("id")
	if r.PathValue("extra") == "" {
		rawID = strings.TrimSuffix(rawID, ".json")
	}
	return viewer, deviceFor(credential), typ, rawID, true
}

func pseNuvioDone(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte("{}"))
}

// pseNuvioKey résout un identifiant Stremio en clé d'Ether : un épisode
// (tt:s:e) ou un titre (film, série).
func (i *instance) pseNuvioKey(r *http.Request, viewer *frontend.Viewer, device frontend.Device, typ, raw string) (frontend.RatingKey, bool) {
	id, err := ids.ParseStremio(raw)
	if err != nil {
		return 0, false
	}
	key, err := i.resolveCatalogID(r, viewer, device, typ, raw, id.HasEpisode)
	return key, err == nil
}

// pseNuvioKeyFor résout l'identifiant par le catalogue quand l'écriture directe
// est branchée (sans test de visibilité), sinon par l'expérience.
func (i *instance) pseNuvioKeyFor(r *http.Request, backend frontend.PSENuvioBackend, viewer *frontend.Viewer, device frontend.Device, typ, raw string) (frontend.RatingKey, bool) {
	if backend == nil {
		return i.pseNuvioKey(r, viewer, device, typ, raw)
	}
	id, err := ids.ParseStremio(raw)
	if err != nil || id.Source != "imdb" {
		return 0, false
	}
	switch {
	case typ == "movie" && !id.HasEpisode:
		return backend.Resolve(r.Context(), id.ID, "movie", 0, 0)
	case typ == "series" && id.HasEpisode:
		return backend.Resolve(r.Context(), id.ID, "episode", id.Season, id.Episode)
	}
	return 0, false
}

func (i *instance) pseNuvioPlayer(w http.ResponseWriter, r *http.Request) {
	viewer, device, typ, raw, ok := i.pseNuvioStart(w, r)
	if !ok {
		return
	}
	defer pseNuvioDone(w)
	backend := frontend.PSENuvio()
	writer, isWriter := i.host.Experience.(frontend.OfflineProgressWriter)
	if backend == nil && !isWriter {
		return
	}
	extra := pseNuvioExtra(r)
	action := extra.Get("action")
	if action != "start" && action != "pause" && action != "stop" {
		return
	}
	pos, err1 := strconv.ParseInt(extra.Get("currentTime"), 10, 64)
	dur, _ := strconv.ParseInt(extra.Get("duration"), 10, 64)
	if err1 != nil || pos < 0 || dur < 0 {
		return
	}
	key, ok := i.pseNuvioKeyFor(r, backend, viewer, device, typ, raw)
	if !ok {
		return
	}
	now := time.Now()
	if !pseNuvioAllow(viewer.ID, key, now, action != "start") {
		return
	}
	sample := frontend.OfflineProgress{
		Position: time.Duration(pos) * time.Millisecond, Duration: time.Duration(dur) * time.Millisecond,
		ObservedAt: now, ReceivedAt: now,
		Played: action == "stop" && dur > 0 && float64(pos) >= 0.9*float64(dur),
	}
	frontend.PSENuvioIncoming(viewer.ID, key)
	ctx := frontend.WithWatchOrigin(r.Context(), frontend.WatchOrigin{Surface: "nuvio", ClientID: device.ID})
	if backend != nil {
		_ = backend.Progress(ctx, viewer.ID, key, sample) // historique écrit même si le titre n'est plus visible
		return
	}
	_ = writer.SetOfflineProgress(ctx, viewer, key, sample)
}

func (i *instance) pseNuvioLibrary(w http.ResponseWriter, r *http.Request) {
	viewer, device, typ, raw, ok := i.pseNuvioStart(w, r)
	if !ok {
		return
	}
	defer pseNuvioDone(w)
	backend := frontend.PSENuvio()
	extra := pseNuvioExtra(r)
	ctx := frontend.WithWatchOrigin(r.Context(), frontend.WatchOrigin{Surface: "nuvio", ClientID: device.ID})
	switch action := extra.Get("action"); action {
	case "libraryAdd", "libraryRemove":
		key, ok := i.pseNuvioKey(r, viewer, device, typ, raw)
		if ok {
			_ = i.host.Experience.SetWatchlist(ctx, viewer, key, action == "libraryAdd")
		}
	case "watched", "unwatched":
		videoIDs, explicit := []string{raw}, false
		if v := strings.TrimSpace(extra.Get("videoId")); v != "" {
			videoIDs, explicit = strings.Split(v, ","), true
		}
		if len(videoIDs) > 200 {
			videoIDs = videoIDs[:200]
		}
		for _, vid := range videoIDs {
			key, ok := i.pseNuvioKeyFor(r, backend, viewer, device, typ, strings.TrimSpace(vid))
			// Sans videoId, seul un film se marque : une série entière ne l'est pas.
			if !ok || (!explicit && key.Kind() != frontend.KindMovie) {
				continue
			}
			frontend.PSENuvioIncoming(viewer.ID, key)
			if backend != nil {
				_ = backend.Played(ctx, viewer.ID, key, action == "watched")
			} else if action == "watched" {
				_ = i.host.Experience.SetPlayed(ctx, viewer, key)
			} else {
				_ = i.host.Experience.SetUnplayed(ctx, viewer, key)
			}
		}
	}
}
