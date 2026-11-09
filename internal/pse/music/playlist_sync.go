package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Synchronisation des playlists : playlists publiques d'un profil Deezer
// (dont « Coups de cœur ») ou playlists Deezer isolées, et listes Last.fm
// (titres aimés, tops du mois et de tous les temps) retrouvées dans le
// catalogue Deezer.

// LastFMAPI est l'API publique de Last.fm.
const LastFMAPI = "https://ws.audioscrobbler.com/2.0/"

var (
	dzProfileRe  = regexp.MustCompile(`(?i)deezer\.com/(?:[a-z]{2}(?:-[a-z]{2})?/)?(?:profile|user)/(\d+)`)
	dzPlaylistRe = regexp.MustCompile(`(?i)deezer\.com/(?:[a-z]{2}(?:-[a-z]{2})?/)?playlist/(\d+)`)
	lastfmUserRe = regexp.MustCompile(`(?i)last\.fm/(?:[a-z]{2}/)?user/([^/?#\s]+)`)
	lastfmNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,14}$`)
)

// ParseDeezerAccount normalise ce que colle un spectateur : identifiant ou
// lien de profil, liens de playlists, séparés par des espaces ou des
// virgules. Renvoie « user:123 playlist:456 … ».
func ParseDeezerAccount(raw string) (string, error) {
	var out []string
	seen := map[string]bool{}
	for _, f := range strings.FieldsFunc(raw, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' || r == ';' }) {
		tok := ""
		switch {
		case isDigits(f):
			tok = "user:" + f
		case strings.HasPrefix(f, "user:") && isDigits(f[5:]) && len(f) > 5,
			strings.HasPrefix(f, "playlist:") && isDigits(f[9:]) && len(f) > 9:
			tok = f
		case dzProfileRe.MatchString(f):
			tok = "user:" + dzProfileRe.FindStringSubmatch(f)[1]
		case dzPlaylistRe.MatchString(f):
			tok = "playlist:" + dzPlaylistRe.FindStringSubmatch(f)[1]
		case strings.Contains(f, "link.deezer.com"), strings.Contains(f, "deezer.page.link"):
			return "", errors.New("lien court Deezer illisible : ouvrez-le puis collez l'adresse complète (deezer.com/…/profile/… ou /playlist/…)")
		default:
			return "", fmt.Errorf("%q n'est ni un identifiant ni un lien de profil ou de playlist Deezer", f)
		}
		if !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	return strings.Join(out, " "), nil
}

// isDeezerShortLink reconnaît les liens de partage de l'application Deezer.
func isDeezerShortLink(f string) bool {
	u, err := url.Parse(f)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "link.deezer.com" || h == "deezer.page.link"
}

// ExpandDeezerLinks remplace les liens de partage Deezer (link.deezer.com,
// deezer.page.link) par l'adresse du profil ou de la playlist vers laquelle
// ils redirigent. Seuls ces deux hôtes sont contactés ; un lien qui ne mène
// à rien de reconnaissable est laissé tel quel (l'analyse le signalera).
func (s *PlaylistSync) ExpandDeezerLinks(ctx context.Context, raw string) string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ' ' || r == ',' || r == '\n' || r == ';' })
	base := s.HTTP
	if base == nil {
		base = http.DefaultClient
	}
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout == 0 {
		client.Timeout = 15 * time.Second
	}
	for i, f := range fields {
		if !isDeezerShortLink(f) {
			continue
		}
		next := f
		for hop := 0; hop < 4 && isDeezerShortLink(next); hop++ {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
			if err != nil {
				break
			}
			resp, err := client.Do(req)
			if err != nil {
				break
			}
			loc := resp.Header.Get("Location")
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
			resp.Body.Close()
			if loc == "" {
				break
			}
			decoded, err := url.QueryUnescape(loc)
			if err != nil {
				decoded = loc
			}
			if m := dzPlaylistRe.FindStringSubmatch(decoded); m != nil {
				fields[i] = "https://www.deezer.com/playlist/" + m[1]
				break
			}
			if m := dzProfileRe.FindStringSubmatch(decoded); m != nil {
				fields[i] = "https://www.deezer.com/profile/" + m[1]
				break
			}
			if u, err := url.Parse(loc); err == nil && u.IsAbs() {
				next = loc
			} else {
				break
			}
		}
	}
	return strings.Join(fields, " ")
}

// ParseLastFMAccount normalise un nom d'utilisateur ou un lien de profil.
func ParseLastFMAccount(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if m := lastfmUserRe.FindStringSubmatch(raw); m != nil {
		raw, _ = url.PathUnescape(m[1])
	}
	if !lastfmNameRe.MatchString(raw) {
		return "", fmt.Errorf("%q n'est pas un nom d'utilisateur Last.fm", raw)
	}
	return raw, nil
}

// PlaylistSync importe les playlists des comptes liés.
type PlaylistSync struct {
	Store  *Playlists
	Deezer *DeezerSource
	// LastFMKey renvoie la clé API Last.fm (vide : Last.fm désactivé).
	LastFMKey func() string
	LastFM    string // API Last.fm (LastFMAPI par défaut)
	// ListenBrainz : API ListenBrainz (ListenBrainzAPI par défaut).
	ListenBrainz string
	HTTP         *http.Client
	Log          *slog.Logger
	MaxTracks    int           // par playlist (2000 par défaut)
	Pace         time.Duration // entre deux appels Deezer (150 ms par défaut)

	run  sync.Mutex // une synchronisation à la fois
	mu   sync.Mutex
	last time.Time
	busy map[int64]int // spectateurs en cours de synchronisation
	// system : dernière génération des playlists système, par spectateur.
	system map[int64]systemRun
}

// Busy indique si les playlists d'un spectateur sont en cours d'import.
func (s *PlaylistSync) Busy(viewer int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy[viewer] > 0
}

func (s *PlaylistSync) mark(viewer int64, d int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy == nil {
		s.busy = map[int64]int{}
	}
	s.busy[viewer] += d
	if s.busy[viewer] <= 0 {
		delete(s.busy, viewer)
	}
}

// SyncDue synchronise les comptes dont le dernier import date de plus de
// every.
func (s *PlaylistSync) SyncDue(ctx context.Context, every time.Duration) {
	accounts, err := s.Store.Accounts(ctx, 0)
	if err != nil {
		s.logger().Warn("playlists : comptes illisibles", "err", err)
		return
	}
	now := s.Store.now().Unix()
	for _, a := range accounts {
		if ctx.Err() != nil {
			return
		}
		if now-a.SyncedAt < int64(every/time.Second) {
			continue
		}
		s.mark(a.Viewer, 1)
		s.run.Lock()
		_ = s.syncLocked(ctx, a)
		s.run.Unlock()
		s.mark(a.Viewer, -1)
	}
}

func (s *PlaylistSync) maxTracks() int {
	if s.MaxTracks > 0 {
		return s.MaxTracks
	}
	return 2000
}

func (s *PlaylistSync) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// wait espace les appels à Deezer (50 requêtes par 5 s au plus).
func (s *PlaylistSync) wait(ctx context.Context) error {
	pace := s.Pace
	if pace == 0 {
		pace = 150 * time.Millisecond
	}
	s.mu.Lock()
	next := s.last.Add(pace)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	s.last = next
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Until(next)):
		return nil
	}
}

func (s *PlaylistSync) dz(ctx context.Context, path string, q url.Values, into any) error {
	if err := s.wait(ctx); err != nil {
		return err
	}
	return s.Deezer.get(ctx, path, q, into)
}

// SyncViewer synchronise les comptes d'un spectateur.
func (s *PlaylistSync) SyncViewer(ctx context.Context, viewer int64) error {
	accounts, err := s.Store.Accounts(ctx, viewer)
	if err != nil {
		return err
	}
	s.mark(viewer, 1)
	defer s.mark(viewer, -1)
	s.run.Lock()
	defer s.run.Unlock()
	var errs []error
	for _, a := range accounts {
		if err := s.syncLocked(ctx, a); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *PlaylistSync) syncLocked(ctx context.Context, a Account) error {
	var lists []Playlist
	var err error
	switch a.Service {
	case ServiceDeezer:
		lists, err = s.deezerLists(ctx, a.Account)
	case ServiceLastFM:
		lists, err = s.lastfmLists(ctx, a.Account)
	case ServiceListenBrainz:
		lists, err = s.listenbrainzLists(ctx, a.Account)
	default:
		err = fmt.Errorf("service inconnu %q", a.Service)
	}
	if err == nil || len(lists) > 0 {
		if rerr := s.Store.replace(ctx, a.Viewer, a.Service, lists); rerr != nil {
			err = errors.Join(err, rerr)
		}
	}
	s.Store.report(context.WithoutCancel(ctx), a, err)
	if err != nil {
		s.logger().Info("playlists : synchronisation incomplète", "service", a.Service, "viewer", a.Viewer, "err", err)
	}
	return err
}

// --- Deezer ---

type dzPlaylist struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	NbTracks    int    `json:"nb_tracks"`
	IsLovedList bool   `json:"is_loved_track"`
}

func (s *PlaylistSync) deezerLists(ctx context.Context, account string) ([]Playlist, error) {
	var lists []Playlist
	var errs []error
	for _, tok := range strings.Fields(account) {
		kind, id, _ := strings.Cut(tok, ":")
		switch kind {
		case "user":
			pls, err := s.deezerUserPlaylists(ctx, id)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			for _, p := range pls {
				pl, err := s.deezerPlaylist(ctx, p)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				lists = append(lists, pl)
			}
		case "playlist":
			var p dzPlaylist
			if err := s.dz(ctx, "/playlist/"+id, nil, &p); err != nil {
				errs = append(errs, fmt.Errorf("playlist Deezer %s : %w", id, deezerErr(err)))
				continue
			}
			pl, err := s.deezerPlaylist(ctx, p)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			lists = append(lists, pl)
		}
	}
	return lists, errors.Join(errs...)
}

func deezerErr(err error) error {
	if errors.Is(err, ErrNotFound) {
		return errors.New("introuvable ou privée")
	}
	return err
}

func (s *PlaylistSync) deezerUserPlaylists(ctx context.Context, user string) ([]dzPlaylist, error) {
	var out []dzPlaylist
	for index := 0; index < 1000; {
		var r struct {
			Data  []dzPlaylist `json:"data"`
			Total int          `json:"total"`
		}
		q := url.Values{"limit": {"100"}, "index": {strconv.Itoa(index)}}
		if err := s.dz(ctx, "/user/"+user+"/playlists", q, &r); err != nil {
			return out, fmt.Errorf("profil Deezer %s : %w", user, deezerErr(err))
		}
		out = append(out, r.Data...)
		index += len(r.Data)
		if len(r.Data) == 0 || index >= r.Total {
			break
		}
	}
	return out, nil
}

func (s *PlaylistSync) deezerPlaylist(ctx context.Context, p dzPlaylist) (Playlist, error) {
	pl := Playlist{Ref: strconv.FormatInt(p.ID, 10), Name: strings.TrimSpace(p.Title)}
	if p.IsLovedList || pl.Name == "Loved Tracks" {
		pl.Name = "Coups de cœur"
	}
	if pl.Name == "" {
		pl.Name = "Playlist " + pl.Ref
	}
	pl.Name += " · Deezer"
	max := s.maxTracks()
	for index := 0; index < max; {
		var r struct {
			Data  []dzTrack `json:"data"`
			Total int       `json:"total"`
		}
		q := url.Values{"limit": {"100"}, "index": {strconv.Itoa(index)}}
		if err := s.dz(ctx, "/playlist/"+pl.Ref+"/tracks", q, &r); err != nil {
			return pl, fmt.Errorf("playlist Deezer « %s » : %w", p.Title, deezerErr(err))
		}
		pl.Tracks = append(pl.Tracks, readable(r.Data)...)
		index += len(r.Data)
		if len(r.Data) == 0 || index >= r.Total {
			break
		}
	}
	if len(pl.Tracks) > max {
		pl.Tracks = pl.Tracks[:max]
	}
	return pl, nil
}

// --- Last.fm ---

type lfmTrack struct {
	Name   string `json:"name"`
	Artist struct {
		Name string `json:"name"`
		Text string `json:"#text"`
	} `json:"artist"`
}

func (t lfmTrack) artist() string {
	if t.Artist.Name != "" {
		return t.Artist.Name
	}
	return t.Artist.Text
}

var lastfmLists = []struct {
	ref, name, method, period string
}{
	{"loved", "Titres aimés · Last.fm", "user.getlovedtracks", ""},
	{"top-1month", "Top du mois · Last.fm", "user.gettoptracks", "1month"},
	{"top-overall", "Top de tous les temps · Last.fm", "user.gettoptracks", "overall"},
}

func (s *PlaylistSync) lastfm(ctx context.Context, q url.Values, into any) error {
	key := ""
	if s.LastFMKey != nil {
		key = strings.TrimSpace(s.LastFMKey())
	}
	if key == "" {
		return errors.New("clé API Last.fm manquante (Ether-Remix › Musique)")
	}
	q.Set("api_key", key)
	q.Set("format", "json")
	base := s.LastFM
	if base == "" {
		base = LastFMAPI
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	hc := s.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := hc.Do(req)
	if err != nil {
		return errors.New("Last.fm injoignable")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	var e struct {
		Error   int    `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != 0 {
		switch e.Error {
		case 6:
			return errors.New("utilisateur Last.fm introuvable")
		case 10, 26:
			return errors.New("clé API Last.fm refusée")
		case 17:
			return errors.New("profil Last.fm privé")
		}
		return fmt.Errorf("Last.fm : %s", e.Message)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("Last.fm a répondu %d", res.StatusCode)
	}
	return json.Unmarshal(body, into)
}

func (s *PlaylistSync) lastfmLists(ctx context.Context, user string) ([]Playlist, error) {
	var lists []Playlist
	var errs []error
	for _, l := range lastfmLists {
		raw, err := s.lastfmTracks(ctx, user, l.method, l.period)
		if err != nil {
			errs = append(errs, err)
			if len(raw) == 0 {
				continue
			}
		}
		pl := Playlist{Ref: l.ref, Name: l.name}
		seen := map[int64]bool{}
		for _, t := range raw {
			if ctx.Err() != nil {
				return lists, ctx.Err()
			}
			m, ok := s.findTrack(ctx, t.artist(), t.Name)
			if ok && !seen[m.ID] {
				seen[m.ID] = true
				pl.Tracks = append(pl.Tracks, m)
			}
		}
		if len(pl.Tracks) > 0 {
			lists = append(lists, pl)
		}
	}
	return lists, errors.Join(errs...)
}

func (s *PlaylistSync) lastfmTracks(ctx context.Context, user, method, period string) ([]lfmTrack, error) {
	max := s.maxTracks()
	limit := 200
	if method == "user.gettoptracks" {
		max, limit = min(max, 100), 100
	}
	var out []lfmTrack
	for page := 1; len(out) < max; page++ {
		q := url.Values{"method": {method}, "user": {user}, "limit": {strconv.Itoa(limit)}, "page": {strconv.Itoa(page)}}
		if period != "" {
			q.Set("period", period)
		}
		var r struct {
			Loved struct {
				Track []lfmTrack `json:"track"`
				Attr  struct {
					TotalPages string `json:"totalPages"`
				} `json:"@attr"`
			} `json:"lovedtracks"`
			Top struct {
				Track []lfmTrack `json:"track"`
				Attr  struct {
					TotalPages string `json:"totalPages"`
				} `json:"@attr"`
			} `json:"toptracks"`
		}
		if err := s.lastfm(ctx, q, &r); err != nil {
			return out, err
		}
		tracks, pages := r.Loved.Track, r.Loved.Attr.TotalPages
		if method == "user.gettoptracks" {
			tracks, pages = r.Top.Track, r.Top.Attr.TotalPages
		}
		out = append(out, tracks...)
		total, _ := strconv.Atoi(pages)
		if len(tracks) == 0 || page >= total {
			break
		}
	}
	if len(out) > max {
		out = out[:max]
	}
	return out, nil
}

// findTrack retrouve un titre Last.fm dans le catalogue Deezer ; le
// résultat (ou l'absence) est gardé en base.
func (s *PlaylistSync) findTrack(ctx context.Context, artist, title string) (Track, bool) {
	artist, title = strings.TrimSpace(artist), strings.TrimSpace(title)
	if artist == "" || title == "" {
		return Track{}, false
	}
	key := foldWords(artist) + "\x00" + foldWords(title)
	if t, ok, known := s.Store.match(ctx, key); known {
		return t, ok
	}
	clean := func(v string) string { return strings.NewReplacer(`"`, " ", `\`, " ").Replace(v) }
	var best *Track
	for _, q := range []string{`artist:"` + clean(artist) + `" track:"` + clean(title) + `"`, clean(artist) + " " + clean(title)} {
		var r struct{ Data []dzTrack }
		v := url.Values{"q": {q}, "limit": {"10"}}
		if err := s.dz(ctx, "/search/track", v, &r); err != nil {
			if ctx.Err() != nil || !errors.Is(err, ErrNotFound) {
				return Track{}, false // erreur passagère : rien n'est gardé
			}
			continue
		}
		if t, ok := bestTrackMatch(readable(r.Data), artist, title); ok {
			best = &t
			break
		}
	}
	s.Store.saveMatch(ctx, key, best)
	if best == nil {
		return Track{}, false
	}
	return *best, true
}

// bestTrackMatch garde le premier résultat dont l'artiste et le titre
// correspondent (mots du titre Last.fm présents, artiste identique ou
// contenu).
func bestTrackMatch(cands []Track, artist, title string) (Track, bool) {
	fa, ft := foldWords(artist), foldWords(title)
	base := foldWords(stripTrackExtras(title))
	for pass := 0; pass < 2; pass++ {
		for _, c := range cands {
			ca, ct := foldWords(c.Artist.Name), foldWords(c.Title)
			if ca == "" || !(ca == fa || strings.Contains(ca, fa) || strings.Contains(fa, ca)) {
				continue
			}
			if !sameVersion(c.Title, title) {
				continue // une version live ou un remix n'est pas l'original
			}
			if pass == 0 && ct == ft {
				return c, true
			}
			if pass == 1 && (strings.HasPrefix(ct, base) || strings.HasPrefix(foldWords(stripTrackExtras(c.Title)), base)) && base != "" {
				return c, true
			}
		}
	}
	return Track{}, false
}

var trackExtrasRe = regexp.MustCompile(`\s*[\(\[][^\)\]]*[\)\]]|\s+-\s+.*$`)

// stripTrackExtras retire « (Remastered 2011) », « - Radio Edit »…
func stripTrackExtras(t string) string {
	return strings.TrimSpace(trackExtrasRe.ReplaceAllString(t, ""))
}
