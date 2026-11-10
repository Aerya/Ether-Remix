package music

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// API de la console (/ether/api/pse/music/playlists/) : comptes Deezer et
// Last.fm de chaque spectateur, playlists importées, import à la demande.

// PlaylistEvery renvoie la cadence d'import (PSE_MUSIC_PLAYLISTS_HOURS,
// 6 h par défaut, de 1 à 168 h).
func PlaylistEvery() time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(pseenv.Get("PSE_MUSIC_PLAYLISTS_HOURS")))
	if err != nil || n <= 0 {
		n = 6
	}
	return time.Duration(min(n, 168)) * time.Hour
}

// LastFMKey renvoie la clé API Last.fm (PSE_LASTFM_API_KEY).
func LastFMKey() string { return strings.TrimSpace(pseenv.Get("PSE_LASTFM_API_KEY")) }

// Viewer est un spectateur proposé dans la console.
type Viewer struct {
	ID   int64
	Name string
}

// PlaylistAPI sert la gestion des playlists dans la console.
type PlaylistAPI struct {
	Sync    *PlaylistSync
	Spotify *SpotifyPlaylists
	Viewers func(ctx context.Context) ([]Viewer, error)
	// Background lance une tâche détachée de la requête.
	Background func(fn func(ctx context.Context))
}

type apiPlaylist struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"`
	Tracks int    `json:"tracks"`
}

type apiViewer struct {
	ID        int64              `json:"id"`
	Name      string             `json:"name"`
	Accounts  map[string]Account `json:"accounts"`
	Playlists []apiPlaylist      `json:"playlists"`
	Spotify   bool               `json:"spotify"`
	Syncing   bool               `json:"syncing"`
}

func (a *PlaylistAPI) view(ctx context.Context) (map[string]any, error) {
	viewers, err := a.Viewers(ctx)
	if err != nil {
		return nil, err
	}
	accounts, err := a.Sync.Store.Accounts(ctx, 0)
	if err != nil {
		return nil, err
	}
	byViewer := map[int64]map[string]Account{}
	for _, ac := range accounts {
		if byViewer[ac.Viewer] == nil {
			byViewer[ac.Viewer] = map[string]Account{}
		}
		byViewer[ac.Viewer][ac.Service] = ac
	}
	out := make([]apiViewer, 0, len(viewers))
	for _, v := range viewers {
		av := apiViewer{ID: v.ID, Name: v.Name, Accounts: byViewer[v.ID], Playlists: []apiPlaylist{}, Syncing: a.Sync.Busy(v.ID)}
		if a.Spotify != nil {
			var n int
			_ = a.Sync.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM pse_music_spotify WHERE viewer_id=?`, v.ID).Scan(&n)
			av.Spotify = n > 0
		}
		if av.Accounts == nil {
			av.Accounts = map[string]Account{}
		}
		pls, _ := a.Sync.Store.List(ctx, v.ID)
		for _, pl := range pls {
			av.Playlists = append(av.Playlists, apiPlaylist{ID: pl.ID, Name: pl.Name, Source: pl.Source, Tracks: len(pl.Tracks)})
		}
		out = append(out, av)
	}
	return map[string]any{
		"lastfmKey": LastFMKey() != "",
		"hours":     int(PlaylistEvery() / time.Hour),
		"viewers":   out,
	}, nil
}

// Known indique si un spectateur existe.
func (a *PlaylistAPI) Known(ctx context.Context, id int64) bool {
	viewers, err := a.Viewers(ctx)
	if err != nil {
		return false
	}
	for _, v := range viewers {
		if v.ID == id {
			return true
		}
	}
	return false
}

func (a *PlaylistAPI) start(viewer int64) {
	run := a.Background
	if run == nil {
		run = func(fn func(ctx context.Context)) { go fn(context.Background()) }
	}
	a.Sync.mark(viewer, 1) // visible tout de suite dans la console
	run(func(ctx context.Context) {
		defer a.Sync.mark(viewer, -1)
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		_ = a.Sync.SyncViewer(ctx, viewer)
	})
}

// Handler renvoie le routeur de l'API.
func (a *PlaylistAPI) Handler() http.Handler {
	mux := http.NewServeMux()
	a.importRoutes(mux)
	reply := func(w http.ResponseWriter, r *http.Request) {
		v, err := a.view(r.Context())
		if err != nil {
			apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		apiJSON(w, http.StatusOK, v)
	}
	mux.HandleFunc("GET /{$}", reply)
	mux.HandleFunc("PUT /{$}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Viewer int64   `json:"viewer"`
			Deezer *string `json:"deezer"`
			LastFM *string `json:"lastfm"`
			LB     *string `json:"listenbrainz"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || !a.Known(r.Context(), body.Viewer) {
			apiJSON(w, http.StatusBadRequest, map[string]string{"error": "spectateur inconnu"})
			return
		}
		type change struct{ service, account string }
		var changes []change
		if body.Deezer != nil {
			v, err := ParseDeezerAccount(a.Sync.ExpandDeezerLinks(r.Context(), *body.Deezer))
			if err != nil {
				apiJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "field": ServiceDeezer})
				return
			}
			changes = append(changes, change{ServiceDeezer, v})
		}
		if body.LastFM != nil {
			v, err := ParseLastFMAccount(*body.LastFM)
			if err != nil {
				apiJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "field": ServiceLastFM})
				return
			}
			changes = append(changes, change{ServiceLastFM, v})
		}
		if body.LB != nil {
			v, err := ParseListenBrainzAccount(*body.LB)
			if err != nil {
				apiJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "field": ServiceListenBrainz})
				return
			}
			changes = append(changes, change{ServiceListenBrainz, v})
		}
		for _, c := range changes {
			if err := a.Sync.Store.SetAccount(r.Context(), body.Viewer, c.service, c.account); err != nil {
				apiJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		}
		a.start(body.Viewer)
		reply(w, r)
	})
	mux.HandleFunc("POST /sync", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Viewer int64 `json:"viewer"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || !a.Known(r.Context(), body.Viewer) {
			apiJSON(w, http.StatusBadRequest, map[string]string{"error": "spectateur inconnu"})
			return
		}
		if !a.Sync.Busy(body.Viewer) {
			a.start(body.Viewer)
		}
		reply(w, r)
	})
	return mux
}

func apiJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
