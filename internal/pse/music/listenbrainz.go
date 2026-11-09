package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ListenBrainz (MetaBrainz, libre et gratuit) : playlists publiques d'un
// utilisateur et celles que ListenBrainz génère pour lui (Daily Jams,
// Weekly Jams, Weekly Exploration…). Lecture seule, sans clé API. Les
// titres sont retrouvés dans le catalogue Deezer comme ceux de Last.fm.

// ServiceListenBrainz : compte ListenBrainz lié.
const ServiceListenBrainz = "listenbrainz"

// ListenBrainzAPI est l'API publique de ListenBrainz.
const ListenBrainzAPI = "https://api.listenbrainz.org"

// lbPace respecte la limite publique (30 requêtes par 10 s).
const lbPace = 400 * time.Millisecond

// lbMaxOwn borne les playlists personnelles importées, lbMaxSeries les
// séries générées (Daily Jams, Weekly Exploration…).
const (
	lbMaxOwn    = 10
	lbMaxSeries = 6
)

var (
	lbUserRe = regexp.MustCompile(`(?i)listenbrainz\.org/user/([^/?#\s]+)`)
	lbNameRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)
	lbMBIDRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
)

// ParseListenBrainzAccount normalise un nom d'utilisateur ou un lien.
func ParseListenBrainzAccount(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if m := lbUserRe.FindStringSubmatch(raw); m != nil {
		raw, _ = url.PathUnescape(m[1])
	}
	if !lbNameRe.MatchString(raw) {
		return "", fmt.Errorf("%q n'est pas un nom d'utilisateur ListenBrainz", raw)
	}
	return raw, nil
}

type lbList struct {
	Playlist struct {
		Title      string `json:"title"`
		Identifier string `json:"identifier"`
		Date       string `json:"date"`
		Track      []struct {
			Title   string `json:"title"`
			Creator string `json:"creator"`
		} `json:"track"`
	} `json:"playlist"`
}

func (s *PlaylistSync) lb(ctx context.Context, path string, q url.Values, into any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(lbPace):
	}
	base := s.ListenBrainz
	if base == "" {
		base = ListenBrainzAPI
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Ether-Remix (https://github.com/Aerya/Ether-Remix)")
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return errors.New("ListenBrainz injoignable")
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return errors.New("utilisateur ListenBrainz introuvable")
	case http.StatusTooManyRequests:
		return errors.New("ListenBrainz limite les requêtes, nouvel essai plus tard")
	default:
		return fmt.Errorf("ListenBrainz a répondu %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(into)
}

// listenbrainzLists importe la dernière playlist de chaque série générée
// pour l'utilisateur, puis ses propres playlists publiques.
func (s *PlaylistSync) listenbrainzLists(ctx context.Context, user string) ([]Playlist, error) {
	var created, own struct {
		Playlists []lbList `json:"playlists"`
	}
	if err := s.lb(ctx, "/1/user/"+url.PathEscape(user)+"/playlists/createdfor", url.Values{"count": {"25"}}, &created); err != nil {
		return nil, err
	}
	if err := s.lb(ctx, "/1/user/"+url.PathEscape(user)+"/playlists", url.Values{"count": {fmt.Sprint(lbMaxOwn)}}, &own); err != nil {
		return nil, err
	}
	type pick struct{ ref, name, mbid string }
	var picks []pick
	seen := map[string]bool{}
	for _, l := range created.Playlists { // du plus récent au plus ancien
		series := lbSeries(l.Playlist.Title)
		mbid := lbMBIDRe.FindString(l.Playlist.Identifier)
		if series == "" || mbid == "" || seen[series] || len(seen) >= lbMaxSeries {
			continue
		}
		seen[series] = true
		picks = append(picks, pick{"for-" + foldWords(series), series + " · ListenBrainz", mbid})
	}
	for _, l := range own.Playlists {
		if mbid := lbMBIDRe.FindString(l.Playlist.Identifier); mbid != "" {
			picks = append(picks, pick{mbid, strings.TrimSpace(l.Playlist.Title) + " · ListenBrainz", mbid})
		}
	}
	var out []Playlist
	var errs []error
	for _, p := range picks {
		var full lbList
		if err := s.lb(ctx, "/1/playlist/"+p.mbid, nil, &full); err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			errs = append(errs, err)
			continue
		}
		pl := Playlist{Ref: p.ref, Name: p.name}
		for _, t := range full.Playlist.Track {
			if tr, ok := s.findTrack(ctx, t.Creator, t.Title); ok {
				pl.Tracks = append(pl.Tracks, tr)
				if len(pl.Tracks) >= s.maxTracks() {
					break
				}
			}
		}
		if len(pl.Tracks) > 0 {
			out = append(out, pl)
		}
	}
	return out, errors.Join(errs...)
}

// lbSeries retire « for <user>, <date> » d'un titre généré : « Daily Jams
// for rob, 2026-11-08 Sun » donne « Daily Jams ».
func lbSeries(title string) string {
	if i := strings.Index(title, " for "); i > 0 {
		return strings.TrimSpace(title[:i])
	}
	return ""
}
