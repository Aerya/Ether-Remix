package music

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Authenticator vérifie un couple identifiant / mot de passe d'Ether et
// renvoie l'utilisateur correspondant.
type Authenticator func(ctx context.Context, username, password string) (viewerID int64, err error)

// Options configure le serveur Subsonic.
type Options struct {
	Source    Source // catalogue et flux (Deezer + Eclipse, ou HiFi API)
	Library   *Library
	Auth      Authenticator
	TokenAuth func(context.Context, string, string, string) (int64, error)
	Quality   string       // qualité demandée à la HiFi API (LOSSLESS par défaut)
	Media     *http.Client // client des flux audio et des pochettes
	// Playlists : playlists importées de Deezer et Last.fm (facultatif).
	Playlists *Playlists
	Logger    *slog.Logger
}

// Server sert l'API Subsonic sous /rest/.
type Server struct {
	nowMu       sync.Mutex
	nowSequence uint64
	nowPlaying  map[string]MusicSession
	o           Options
	auth        *authCache
	clock       func() time.Time
	cache       *memo // albums, artistes et titres (clients Jellyfin)
	charts      *memo // classements
	// discover : styles, classements par style, artistes proches, radios.
	discover *memo
	// recherches des frontaux Jellyfin et Plex
	searches *memo
	// fresh : « Nouveautés de vos artistes », par spectateur.
	fresh freshCache
}

// NewServer prépare le serveur Subsonic.
func NewServer(o Options) *Server {
	if o.Quality == "" {
		o.Quality = "LOSSLESS"
	}
	if o.Media == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.ResponseHeaderTimeout = 20 * time.Second
		o.Media = &http.Client{Transport: t, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("music: trop de redirections")
			}
			return checkAudioHost(req.Context(), req.URL.String())
		}}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{o: o, auth: newAuthCache(), clock: time.Now,
		cache: newMemo(10*time.Minute, 2000), charts: newMemo(time.Hour, 16), discover: newMemo(discoverTTL, 1000), searches: newMemo(2*time.Minute, 500)}
}

type call struct {
	w      http.ResponseWriter
	r      *http.Request
	viewer int64
	user   string
}

func (c *call) ok(e *el)                  { write(c.w, c.r, e) }
func (c *call) fail(code int, msg string) { fail(c.w, c.r, code, msg) }
func (c *call) param(k string) string     { return c.r.Form.Get(k) }
func (c *call) params(k string) []string  { return c.r.Form[k] }
func (c *call) num(k string, def, max int) int {
	n, err := strconv.Atoi(c.param(k))
	if err != nil || n < 0 {
		return def
	}
	if max > 0 && n > max {
		return max
	}
	return n
}

// ServeHTTP implémente http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	method := strings.TrimPrefix(r.URL.Path, "/rest/")
	method = strings.TrimSuffix(method, ".view")
	c := &call{w: w, r: r}
	if !s.authenticate(c) {
		return
	}
	h, ok := s.routes()[method]
	if !ok {
		c.fail(errGeneric, "Méthode non prise en charge par Ether-Remix: "+method)
		return
	}
	h(c)
}

func (s *Server) routes() map[string]func(*call) {
	return map[string]func(*call){
		"ping":                      func(c *call) { c.ok(nil) },
		"getLicense":                s.license,
		"getOpenSubsonicExtensions": func(c *call) { c.ok(node("openSubsonicExtensions").add()) },
		"getMusicFolders":           s.musicFolders,
		"getUser":                   s.user,
		"getScanStatus":             s.scanStatus,
		"startScan":                 s.scanStatus,
		"getArtists":                s.artists("artists"),
		"getIndexes":                s.artists("indexes"),
		"getArtist":                 s.artist,
		"getAlbum":                  s.album,
		"getSong":                   s.song,
		"getMusicDirectory":         s.directory,
		"search2":                   s.search("searchResult2", false),
		"search3":                   s.search("searchResult3", true),
		"getAlbumList":              s.albumList("albumList", false),
		"getAlbumList2":             s.albumList("albumList2", true),
		"getRandomSongs":            s.randomSongs,
		"getStarred":                s.starred("starred", false),
		"getStarred2":               s.starred("starred2", true),
		"star":                      s.star(true),
		"unstar":                    s.star(false),
		"scrobble":                  s.scrobble,
		"stream":                    s.stream,
		"download":                  s.stream,
		"getCoverArt":               s.cover,
		"getGenres":                 s.genres,
		"getSongsByGenre":           s.songsByGenre,
		"getPlaylists":              s.getPlaylists,
		"getPlaylist":               s.getPlaylist,
		"createPlaylist":            s.createPlaylist,
		"updatePlaylist":            s.updatePlaylist,
		"deletePlaylist":            s.deletePlaylist,
		"getArtistInfo":             s.artistInfo("artistInfo"),
		"getArtistInfo2":            s.artistInfo("artistInfo2"),
		"getAlbumInfo":              func(c *call) { c.ok(node("albumInfo")) },
		"getAlbumInfo2":             func(c *call) { c.ok(node("albumInfo")) },
		"getSimilarSongs":           s.similarSongs("similarSongs"),
		"getSimilarSongs2":          s.similarSongs("similarSongs2"),
		"getTopSongs":               s.topSongs,
		"getLyrics":                 func(c *call) { c.ok(node("lyrics")) },
		"getNowPlaying":             func(c *call) { c.ok(node("nowPlaying")) },
	}
}

// --- authentification ---

func (s *Server) authenticate(c *call) bool {
	user, pass := c.param("u"), c.param("p")
	if c.param("t") != "" || c.param("s") != "" {
		if s.o.TokenAuth == nil {
			c.fail(errTokenNoSupp, "Authentification par jeton indisponible")
			return false
		}
		if user == "" || c.param("t") == "" || c.param("s") == "" {
			c.fail(errMissingParam, "Paramètres u, t et s requis")
			return false
		}
		viewer, err := s.auth.checkUncached(c.r.Context(), func(ctx context.Context, u, _ string) (int64, error) {
			return s.o.TokenAuth(ctx, u, c.param("t"), c.param("s"))
		}, user, "")
		if err != nil {
			c.fail(errWrongAuth, "Accès musical incorrect ou révoqué ; utilisez le mot de passe musical généré dans Remix")
			return false
		}
		c.viewer, c.user = viewer, user
		c.r = c.r.WithContext(withMusicViewer(c.r.Context(), viewer))
		return true
	}
	if c.param("apiKey") != "" {
		c.fail(42, "Clé d'API non prise en charge : utilisez identifiant et mot de passe")
		return false
	}
	if user == "" || pass == "" {
		c.fail(errMissingParam, "Paramètres u et p requis")
		return false
	}
	if strings.HasPrefix(pass, "enc:") {
		b, err := hex.DecodeString(pass[4:])
		if err != nil {
			c.fail(errWrongAuth, "Identifiant ou mot de passe incorrect")
			return false
		}
		pass = string(b)
	}
	viewer, err := s.auth.check(c.r.Context(), s.o.Auth, user, pass)
	if err != nil {
		if errors.Is(err, errLocked) {
			c.fail(errWrongAuth, "Trop d'échecs : réessayez plus tard")
		} else {
			c.fail(errWrongAuth, "Identifiant ou mot de passe incorrect")
		}
		return false
	}
	c.viewer, c.user = viewer, user
	c.r = c.r.WithContext(withMusicViewer(c.r.Context(), viewer))
	return true
}

var errLocked = errors.New("music: compte temporairement bloqué")

// authCache évite de recalculer le hachage du mot de passe à chaque requête
// (les clients Subsonic s'authentifient à chaque appel) et bloque un compte
// après dix échecs en quinze minutes.
type authCache struct {
	mu    sync.Mutex
	ok    map[[32]byte]authEntry
	fails map[string][]time.Time
	now   func() time.Time
}

type authEntry struct {
	viewer int64
	until  time.Time
}

func newAuthCache() *authCache {
	return &authCache{ok: map[[32]byte]authEntry{}, fails: map[string][]time.Time{}, now: time.Now}
}

func (a *authCache) check(ctx context.Context, fn Authenticator, user, pass string) (int64, error) {
	return a.checkMode(ctx, fn, user, pass, true)
}

func (a *authCache) checkUncached(ctx context.Context, fn Authenticator, user, pass string) (int64, error) {
	return a.checkMode(ctx, fn, user, pass, false)
}

func (a *authCache) checkMode(ctx context.Context, fn Authenticator, user, pass string, cache bool) (int64, error) {
	key := sha256.Sum256([]byte(user + "\x00" + pass))
	now := a.now()
	a.mu.Lock()
	if e, ok := a.ok[key]; cache && ok && now.Before(e.until) {
		a.mu.Unlock()
		return e.viewer, nil
	}
	lower := strings.ToLower(user)
	recent := a.fails[lower][:0]
	for _, t := range a.fails[lower] {
		if now.Sub(t) < 15*time.Minute {
			recent = append(recent, t)
		}
	}
	a.fails[lower] = recent
	locked := len(recent) >= 10
	a.mu.Unlock()
	if locked {
		return 0, errLocked
	}
	if fn == nil {
		return 0, errors.New("music: authentification indisponible")
	}
	viewer, err := fn(ctx, user, pass)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.fails[lower] = append(a.fails[lower], now)
		return 0, err
	}
	delete(a.fails, lower)
	if len(a.ok) > 512 {
		a.ok = map[[32]byte]authEntry{}
	}
	if cache {
		a.ok[key] = authEntry{viewer: viewer, until: now.Add(10 * time.Minute)}
	}
	return viewer, nil
}

// --- identifiants Subsonic ---

func trackID(id int64) string  { return "tr-" + strconv.FormatInt(id, 10) }
func albumID(id int64) string  { return "al-" + strconv.FormatInt(id, 10) }
func artistID(id int64) string { return "ar-" + strconv.FormatInt(id, 10) }

// parseID découpe un identifiant Subsonic de PSE (« al-123 »).
func parseID(s string) (kind string, id int64, ok bool) {
	prefix, rest, found := strings.Cut(s, "-")
	if !found {
		return "", 0, false
	}
	n, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || n <= 0 {
		return "", 0, false
	}
	switch prefix {
	case "tr":
		return KindTrack, n, true
	case "al":
		return KindAlbum, n, true
	case "ar":
		return KindArtist, n, true
	}
	return "", 0, false
}

func coverID(ref string) string {
	if imageRefRe.MatchString(ref) {
		return "c-" + ref
	}
	return ""
}

func pictureID(ref string) string {
	if imageRefRe.MatchString(ref) {
		return "p-" + ref
	}
	return ""
}

// --- conversion vers les éléments Subsonic ---

func (s *Server) contentType() (mime, suffix string) {
	switch s.o.Quality {
	case "HIGH", "LOW":
		return "audio/mp4", "m4a"
	}
	return "audio/flac", "flac"
}

func iso(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}

type userState struct {
	starred int64
	plays   int
	played  int64
}

func (s *Server) state(ctx context.Context, viewer int64, kind string, id int64) userState {
	it, ok, err := s.o.Library.Get(ctx, viewer, kind, id)
	if err != nil || !ok {
		return userState{}
	}
	return userState{starred: it.StarredAt, plays: it.PlayCount, played: it.PlayedAt}
}

func (s *Server) songEl(name string, t Track, st userState) *el {
	mime, suffix := s.trackContentType(t)
	e := node(name, "id", trackID(t.ID), "parent", albumIDOrEmpty(t.Album.ID), "isDir", false,
		"title", t.Title, "album", t.Album.Title, "artist", t.Artist.Name)
	e.opt("track", int64(t.Number)).opt("discNumber", int64(t.Volume)).opt("year", int64(t.Album.Year()))
	e.set("coverArt", coverID(t.Album.Cover))
	e.set("contentType", mime).set("suffix", suffix).set("duration", t.Duration)
	e.set("path", sanitize(t.Artist.Name)+"/"+sanitize(t.Album.Title)+"/"+sanitize(t.Title)+"."+suffix)
	e.set("isVideo", false).set("type", "music").set("mediaType", "song")
	e.set("albumId", albumIDOrEmpty(t.Album.ID)).set("artistId", artistIDOrEmpty(t.Artist.ID))
	e.set("starred", iso(st.starred)).opt("playCount", int64(st.plays)).set("played", iso(st.played))
	if t.Explicit {
		e.set("explicitStatus", "explicit")
	}
	return e
}

func (s *Server) albumEl(name string, a Album, st userState, id3 bool) *el {
	e := node(name, "id", albumID(a.ID))
	if id3 {
		e.set("name", a.Title)
	} else {
		e.set("parent", artistIDOrEmpty(a.Artist.ID)).set("isDir", true).set("title", a.Title).set("album", a.Title)
	}
	e.set("artist", a.Artist.Name).set("artistId", artistIDOrEmpty(a.Artist.ID))
	e.set("coverArt", coverID(a.Cover)).set("songCount", a.Tracks).set("duration", a.Duration)
	e.opt("year", int64(a.Year()))
	e.set("created", createdOf(a))
	e.set("starred", iso(st.starred)).opt("playCount", int64(st.plays)).set("played", iso(st.played))
	return e
}

func (s *Server) artistEl(name string, a Artist, albums int, st userState) *el {
	e := node(name, "id", artistID(a.ID), "name", a.Name)
	e.set("coverArt", pictureID(a.Picture))
	if p := pictureID(a.Picture); p != "" {
		e.set("artistImageUrl", s.o.Source.ImageURL('p', a.Picture, 750))
	}
	if albums >= 0 {
		e.set("albumCount", albums)
	}
	e.set("starred", iso(st.starred))
	return e
}

func createdOf(a Album) string {
	if len(a.ReleaseDate) >= 10 {
		return a.ReleaseDate[:10] + "T00:00:00Z"
	}
	return "1970-01-01T00:00:00Z"
}

func albumIDOrEmpty(id int64) string {
	if id == 0 {
		return ""
	}
	return albumID(id)
}

func artistIDOrEmpty(id int64) string {
	if id == 0 {
		return ""
	}
	return artistID(id)
}

func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, s)
	if s == "" {
		return "_"
	}
	return s
}

// --- méthodes système ---

func (s *Server) license(c *call) {
	c.ok(node("license", "valid", true, "email", "pse@localhost", "licenseExpires", "2099-12-31T00:00:00Z"))
}

func (s *Server) musicFolders(c *call) {
	c.ok(node("musicFolders").item(node("musicFolder", "id", 1, "name", "Musique Ether-Remix")))
}

func (s *Server) user(c *call) {
	name := c.param("username")
	if name == "" {
		name = c.user
	}
	if !strings.EqualFold(name, c.user) {
		c.fail(errNotAuthorized, "Accès refusé")
		return
	}
	u := node("user", "username", c.user, "scrobblingEnabled", true, "adminRole", false, "settingsRole", false,
		"downloadRole", true, "uploadRole", false, "playlistRole", false, "coverArtRole", false,
		"commentRole", false, "podcastRole", false, "streamRole", true, "jukeboxRole", false, "shareRole", false)
	c.ok(u)
}

func (s *Server) scanStatus(c *call) {
	n, _ := s.o.Library.Count(c.r.Context(), c.viewer, KindTrack)
	c.ok(node("scanStatus", "scanning", false, "count", n))
}

// --- bibliothèque ---

func decode[T any](it Item) T {
	var v T
	_ = json.Unmarshal(it.Data, &v)
	return v
}

func (s *Server) libraryArtists(ctx context.Context, viewer int64) ([]Artist, map[int64]int, map[int64]int64) {
	items, _ := s.o.Library.List(ctx, viewer, KindArtist, ListOptions{})
	albums, _ := s.o.Library.List(ctx, viewer, KindAlbum, ListOptions{})
	counts := map[int64]int{}
	for _, it := range albums {
		counts[decode[Album](it).Artist.ID]++
	}
	stars := map[int64]int64{}
	out := make([]Artist, 0, len(items))
	for _, it := range items {
		a := decode[Artist](it)
		if a.ID == 0 {
			continue
		}
		stars[a.ID] = it.StarredAt
		out = append(out, a)
	}
	return out, counts, stars
}

func indexLetter(name string) string {
	name = strings.TrimSpace(name)
	for _, p := range []string{"The ", "Les ", "Le ", "La ", "L'"} {
		if len(name) > len(p) && strings.EqualFold(name[:len(p)], p) {
			name = name[len(p):]
			break
		}
	}
	for _, r := range name {
		r = unicode.ToUpper(r)
		if r >= 'A' && r <= 'Z' {
			return string(r)
		}
		if unicode.IsLetter(r) {
			return string(foldAccent(r))
		}
		return "#"
	}
	return "#"
}

func foldAccent(r rune) rune {
	switch r {
	case 'À', 'Â', 'Ä', 'Á', 'Ã':
		return 'A'
	case 'É', 'È', 'Ê', 'Ë':
		return 'E'
	case 'Î', 'Ï', 'Í':
		return 'I'
	case 'Ô', 'Ö', 'Ó', 'Õ':
		return 'O'
	case 'Ù', 'Û', 'Ü', 'Ú':
		return 'U'
	case 'Ç':
		return 'C'
	}
	return r
}

func (s *Server) artists(container string) func(*call) {
	return func(c *call) {
		artists, counts, stars := s.libraryArtists(c.r.Context(), c.viewer)
		sort.SliceStable(artists, func(i, j int) bool {
			return strings.ToLower(artists[i].Name) < strings.ToLower(artists[j].Name)
		})
		root := node(container, "ignoredArticles", "The Les Le La")
		if container == "indexes" {
			root.set("lastModified", s.clock().UnixMilli())
		}
		var cur *el
		for _, a := range artists {
			letter := indexLetter(a.Name)
			if cur == nil || cur.attrs[0].v != letter {
				cur = node("index", "name", letter)
				root.item(cur)
			}
			cur.item(s.artistEl("artist", a, counts[a.ID], userState{starred: stars[a.ID]}))
		}
		c.ok(root)
	}
}

func (s *Server) sourceError(c *call, err error) {
	if errors.Is(err, ErrNotFound) {
		c.fail(errNotFound, "Introuvable")
		return
	}
	s.o.Logger.Warn("source musicale indisponible", "err", err)
	c.fail(errGeneric, "Source musicale indisponible")
}

func (s *Server) idParam(c *call, want string) (int64, bool) {
	raw := c.param("id")
	if raw == "" {
		c.fail(errMissingParam, "Paramètre id requis")
		return 0, false
	}
	kind, id, ok := parseID(raw)
	if !ok || (want != "" && kind != want) {
		c.fail(errNotFound, "Identifiant inconnu")
		return 0, false
	}
	return id, true
}

func (s *Server) artist(c *call) {
	id, ok := s.idParam(c, KindArtist)
	if !ok {
		return
	}
	a, albums, err := s.o.Source.Artist(c.r.Context(), id)
	if err != nil {
		s.sourceError(c, err)
		return
	}
	ctx := c.r.Context()
	e := s.artistEl("artist", a, len(albums), s.state(ctx, c.viewer, KindArtist, a.ID))
	for _, al := range albums {
		e.item(s.albumEl("album", al, s.state(ctx, c.viewer, KindAlbum, al.ID), true))
	}
	c.ok(e)
}

func (s *Server) album(c *call) {
	id, ok := s.idParam(c, KindAlbum)
	if !ok {
		return
	}
	a, tracks, err := s.o.Source.Album(c.r.Context(), id)
	if err != nil {
		s.sourceError(c, err)
		return
	}
	ctx := c.r.Context()
	e := s.albumEl("album", a, s.state(ctx, c.viewer, KindAlbum, a.ID), true)
	for _, t := range tracks {
		e.item(s.songEl("song", t, s.state(ctx, c.viewer, KindTrack, t.ID)))
	}
	c.ok(e)
}

func (s *Server) song(c *call) {
	id, ok := s.idParam(c, KindTrack)
	if !ok {
		return
	}
	t, err := s.o.Source.Track(c.r.Context(), id)
	if err != nil {
		s.sourceError(c, err)
		return
	}
	c.ok(s.songEl("song", t, s.state(c.r.Context(), c.viewer, KindTrack, t.ID)))
}

// directory sert la navigation par dossiers des anciens clients :
// racine (id 1) → artistes, artiste → albums, album → titres.
func (s *Server) directory(c *call) {
	ctx := c.r.Context()
	if c.param("id") == "1" {
		artists, _, stars := s.libraryArtists(ctx, c.viewer)
		d := node("directory", "id", "1", "name", "Musique Ether-Remix")
		for _, a := range artists {
			d.item(node("child", "id", artistID(a.ID), "parent", "1", "isDir", true, "title", a.Name, "artist", a.Name).
				set("coverArt", pictureID(a.Picture)).set("starred", iso(stars[a.ID])))
		}
		c.ok(d)
		return
	}
	kind, id, ok := parseID(c.param("id"))
	if !ok {
		c.fail(errNotFound, "Identifiant inconnu")
		return
	}
	switch kind {
	case KindArtist:
		a, albums, err := s.o.Source.Artist(ctx, id)
		if err != nil {
			s.sourceError(c, err)
			return
		}
		d := node("directory", "id", artistID(a.ID), "parent", "1", "name", a.Name)
		for _, al := range albums {
			d.item(s.albumEl("child", al, s.state(ctx, c.viewer, KindAlbum, al.ID), false))
		}
		c.ok(d)
	case KindAlbum:
		a, tracks, err := s.o.Source.Album(ctx, id)
		if err != nil {
			s.sourceError(c, err)
			return
		}
		d := node("directory", "id", albumID(a.ID), "parent", artistIDOrEmpty(a.Artist.ID), "name", a.Title)
		for _, t := range tracks {
			d.item(s.songEl("child", t, s.state(ctx, c.viewer, KindTrack, t.ID)))
		}
		c.ok(d)
	default:
		c.fail(errNotFound, "Pas un dossier")
	}
}

// search interroge la HiFi API en direct. Une requête vide (utilisée par
// des clients comme Symfonium pour synchroniser) renvoie la bibliothèque.
func (s *Server) search(container string, id3 bool) func(*call) {
	return func(c *call) {
		ctx := c.r.Context()
		q := strings.TrimSpace(strings.Trim(c.param("query"), `"`))
		if q == "*" {
			q = ""
		}
		artistCount, artistOffset := c.num("artistCount", 20, 500), c.num("artistOffset", 0, 0)
		albumCount, albumOffset := c.num("albumCount", 20, 500), c.num("albumOffset", 0, 0)
		songCount, songOffset := c.num("songCount", 20, 500), c.num("songOffset", 0, 0)
		res := node(container)
		albumName, songName := "album", "song"
		if q == "" {
			lib := func(kind string, n, off int) []Item {
				if n == 0 {
					return nil
				}
				items, _ := s.o.Library.List(ctx, c.viewer, kind, ListOptions{Limit: n, Offset: off})
				return items
			}
			for _, it := range lib(KindArtist, artistCount, artistOffset) {
				res.item(s.artistEl("artist", decode[Artist](it), -1, userState{starred: it.StarredAt}))
			}
			for _, it := range lib(KindAlbum, albumCount, albumOffset) {
				res.item(s.albumEl(albumName, decode[Album](it), userState{it.StarredAt, it.PlayCount, it.PlayedAt}, id3))
			}
			for _, it := range lib(KindTrack, songCount, songOffset) {
				res.item(s.songEl(songName, decode[Track](it), userState{it.StarredAt, it.PlayCount, it.PlayedAt}))
			}
			c.ok(res)
			return
		}
		var firstErr error
		if artistCount > 0 {
			as, err := s.o.Source.SearchArtists(ctx, q, artistCount, artistOffset)
			firstErr = err
			for _, a := range as {
				res.item(s.artistEl("artist", a, -1, s.state(ctx, c.viewer, KindArtist, a.ID)))
			}
		}
		if albumCount > 0 {
			as, err := s.o.Source.SearchAlbums(ctx, q, albumCount, albumOffset)
			if firstErr == nil {
				firstErr = err
			}
			for _, a := range as {
				res.item(s.albumEl(albumName, a, s.state(ctx, c.viewer, KindAlbum, a.ID), id3))
			}
		}
		if songCount > 0 {
			ts, err := s.o.Source.SearchTracks(ctx, q, songCount, songOffset)
			if firstErr == nil {
				firstErr = err
			}
			for _, t := range ts {
				res.item(s.songEl(songName, t, s.state(ctx, c.viewer, KindTrack, t.ID)))
			}
		}
		if len(res.kids) == 0 && firstErr != nil {
			s.sourceError(c, firstErr)
			return
		}
		c.ok(res)
	}
}

func (s *Server) albumList(container string, id3 bool) func(*call) {
	return func(c *call) {
		o := ListOptions{Limit: c.num("size", 10, 500), Offset: c.num("offset", 0, 0)}
		if c.param("type") == "byGenre" && s.genreAlbums(c, container, id3, o) {
			return
		}
		switch c.param("type") {
		case "random":
			o.Order = "random"
		case "newest":
			o.Order = "added"
		case "recent":
			o.Order, o.Played = "played", true
		case "frequent":
			o.Order, o.Played = "frequent", true
		case "starred":
			o.Order, o.Starred = "starred", true
		case "byYear":
			// Filtré après lecture : toute la bibliothèque, puis la page.
			o.Order, o.Limit, o.Offset = "name", 0, 0
		case "alphabeticalByName", "alphabeticalByArtist", "byGenre", "highest":
			o.Order = "name"
		case "":
			c.fail(errMissingParam, "Paramètre type requis")
			return
		default:
			c.fail(errGeneric, "Type de liste inconnu")
			return
		}
		items, err := s.o.Library.List(c.r.Context(), c.viewer, KindAlbum, o)
		if err != nil {
			c.fail(errGeneric, "Bibliothèque indisponible")
			return
		}
		albums := make([]Album, 0, len(items))
		states := make([]userState, 0, len(items))
		for _, it := range items {
			albums = append(albums, decode[Album](it))
			states = append(states, userState{it.StarredAt, it.PlayCount, it.PlayedAt})
		}
		if c.param("type") == "alphabeticalByArtist" {
			sort.SliceStable(albums, func(i, j int) bool {
				return strings.ToLower(albums[i].Artist.Name) < strings.ToLower(albums[j].Artist.Name)
			})
		}
		if c.param("type") == "byYear" {
			albums, states = byYear(albums, states, c.num("fromYear", 0, 0), c.num("toYear", 9999, 0), c.num("size", 10, 500), c.num("offset", 0, 0))
		}
		e := node(container)
		for i, a := range albums {
			e.item(s.albumEl("album", a, states[i], id3))
		}
		c.ok(e)
	}
}

func (s *Server) randomSongs(c *call) {
	items, _ := s.o.Library.List(c.r.Context(), c.viewer, KindTrack, ListOptions{Order: "random", Limit: c.num("size", 10, 500)})
	e := node("randomSongs")
	for _, it := range items {
		e.item(s.songEl("song", decode[Track](it), userState{it.StarredAt, it.PlayCount, it.PlayedAt}))
	}
	c.ok(e)
}

func (s *Server) starred(container string, id3 bool) func(*call) {
	return func(c *call) {
		ctx := c.r.Context()
		o := ListOptions{Starred: true, Order: "starred"}
		e := node(container)
		artists, _ := s.o.Library.List(ctx, c.viewer, KindArtist, o)
		for _, it := range artists {
			e.item(s.artistEl("artist", decode[Artist](it), -1, userState{starred: it.StarredAt}))
		}
		albums, _ := s.o.Library.List(ctx, c.viewer, KindAlbum, o)
		for _, it := range albums {
			e.item(s.albumEl("album", decode[Album](it), userState{it.StarredAt, it.PlayCount, it.PlayedAt}, id3))
		}
		tracks, _ := s.o.Library.List(ctx, c.viewer, KindTrack, o)
		for _, it := range tracks {
			e.item(s.songEl("song", decode[Track](it), userState{it.StarredAt, it.PlayCount, it.PlayedAt}))
		}
		c.ok(e)
	}
}

func (s *Server) star(on bool) func(*call) {
	return func(c *call) {
		ctx := c.r.Context()
		type target struct {
			kind string
			id   int64
		}
		var targets []target
		for _, raw := range c.params("id") {
			if kind, id, ok := parseID(raw); ok {
				targets = append(targets, target{kind, id})
			}
		}
		for _, raw := range c.params("albumId") {
			if kind, id, ok := parseID(raw); ok && kind == KindAlbum {
				targets = append(targets, target{kind, id})
			}
		}
		for _, raw := range c.params("artistId") {
			if kind, id, ok := parseID(raw); ok && kind == KindArtist {
				targets = append(targets, target{kind, id})
			}
		}
		if len(targets) == 0 {
			c.fail(errMissingParam, "Paramètre id, albumId ou artistId requis")
			return
		}
		for _, t := range targets {
			var err error
			switch t.kind {
			case KindTrack:
				var tr Track
				if tr, err = s.trackSnapshot(ctx, c.viewer, t.id, on); err == nil {
					err = s.o.Library.StarTrack(ctx, c.viewer, tr, on)
				}
			case KindAlbum:
				var al Album
				if al, err = s.albumSnapshot(ctx, c.viewer, t.id, on); err == nil {
					err = s.o.Library.StarAlbum(ctx, c.viewer, al, on)
				}
			case KindArtist:
				var ar Artist
				if ar, err = s.artistSnapshot(ctx, c.viewer, t.id, on); err == nil {
					err = s.o.Library.StarArtist(ctx, c.viewer, ar, on)
				}
			}
			if err != nil {
				s.sourceError(c, err)
				return
			}
		}
		c.ok(nil)
	}
}

// Les instantanés viennent de la bibliothèque quand on retire un favori,
// pour qu'un objet disparu côté HiFi puisse toujours être retiré.
func (s *Server) trackSnapshot(ctx context.Context, viewer, id int64, fresh bool) (Track, error) {
	if !fresh {
		if it, ok, _ := s.o.Library.Get(ctx, viewer, KindTrack, id); ok {
			return decode[Track](it), nil
		}
	}
	return s.o.Source.Track(ctx, id)
}

func (s *Server) albumSnapshot(ctx context.Context, viewer, id int64, fresh bool) (Album, error) {
	if !fresh {
		if it, ok, _ := s.o.Library.Get(ctx, viewer, KindAlbum, id); ok {
			return decode[Album](it), nil
		}
	}
	a, _, err := s.o.Source.Album(ctx, id)
	return a, err
}

func (s *Server) artistSnapshot(ctx context.Context, viewer, id int64, fresh bool) (Artist, error) {
	if !fresh {
		if it, ok, _ := s.o.Library.Get(ctx, viewer, KindArtist, id); ok {
			return decode[Artist](it), nil
		}
	}
	a, _, err := s.o.Source.Artist(ctx, id)
	return a, err
}

func (s *Server) scrobble(c *call) {
	ids := c.params("id")
	if len(ids) == 0 {
		c.fail(errMissingParam, "Paramètre id requis")
		return
	}
	if c.param("submission") == "false" {
		if kind, id, ok := parseID(ids[0]); ok && kind == KindTrack {
			s.observeMusic(c.r, c.viewer, "subsonic", id, "playing", nil, c.user)
		}
		c.ok(nil) // Observation uniquement : pas d’entrée dans l’historique.
		return
	}
	ctx := c.r.Context()
	for _, raw := range ids {
		kind, id, ok := parseID(raw)
		if !ok || kind != KindTrack {
			continue
		}
		t, err := s.trackSnapshot(ctx, c.viewer, id, true)
		if err != nil {
			if it, ok, _ := s.o.Library.Get(ctx, c.viewer, KindTrack, id); ok {
				t = decode[Track](it)
			} else {
				continue
			}
		}
		if err := s.o.Library.Played(ctx, c.viewer, t); err != nil {
			c.fail(errGeneric, "Bibliothèque indisponible")
			return
		}
	}
	c.ok(nil)
}

// --- flux ---

var relayedHeaders = []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"}

func (s *Server) stream(c *call) {
	id, ok := s.idParam(c, KindTrack)
	if !ok {
		return
	}
	if err := s.relayAudio(c.w, c.r, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			c.fail(errNotFound, "Introuvable")
			return
		}
		c.fail(errGeneric, "Flux indisponible")
	}
}

// relayAttempts borne les sources essayées pour une lecture : un lien
// expiré ou refusé fait passer à la source suivante.
const relayAttempts = 3

// relayAudio relaie le flux audio d'un titre (Range compris). Une erreur
// renvoyée signifie que rien n'a encore été écrit dans w.
func (s *Server) relayAudio(w http.ResponseWriter, r *http.Request, id int64) error {
	ctx := r.Context()
	var last error = ErrNotFound
	for attempt := 0; attempt < relayAttempts; attempt++ {
		st, err := s.o.Source.Stream(ctx, id, s.o.Quality)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt == 0 && !errors.Is(err, ErrNotFound) {
				s.o.Logger.Warn("source musicale indisponible", "err", redactErr(err))
			}
			if attempt == 0 {
				return err
			}
			return last
		}
		if st.Open != nil {
			f, modified, err := st.Open()
			if err != nil {
				return err
			}
			defer f.Close()
			w.Header().Set("Content-Type", st.Mime)
			w.Header().Set("X-Accent-Audio-Source", st.Provider)
			http.ServeContent(w, r, "audio", modified, f)
			return nil
		}
		res, err := s.openAudio(ctx, r, st.URL)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.o.Logger.Warn("flux musical refusé, source suivante", "track", id, "quality", st.Quality, "err", redactErr(err))
			last = err
			ctx = WithSkippedStreams(ctx, st.URL)
			continue
		}
		defer res.Body.Close()
		s.writeAudio(w, r, st, res)
		return nil
	}
	return last
}

// openAudio ouvre un lien audio ; un statut inattendu est une erreur.
func (s *Server) openAudio(ctx context.Context, r *http.Request, link string) (*http.Response, error) {
	if err := checkAudioHost(ctx, link); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}
	res, err := s.o.Media.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusPartialContent && res.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		res.Body.Close()
		return nil, fmt.Errorf("music: flux refusé (%d)", res.StatusCode)
	}
	return res, nil
}

// writeAudio relaie la réponse d'un lien audio.
func (s *Server) writeAudio(w http.ResponseWriter, r *http.Request, st Stream, res *http.Response) {
	h := w.Header()
	if st.Provider != "" {
		h.Set("X-Accent-Audio-Source", st.Provider)
	}
	for _, k := range relayedHeaders {
		if v := res.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	if ct := res.Header.Get("Content-Type"); ct == "" || ct == "application/octet-stream" || ct == "binary/octet-stream" {
		mime := st.Mime
		if mime == "" {
			mime, _ = s.contentType()
		}
		h.Set("Content-Type", mime)
	}
	if h.Get("Accept-Ranges") == "" && res.StatusCode == http.StatusOK && res.ContentLength > 0 {
		h.Set("Accept-Ranges", "bytes")
	}
	w.WriteHeader(res.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, res.Body)
}

func (s *Server) cover(c *call) {
	raw := c.param("id")
	prefix, ref, _ := strings.Cut(raw, "-")
	if prefix != "c" && prefix != "p" {
		c.w.WriteHeader(http.StatusNotFound)
		return
	}
	want, _ := strconv.Atoi(c.param("size"))
	s.relayImage(c.w, c.r, prefix[0], ref, want)
}

// relayImage relaie une pochette (kind 'c') ou une photo d'artiste ('p').
func (s *Server) relayImage(w http.ResponseWriter, r *http.Request, kind byte, ref string, size int) {
	if source, ok := s.o.Source.(*OwnedSource); ok && strings.HasPrefix(ref, "ib-") && !source.artAllowed(r.Context(), ref) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if !imageRefRe.MatchString(ref) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	u := s.o.Source.ImageURL(kind, ref, size)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u, nil)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	res, err := s.o.Media.Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=604800")
	if cl := res.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = io.Copy(w, io.LimitReader(res.Body, 16<<20))
}

// byYear garde les albums sortis entre from et to (inclus), du plus ancien
// au plus récent, ou l'inverse si from > to (décennies, périodes).
func byYear(albums []Album, states []userState, from, to, size, offset int) ([]Album, []userState) {
	lo, hi, desc := from, to, from > to
	if desc {
		lo, hi = to, from
	}
	type pair struct {
		a Album
		s userState
	}
	var keep []pair
	for i, a := range albums {
		if y := a.Year(); y > 0 && y >= lo && y <= hi {
			keep = append(keep, pair{a, states[i]})
		}
	}
	sort.SliceStable(keep, func(i, j int) bool {
		if desc {
			return keep[i].a.Year() > keep[j].a.Year()
		}
		return keep[i].a.Year() < keep[j].a.Year()
	})
	if offset > len(keep) {
		offset = len(keep)
	}
	keep = keep[offset:]
	if size > 0 && len(keep) > size {
		keep = keep[:size]
	}
	outA, outS := make([]Album, len(keep)), make([]userState, len(keep))
	for i, p := range keep {
		outA[i], outS[i] = p.a, p.s
	}
	return outA, outS
}
