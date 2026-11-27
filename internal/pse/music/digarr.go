package music

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/pse/pseenv"
)

// Digarr is opt-in, with a personal read-only token for each Remix viewer.
// Never fall back to another viewer's token or forward upstream errors.
type DigarrConfig struct{ URL, Token string }

func (a *PlaylistAPI) digarrConfig(viewer int64) DigarrConfig {
	if a.Digarr != nil {
		return a.Digarr(viewer)
	}
	var tokens map[string]string
	if json.Unmarshal([]byte(pseenv.Get("PSE_DIGARR_TOKENS")), &tokens) != nil {
		return DigarrConfig{}
	}
	return DigarrConfig{pseenv.Get("PSE_DIGARR_URL"), tokens[strconv.FormatInt(viewer, 10)]}
}
func digarrURL(c DigarrConfig) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(c.URL))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(c.Token) == "" {
		return nil, errors.New("Digarr non configuré pour ce spectateur")
	}
	return u, nil
}

type digarrRecommendation struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Artist struct {
		Name string `json:"name"`
	} `json:"artist"`
	Album  string `json:"recommendedReleaseGroupTitle"`
	Reason string `json:"aiReasoning"`
}

func (a *PlaylistAPI) digarrGet(ctx context.Context, viewer int64, path string, query url.Values, out any) error {
	cfg := a.digarrConfig(viewer)
	u, err := digarrURL(cfg)
	if err != nil {
		return err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/v1/" + path
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errors.New("requête Digarr invalide")
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("Digarr indisponible")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("Digarr a refusé la requête ; vérifiez le jeton et le service")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, out) != nil {
		return errors.New("réponse Digarr invalide ou trop grande")
	}
	return nil
}
func (a *PlaylistAPI) digarrRecommendation(ctx context.Context, viewer, id int64) (digarrRecommendation, error) {
	var rec digarrRecommendation
	err := a.digarrGet(ctx, viewer, "recommendations/"+strconv.FormatInt(id, 10), nil, &rec)
	if err != nil {
		return rec, err
	}
	if rec.ID != id || strings.TrimSpace(rec.Artist.Name) == "" || (rec.Kind != "album" && rec.Kind != "artist") || (rec.Kind == "album" && strings.TrimSpace(rec.Album) == "") {
		return rec, errors.New("recommandation Digarr incomplète")
	}
	return rec, nil
}
func (a *PlaylistAPI) digarrAlbums(ctx context.Context, rec digarrRecommendation) ([]Album, error) {
	if a.Sync.Deezer == nil {
		return nil, errors.New("catalogue musical indisponible")
	}
	query := rec.Artist.Name
	if rec.Kind == "album" {
		query += " " + rec.Album
	}
	albums, err := a.Sync.Deezer.SearchAlbums(ctx, query, 50, 0)
	if err != nil {
		return nil, errors.New("recherche dans le catalogue indisponible")
	}
	out := []Album{}
	for _, al := range albums {
		if al.ID > 0 && flowName(al.Artist.Name) == flowName(rec.Artist.Name) && (rec.Kind == "artist" || flowName(al.Title) == flowName(rec.Album)) {
			out = append(out, al)
		}
	}
	return out, nil
}
func (a *PlaylistAPI) digarrRoutes(mux *http.ServeMux) {
	fail := func(w http.ResponseWriter, status int, err error) {
		apiJSON(w, status, map[string]string{"error": err.Error()})
	}
	viewer := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
		id, err := strconv.ParseInt(r.URL.Query().Get("viewer"), 10, 64)
		if err != nil || id <= 0 || !a.Known(r.Context(), id) {
			fail(w, 400, errors.New("spectateur inconnu"))
			return 0, false
		}
		return id, true
	}
	mux.HandleFunc("GET /digarr", func(w http.ResponseWriter, r *http.Request) {
		v, ok := viewer(w, r)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if _, err := digarrURL(a.digarrConfig(v)); err != nil {
			apiJSON(w, 200, map[string]any{"configured": false, "items": []digarrRecommendation{}, "total": 0})
			return
		}
		offset := 0
		if raw := r.URL.Query().Get("offset"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 || n > 10000 {
				fail(w, 400, errors.New("page invalide"))
				return
			}
			offset = n
		}
		var page struct {
			Items []digarrRecommendation `json:"items"`
			Total int                    `json:"total"`
		}
		err := a.digarrGet(r.Context(), v, "recommendations", url.Values{"status": {"pending"}, "limit": {"20"}, "offset": {strconv.Itoa(offset)}, "sort": {"score_desc"}}, &page)
		if err != nil {
			fail(w, 502, err)
			return
		}
		if page.Items == nil || page.Total < 0 || len(page.Items) > 20 {
			fail(w, 502, errors.New("réponse Digarr incomplète"))
			return
		}
		for _, item := range page.Items {
			if item.ID <= 0 || strings.TrimSpace(item.Artist.Name) == "" || (item.Kind != "artist" && item.Kind != "album") || (item.Kind == "album" && strings.TrimSpace(item.Album) == "") {
				fail(w, 502, errors.New("recommandation Digarr incomplète"))
				return
			}
		}
		apiJSON(w, 200, map[string]any{"configured": true, "items": page.Items, "total": page.Total})
	})
	mux.HandleFunc("GET /digarr/albums", func(w http.ResponseWriter, r *http.Request) {
		v, ok := viewer(w, r)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if err != nil || id <= 0 {
			fail(w, 400, errors.New("recommandation invalide"))
			return
		}
		rec, err := a.digarrRecommendation(r.Context(), v, id)
		if err != nil {
			fail(w, 502, err)
			return
		}
		albums, err := a.digarrAlbums(r.Context(), rec)
		if err != nil {
			fail(w, 502, err)
			return
		}
		apiJSON(w, 200, albums)
	})
	mux.HandleFunc("POST /digarr/import", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Viewer, Recommendation, Album int64 }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.Viewer <= 0 || body.Recommendation <= 0 || body.Album <= 0 || !a.Known(r.Context(), body.Viewer) {
			fail(w, 400, errors.New("paramètres invalides"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		rec, err := a.digarrRecommendation(ctx, body.Viewer, body.Recommendation)
		if err != nil {
			fail(w, 502, err)
			return
		}
		albums, err := a.digarrAlbums(ctx, rec)
		if err != nil {
			fail(w, 502, err)
			return
		}
		var match *Album
		for _, al := range albums {
			if al.ID == body.Album {
				match = &al
				break
			}
		}
		if match == nil {
			fail(w, 400, errors.New("album sans correspondance exacte"))
			return
		}
		al, tracks, err := a.Sync.Deezer.Album(ctx, body.Album)
		if err != nil || al.ID != match.ID || flowName(al.Title) != flowName(match.Title) || flowName(al.Artist.Name) != flowName(rec.Artist.Name) || len(tracks) == 0 || (al.Tracks > 0 && al.Tracks != len(tracks)) {
			fail(w, 502, errors.New("album incomplet ou incompatible ; aucune playlist créée"))
			return
		}
		for _, tr := range tracks {
			if tr.ID <= 0 || strings.TrimSpace(tr.Title) == "" {
				fail(w, 502, errors.New("album incomplet ; aucune playlist créée"))
				return
			}
		}
		pl, err := a.Sync.Store.CreatePersonal(ctx, body.Viewer, "Digarr · "+al.Artist.Name+" — "+al.Title, tracks)
		if err != nil {
			fail(w, 500, errors.New("création de playlist impossible"))
			return
		}
		apiJSON(w, 201, map[string]any{"playlistId": pl.ID, "tracks": len(tracks)})
	})
}
