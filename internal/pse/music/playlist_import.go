package music

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type playlistChoice struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type playlistEntry struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	track  *Track
}
type playlistPreview struct {
	Token     string          `json:"token"`
	Name      string          `json:"name"`
	Source    string          `json:"source"`
	URL       string          `json:"url,omitempty"`
	Tracks    []playlistEntry `json:"tracks"`
	Total     int             `json:"total"`
	Skipped   int             `json:"skipped"`
	Truncated bool            `json:"truncated"`
}
type playlistImportJob struct {
	ID         string          `json:"id"`
	Done       bool            `json:"done"`
	Processed  int             `json:"processed"`
	Total      int             `json:"total"`
	Matched    int             `json:"matched"`
	Missing    []playlistEntry `json:"missing"`
	Error      string          `json:"error,omitempty"`
	PlaylistID int64           `json:"playlistId,omitempty"`
}
type importState struct {
	viewer  int64
	until   time.Time
	preview playlistPreview
	job     *playlistImportJob
}
type playlistImports struct {
	mu     sync.Mutex
	states map[string]*importState
}

func parsePlaylistURL(raw string) (service, id string, err error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return "", "", errors.New("collez un lien HTTPS Deezer, Spotify ou ListenBrainz")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 3 && len(parts[0]) == 2 && (u.Hostname() == "www.deezer.com" || u.Hostname() == "deezer.com") {
		parts = parts[1:]
	}
	if len(parts) != 2 || parts[0] != "playlist" {
		return "", "", errors.New("lien de playlist invalide")
	}
	id = parts[1]
	switch strings.ToLower(u.Hostname()) {
	case "deezer.com", "www.deezer.com":
		if isDigits(id) && len(id) > 0 && len(id) <= 20 {
			return "deezer", id, nil
		}
	case "open.spotify.com":
		if spotifyID.MatchString(id) {
			return "spotify", id, nil
		}
	case "listenbrainz.org":
		if len(id) == 36 && lbMBIDRe.FindString(id) == id {
			return "listenbrainz", id, nil
		}
	}
	return "", "", errors.New("playlist non reconnue ; pour un autre service, importez un fichier CSV ou XSPF")
}
func parsePlaylistFile(name, format, raw string) (playlistPreview, error) {
	p := playlistPreview{Name: clipName(name), Source: "file", Tracks: []playlistEntry{}}
	if p.Name == "" {
		return p, errors.New("donnez un nom à la playlist")
	}
	add := func(artist, title string) {
		p.Total++
		if len(p.Tracks) >= MaxUserTracks {
			p.Truncated = true
			return
		}
		artist = strings.TrimSpace(artist)
		title = strings.TrimSpace(title)
		if artist == "" || title == "" {
			p.Skipped++
			return
		}
		p.Tracks = append(p.Tracks, playlistEntry{Title: title, Artist: artist})
	}
	switch format {
	case "csv":
		raw = strings.TrimPrefix(raw, "\ufeff")
		r := csv.NewReader(strings.NewReader(raw))
		r.FieldsPerRecord = -1
		first := strings.SplitN(raw, "\n", 2)[0]
		if strings.Count(first, ";") > strings.Count(first, ",") {
			r.Comma = ';'
		}
		headers, err := r.Read()
		if err != nil {
			return p, errors.New("CSV illisible")
		}
		ai, ti := -1, -1
		for i, h := range headers {
			switch strings.ToLower(strings.TrimSpace(h)) {
			case "artist", "artists", "artist name(s)", "artist name", "artiste":
				ai = i
			case "title", "track name", "track", "titre":
				ti = i
			}
		}
		if ai < 0 || ti < 0 {
			return p, errors.New("CSV : colonnes title et artist requises")
		}
		for {
			row, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return p, errors.New("CSV mal formé")
			}
			if ai >= len(row) || ti >= len(row) {
				return p, errors.New("CSV : ligne incomplète")
			}
			add(row[ai], row[ti])
		}
	case "xspf":
		var doc struct {
			XMLName xml.Name `xml:"playlist"`
			Title   string   `xml:"title"`
			Tracks  []struct {
				Title   string `xml:"title"`
				Creator string `xml:"creator"`
			} `xml:"trackList>track"`
		}
		if err := xml.Unmarshal([]byte(raw), &doc); err != nil {
			return p, errors.New("XSPF illisible")
		}
		for _, t := range doc.Tracks {
			add(t.Creator, t.Title)
		}
	default:
		return p, errors.New("formats acceptés : CSV et XSPF")
	}
	if len(p.Tracks) == 0 {
		return p, errors.New("aucun titre avec artiste dans ce fichier")
	}
	return p, nil
}
func (a *PlaylistAPI) preview(ctx context.Context, viewer int64, raw string) (playlistPreview, error) {
	service, id, err := parsePlaylistURL(a.Sync.ExpandDeezerLinks(ctx, raw))
	if err != nil {
		return playlistPreview{}, err
	}
	p := playlistPreview{Source: service, URL: raw, Tracks: []playlistEntry{}}
	switch service {
	case "spotify":
		if a.Spotify == nil {
			return p, errors.New("Spotify indisponible")
		}
		return a.Spotify.preview(ctx, viewer, id)
	case "deezer":
		var meta dzPlaylist
		if err := a.Sync.dz(ctx, "/playlist/"+id, nil, &meta); err != nil {
			return p, err
		}
		p.Name = meta.Title
		p.Total = meta.NbTracks
		pl, err := a.Sync.deezerPlaylist(ctx, meta)
		if err != nil {
			return p, err
		}
		for _, t := range pl.Tracks {
			t := t
			p.Tracks = append(p.Tracks, playlistEntry{Title: t.Title, Artist: t.Artist.Name, track: &t})
		}
		p.Truncated = p.Total > MaxUserTracks
		p.Skipped = max(0, min(p.Total, MaxUserTracks)-len(p.Tracks))
	case "listenbrainz":
		var full lbList
		if err := a.Sync.lb(ctx, "/1/playlist/"+id, nil, &full); err != nil {
			return p, err
		}
		p.Name = full.Playlist.Title
		p.Total = len(full.Playlist.Track)
		for i, t := range full.Playlist.Track {
			if i >= MaxUserTracks {
				p.Truncated = true
				break
			}
			if t.Title == "" || t.Creator == "" {
				p.Skipped++
				continue
			}
			p.Tracks = append(p.Tracks, playlistEntry{Title: t.Title, Artist: t.Creator})
		}
	}
	return p, nil
}
func (a *PlaylistAPI) importRoutes(mux *http.ServeMux) {
	states := &playlistImports{states: map[string]*importState{}}
	fail := func(w http.ResponseWriter, err error) { apiJSON(w, 400, map[string]string{"error": err.Error()}) }
	mux.HandleFunc("GET /discover", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offset < 0 || offset > 1000 {
			fail(w, errors.New("page invalide"))
			return
		}
		service := r.URL.Query().Get("service")
		var lists []playlistChoice
		var more bool
		var err error
		if service == "spotify" {
			viewer, _ := strconv.ParseInt(r.URL.Query().Get("viewer"), 10, 64)
			if !a.Known(ctx, viewer) || a.Spotify == nil {
				fail(w, errors.New("spectateur inconnu ou Spotify indisponible"))
				return
			}
			lists, more, err = a.Spotify.lists(ctx, viewer, offset)
		} else if service == "deezer" {
			q := strings.TrimSpace(r.URL.Query().Get("q"))
			if len(q) > 200 {
				fail(w, errors.New("recherche trop longue"))
				return
			}
			path := "/chart/0/playlists"
			args := url.Values{"limit": {"20"}, "index": {strconv.Itoa(offset)}}
			if q != "" {
				path = "/search/playlist"
				args.Set("q", q)
			}
			var result struct {
				Data []dzPlaylist
				Next string
			}
			err = a.Sync.dz(ctx, path, args, &result)
			lists = []playlistChoice{}
			for _, p := range result.Data {
				lists = append(lists, playlistChoice{Name: p.Title, URL: fmt.Sprintf("https://www.deezer.com/playlist/%d", p.ID)})
			}
			more = result.Next != ""
		} else {
			err = errors.New("choisissez Deezer ou Spotify")
		}
		if err != nil {
			fail(w, err)
			return
		}
		apiJSON(w, 200, map[string]any{"items": lists, "more": more})
	})
	mux.HandleFunc("POST /preview", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Viewer                  int64
			URL, Name, Format, File string
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&body) != nil || !a.Known(r.Context(), body.Viewer) {
			fail(w, errors.New("import invalide ou spectateur inconnu (2 Mo maximum)"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		var p playlistPreview
		var err error
		if body.File != "" {
			p, err = parsePlaylistFile(body.Name, body.Format, body.File)
		} else {
			p, err = a.preview(ctx, body.Viewer, body.URL)
		}
		if err != nil {
			fail(w, err)
			return
		}
		if len(p.Tracks) == 0 {
			fail(w, errors.New("aucun titre accessible dans cette playlist"))
			return
		}
		p.Token = randomToken()
		states.mu.Lock()
		defer states.mu.Unlock()
		for k, s := range states.states {
			if time.Now().After(s.until) && (s.job == nil || s.job.Done) {
				delete(states.states, k)
			}
		}
		if len(states.states) >= 100 {
			fail(w, errors.New("trop d'imports récents ; réessayez plus tard"))
			return
		}
		states.states[p.Token] = &importState{viewer: body.Viewer, until: time.Now().Add(time.Hour), preview: p}
		apiJSON(w, 200, p)
	})
	mux.HandleFunc("POST /import", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Viewer int64
			Token  string
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || !a.Known(r.Context(), body.Viewer) {
			fail(w, errors.New("spectateur inconnu"))
			return
		}
		states.mu.Lock()
		s := states.states[body.Token]
		if s == nil || s.viewer != body.Viewer || time.Now().After(s.until) {
			states.mu.Unlock()
			fail(w, errors.New("aperçu expiré ; recommencez"))
			return
		}
		if s.job != nil {
			job := *s.job
			states.mu.Unlock()
			apiJSON(w, 200, job)
			return
		}
		for _, active := range states.states {
			if active.viewer == body.Viewer && active.job != nil && !active.job.Done {
				states.mu.Unlock()
				fail(w, errors.New("un import est déjà en cours pour ce spectateur"))
				return
			}
		}
		s.job = &playlistImportJob{ID: body.Token, Total: len(s.preview.Tracks), Missing: []playlistEntry{}}
		job := *s.job
		states.mu.Unlock()
		a.Sync.mark(body.Viewer, 1)
		run := a.Background
		if run == nil {
			run = func(fn func(context.Context)) { go fn(context.Background()) }
		}
		run(func(ctx context.Context) {
			defer a.Sync.mark(body.Viewer, -1)
			ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			defer cancel()
			tracks := []Track{}
			var matchErr error
			for _, entry := range s.preview.Tracks {
				if ctx.Err() != nil {
					break
				}
				t, ok := Track{}, false
				if entry.track != nil {
					t, ok = *entry.track, true
				} else {
					t, ok, matchErr = a.Sync.findTrackResult(ctx, entry.Artist, entry.Title)
					if matchErr != nil {
						break
					}
				}
				states.mu.Lock()
				s.job.Processed++
				if ok {
					tracks = append(tracks, t)
					s.job.Matched++
				} else {
					s.job.Missing = append(s.job.Missing, entry)
				}
				states.mu.Unlock()
			}
			var pl Playlist
			err := errors.Join(ctx.Err(), matchErr)
			if err == nil {
				if len(tracks) == 0 {
					err = errors.New("aucun titre retrouvé ; aucune playlist créée")
				} else {
					pl, err = a.Sync.Store.CreatePersonal(ctx, body.Viewer, s.preview.Name, tracks)
				}
			}
			states.mu.Lock()
			defer states.mu.Unlock()
			s.job.Done = true
			s.job.PlaylistID = pl.ID
			if err != nil {
				s.job.Error = err.Error()
			}
			s.until = time.Now().Add(time.Hour)
		})
		apiJSON(w, 202, job)
	})
	mux.HandleFunc("GET /import", func(w http.ResponseWriter, r *http.Request) {
		viewer, _ := strconv.ParseInt(r.URL.Query().Get("viewer"), 10, 64)
		if !a.Known(r.Context(), viewer) {
			fail(w, errors.New("spectateur inconnu"))
			return
		}
		states.mu.Lock()
		defer states.mu.Unlock()
		s := states.states[r.URL.Query().Get("id")]
		if s == nil || s.viewer != viewer || s.job == nil {
			fail(w, errors.New("suivi indisponible (serveur redémarré ou import expiré)"))
			return
		}
		apiJSON(w, 200, s.job)
	})
	if a.Spotify != nil {
		a.spotifyRoutes(mux)
	}
}
func (a *PlaylistAPI) spotifyRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /spotify/{action}", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Viewer                             int64
			ClientID, RedirectURI, State, Code string
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&b) != nil || !a.Known(r.Context(), b.Viewer) {
			apiJSON(w, 400, map[string]string{"error": "spectateur inconnu"})
			return
		}
		var err error
		out := map[string]string{}
		switch r.PathValue("action") {
		case "connect":
			out["url"], err = a.Spotify.begin(b.Viewer, b.ClientID, b.RedirectURI)
		case "callback":
			err = a.Spotify.finish(r.Context(), b.Viewer, b.State, b.Code)
		case "disconnect":
			a.Spotify.mu.Lock()
			_, err = a.Sync.Store.DB.ExecContext(r.Context(), `DELETE FROM pse_music_spotify WHERE viewer_id=?`, b.Viewer)
			for k, p := range a.Spotify.pending {
				if p.Viewer == b.Viewer {
					delete(a.Spotify.pending, k)
				}
			}
			a.Spotify.mu.Unlock()
		default:
			err = errors.New("action inconnue")
		}
		if err != nil {
			apiJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		apiJSON(w, 200, out)
	})
}
