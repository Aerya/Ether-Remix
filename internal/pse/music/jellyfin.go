package music

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// Bibliothèque « Musique » des clients Jellyfin (et Emby) : mêmes artistes,
// albums et titres que l'API Subsonic, servis sous forme d'objets Jellyfin.
// La bibliothèque montre d'abord les favoris et écoutes du spectateur, puis
// les classements Deezer ; la recherche interroge tout le catalogue. La
// lecture est directe (pas de transcodage) : PSE relaie le flux trouvé par
// les addons Eclipse.

// JellyfinViewNumber est le numéro de vue de la bibliothèque Musique, hors
// des numéros de bibliothèques d'Ether (petits entiers) et des rangées
// personnelles (bit 62).
const JellyfinViewNumber = frontend.PSEMusicViewNumber

const (
	guidArtist = 1
	guidAlbum  = 2
	guidTrack  = 3
)

// musicGUIDPrefix occupe les 8 premiers octets des GUID musique (« PSMU »),
// loin des espaces de noms d'Ether (1 à 10).
const musicGUIDPrefix = "50534d55000000"

// JellyfinGUID encode un identifiant musique au format Jellyfin.
func JellyfinGUID(kind int, id int64) string {
	return musicGUIDPrefix + "0" + strconv.Itoa(kind) + leftPad(strconv.FormatUint(uint64(id), 16), 16)
}

func leftPad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return strings.Repeat("0", n-len(s)) + s
}

// ParseJellyfinGUID décode un GUID musique (avec ou sans tirets).
func ParseJellyfinGUID(v string) (kind int, id int64, ok bool) {
	v = strings.ToLower(strings.ReplaceAll(v, "-", ""))
	if len(v) != 32 || !strings.HasPrefix(v, musicGUIDPrefix) {
		return 0, 0, false
	}
	k := int(v[15] - '0')
	if v[14] != '0' || k < guidArtist || k > guidGenre {
		return 0, 0, false
	}
	n, err := strconv.ParseUint(v[16:], 16, 64)
	if err != nil || n == 0 || n > 1<<62 {
		return 0, 0, false
	}
	return k, int64(n), true
}

func sameGUID(a, b string) bool {
	return a != "" && strings.EqualFold(strings.ReplaceAll(a, "-", ""), strings.ReplaceAll(b, "-", ""))
}

// IsJellyfinGUID indique si v désigne un objet musique.
func IsJellyfinGUID(v string) bool { _, _, ok := ParseJellyfinGUID(v); return ok }

// Jellyfin relie le serveur musique au frontal Jellyfin d'Ether.
type Jellyfin = frontend.PSEMusicJellyfin

type jfReq struct {
	s      *Server
	j      Jellyfin
	w      http.ResponseWriter
	r      *http.Request
	viewer int64
	q      map[string]string
}

func (q *jfReq) get(k string) string { return q.q[strings.ToLower(k)] }

func (q *jfReq) list(k string) []string {
	var out []string
	for _, p := range strings.Split(q.get(k), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (q *jfReq) num(k string, def int) int {
	n, err := strconv.Atoi(q.get(k))
	if err != nil || n < 0 {
		return def
	}
	return n
}

func (q *jfReq) reply(v any) {
	h := q.w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	_ = json.NewEncoder(q.w).Encode(v)
}

func (q *jfReq) result(items []any, total, start int) {
	if items == nil {
		items = []any{}
	}
	q.reply(map[string]any{"Items": items, "TotalRecordCount": total, "StartIndex": start})
}

// ServeJellyfin répond aux requêtes Jellyfin qui concernent la musique et
// renvoie false pour toutes les autres, qu'Ether traite normalement.
// viewer vaut 0 pour une requête sans identifiant valide : seules les
// images y sont servies.
func (s *Server) ServeJellyfin(w http.ResponseWriter, r *http.Request, viewer int64, j Jellyfin) bool {
	segs := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	low := make([]string, len(segs))
	for i, p := range segs {
		low[i] = strings.ToLower(p)
	}
	// /Users/{id}/Items/… et /UserItems/… (Jellyfin 10.9+) → /Items/…
	if len(low) >= 3 && low[0] == "users" {
		segs, low = segs[2:], low[2:]
	}
	if len(low) > 0 {
		switch low[0] {
		case "useritems":
			low[0] = "items"
		case "userfavoriteitems":
			low[0] = "favoriteitems"
		case "userplayeditems":
			low[0] = "playeditems"
		}
	}
	q := &jfReq{s: s, j: j, w: w, r: r, viewer: viewer, q: map[string]string{}}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			q.q[strings.ToLower(k)] = v[0]
		}
	}
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	// Images : sans identifiant, comme Jellyfin.
	if get && len(low) >= 4 && low[0] == "items" && low[2] == "images" {
		kind, id, ok := ParseJellyfinGUID(segs[1])
		if !ok {
			return false
		}
		q.image(kind, id)
		return true
	}
	if viewer == 0 || len(low) == 0 {
		return false
	}
	switch {
	case low[0] == "sessions" && r.Method == http.MethodPost && len(low) >= 2 && low[1] == "playing":
		return q.report(len(low) == 3 && low[2] == "stopped")
	case low[0] == "items" && len(low) == 1 && get:
		return q.items()
	case low[0] == "playlists" && len(low) >= 2:
		kind, id, ok := ParseJellyfinGUID(segs[1])
		if !ok || kind != guidPlaylist {
			return false
		}
		q.playlists(low[2:], id)
		return true
	case low[0] == "items" && len(low) == 2 && low[1] == "latest" && get:
		if !sameGUID(q.get("ParentId"), j.ViewID) {
			return false
		}
		q.latest()
		return true
	case (low[0] == "artists") && get:
		if len(low) == 1 || (len(low) == 2 && low[1] == "albumartists") {
			if p := q.get("ParentId"); p != "" && !sameGUID(p, j.ViewID) {
				return false
			}
			q.artists()
			return true
		}
		if len(low) >= 3 && q.discover(segs[1], low[len(low)-1]) {
			return true
		}
		return false
	case low[0] == "audio" && len(low) >= 3 && get:
		_, id, ok := ParseJellyfinGUID(segs[1])
		if !ok || low[2] == "lyrics" {
			return ok && q.notFound()
		}
		if err := s.relayAudio(w, r, id); err != nil {
			http.Error(w, "Flux indisponible", http.StatusBadGateway)
		}
		return true
	case (low[0] == "musicgenres" || low[0] == "genres") && len(low) >= 3 && low[2] == "instantmix" && get:
		return q.genreMix(segs[1])
	case (low[0] == "musicgenres" || low[0] == "genres" || low[0] == "studios" || low[0] == "persons") && get:
		if !sameGUID(q.get("ParentId"), j.ViewID) {
			return false
		}
		if low[0] != "studios" && low[0] != "persons" && len(low) == 1 {
			q.page(q.genreDTOs())
			return true
		}
		q.result(nil, 0, 0)
		return true
	}
	if len(low) >= 2 && (low[0] == "items" || low[0] == "favoriteitems" || low[0] == "playeditems" || low[0] == "playingitems") {
		if sameGUID(segs[1], j.ViewID) {
			if low[0] == "items" && len(low) == 2 && get && j.Folder != nil {
				q.reply(j.Folder())
				return true
			}
			return false
		}
		kind, id, ok := ParseJellyfinGUID(segs[1])
		if !ok {
			return false
		}
		q.item(low[0], low[2:], kind, id)
		return true
	}
	// Toute autre requête qui vise un objet musique (similaires, mix…).
	for i := 1; i < len(segs); i++ {
		if IsJellyfinGUID(segs[i]) {
			if get && q.discover(segs[i], low[len(low)-1]) {
				return true
			}
			if !get {
				w.WriteHeader(http.StatusNoContent)
			} else {
				q.result(nil, 0, 0)
			}
			return true
		}
	}
	return false
}

func (q *jfReq) notFound() bool {
	http.NotFound(q.w, q.r)
	return true
}

// item traite /Items/{id}[/…], /FavoriteItems/{id}, /PlayedItems/{id} et
// /PlayingItems/{id}.
func (q *jfReq) item(base string, rest []string, kind int, id int64) {
	ctx := q.r.Context()
	if kind == guidPlaylist && base != "items" {
		q.reply(q.userData(kind, id, jfState{}))
		return
	}
	switch base {
	case "favoriteitems":
		on := q.r.Method != http.MethodDelete
		if err := q.star(ctx, kind, id, on); err != nil {
			http.Error(q.w, "Source musicale indisponible", http.StatusBadGateway)
			return
		}
		q.reply(q.userData(kind, id, q.state(ctx)))
		return
	case "playeditems":
		if q.r.Method == http.MethodPost && kind == guidTrack {
			if t, err := q.track(ctx, id); err == nil {
				_ = q.s.o.Library.Played(ctx, q.viewer, t)
			}
		}
		q.reply(q.userData(kind, id, q.state(ctx)))
		return
	case "playingitems":
		q.w.WriteHeader(http.StatusNoContent)
		return
	}
	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}
	switch sub {
	case "":
		dto, err := q.dto(ctx, kind, id, q.state(ctx))
		if err != nil {
			q.sourceError(err)
			return
		}
		q.reply(dto)
	case "playbackinfo":
		if kind != guidTrack {
			http.NotFound(q.w, q.r)
			return
		}
		t, err := q.track(ctx, id)
		if err != nil {
			q.sourceError(err)
			return
		}
		q.reply(q.playbackInfo(t))
	case "userdata":
		q.reply(q.userData(kind, id, q.state(ctx)))
	case "localtrailers", "specialfeatures", "ancestors", "intros":
		if sub == "intros" {
			q.result(nil, 0, 0)
			return
		}
		q.reply([]any{})
	case "lyrics", "remoteimages", "externalidinfos":
		http.NotFound(q.w, q.r)
	case "instantmix", "similar":
		if !q.discover(JellyfinGUID(kind, id), sub) {
			q.result(nil, 0, 0)
		}
	default:
		if q.r.Method == http.MethodGet || q.r.Method == http.MethodHead {
			q.result(nil, 0, 0)
			return
		}
		q.w.WriteHeader(http.StatusNoContent)
	}
}

func (q *jfReq) sourceError(err error) {
	if err == ErrNotFound {
		http.NotFound(q.w, q.r)
		return
	}
	q.s.o.Logger.Warn("source musicale indisponible", "err", redactErr(err))
	http.Error(q.w, "Source musicale indisponible", http.StatusBadGateway)
}

// --- état du spectateur ---

type jfState map[string]Item // clé : kind + id

func stateKey(kind string, id int64) string { return kind + ":" + strconv.FormatInt(id, 10) }

func (q *jfReq) state(ctx context.Context) jfState {
	st := jfState{}
	for _, k := range []string{KindArtist, KindAlbum, KindTrack} {
		items, _ := q.s.o.Library.List(ctx, q.viewer, k, ListOptions{})
		for _, it := range items {
			st[stateKey(k, it.ID)] = it
		}
	}
	return st
}

func libKind(kind int) string {
	switch kind {
	case guidArtist:
		return KindArtist
	case guidAlbum:
		return KindAlbum
	}
	return KindTrack
}

func (q *jfReq) userData(kind int, id int64, st jfState) map[string]any {
	it := st[stateKey(libKind(kind), id)]
	guid := JellyfinGUID(kind, id)
	ud := map[string]any{
		"PlaybackPositionTicks": 0,
		"PlayCount":             it.PlayCount,
		"IsFavorite":            it.StarredAt > 0,
		"Played":                kind == guidTrack && it.PlayCount > 0,
		"Key":                   guid,
		"ItemId":                guid,
	}
	if it.PlayedAt > 0 {
		ud["LastPlayedDate"] = time.Unix(it.PlayedAt, 0).UTC().Format(time.RFC3339)
	}
	return ud
}

func (q *jfReq) star(ctx context.Context, kind int, id int64, on bool) error {
	lib := q.s.o.Library
	switch kind {
	case guidArtist:
		a, err := q.s.artistSnapshot(ctx, q.viewer, id, on)
		if err != nil {
			return err
		}
		return lib.StarArtist(ctx, q.viewer, a, on)
	case guidAlbum:
		a, err := q.s.albumSnapshot(ctx, q.viewer, id, on)
		if err != nil {
			return err
		}
		return lib.StarAlbum(ctx, q.viewer, a, on)
	}
	t, err := q.s.trackSnapshot(ctx, q.viewer, id, on)
	if err != nil {
		return err
	}
	return lib.StarTrack(ctx, q.viewer, t, on)
}

// --- catalogue (avec mémoire courte) ---

func (q *jfReq) album(ctx context.Context, id int64) (Album, []Track, error) {
	type pair struct {
		a Album
		t []Track
	}
	p, err := memoized(q.s.cache, "al:"+strconv.FormatInt(id, 10), q.s.clock(), func() (pair, error) {
		a, t, err := q.s.o.Source.Album(ctx, id)
		return pair{a, t}, err
	})
	return p.a, p.t, err
}

func (q *jfReq) artist(ctx context.Context, id int64) (Artist, []Album, error) {
	type pair struct {
		a  Artist
		al []Album
	}
	p, err := memoized(q.s.cache, "ar:"+strconv.FormatInt(id, 10), q.s.clock(), func() (pair, error) {
		a, al, err := q.s.o.Source.Artist(ctx, id)
		return pair{a, al}, err
	})
	return p.a, p.al, err
}

func (q *jfReq) track(ctx context.Context, id int64) (Track, error) {
	if it, ok, _ := q.s.o.Library.Get(ctx, q.viewer, KindTrack, id); ok {
		if t := decode[Track](it); t.Title != "" {
			return t, nil
		}
	}
	return memoized(q.s.cache, "tr:"+strconv.FormatInt(id, 10), q.s.clock(), func() (Track, error) {
		return q.s.o.Source.Track(ctx, id)
	})
}

func (q *jfReq) dto(ctx context.Context, kind int, id int64, st jfState) (map[string]any, error) {
	switch kind {
	case guidGenre:
		return q.genreDTO(ctx, id)
	case guidPlaylist:
		pl, ok := q.s.playlist(ctx, q.viewer, id)
		if !ok {
			return nil, ErrNotFound
		}
		return q.playlistDTO(pl, st), nil
	case guidArtist:
		a, albums, err := q.artist(ctx, id)
		if err != nil {
			return nil, err
		}
		d := q.artistDTO(a, st)
		d["ChildCount"] = len(albums)
		d["AlbumCount"] = len(albums)
		return d, nil
	case guidAlbum:
		a, tracks, err := q.album(ctx, id)
		if err != nil {
			return nil, err
		}
		if a.Tracks == 0 {
			a.Tracks = len(tracks)
		}
		return q.albumDTO(a, st), nil
	}
	t, err := q.track(ctx, id)
	if err != nil {
		return nil, err
	}
	return q.trackDTO(t, st), nil
}

// --- objets Jellyfin ---

func (q *jfReq) base(kind int, id int64, name string, st jfState) map[string]any {
	guid := JellyfinGUID(kind, id)
	return map[string]any{
		"Id":                guid,
		"Name":              name,
		"SortName":          strings.ToLower(strings.TrimSpace(name)),
		"ServerId":          q.j.ServerID,
		"Etag":              guid,
		"CanDelete":         false,
		"CanDownload":       false,
		"LocationType":      "FileSystem",
		"PlayAccess":        "Full",
		"ImageTags":         map[string]string{},
		"BackdropImageTags": []string{},
		"ImageBlurHashes":   map[string]any{},
		"Genres":            []string{},
		"GenreItems":        []any{},
		"Tags":              []string{},
		"People":            []any{},
		"Studios":           []any{},
		"ProviderIds":       map[string]string{"Deezer": strconv.FormatInt(id, 10)},
		"UserData":          q.userData(kind, id, st),
	}
}

func artistRef(a Artist) map[string]any {
	return map[string]any{"Name": a.Name, "Id": JellyfinGUID(guidArtist, a.ID)}
}

func (q *jfReq) artistDTO(a Artist, st jfState) map[string]any {
	d := q.base(guidArtist, a.ID, a.Name, st)
	d["Type"] = "MusicArtist"
	d["IsFolder"] = true
	d["MediaType"] = "Unknown"
	d["ParentId"] = q.j.ViewID
	if a.Picture != "" {
		d["ImageTags"] = map[string]string{"Primary": "p" + a.Picture}
		d["PrimaryImageAspectRatio"] = 1
	}
	return d
}

func (q *jfReq) albumDTO(a Album, st jfState) map[string]any {
	d := q.base(guidAlbum, a.ID, a.Title, st)
	d["Type"] = "MusicAlbum"
	d["IsFolder"] = true
	d["MediaType"] = "Unknown"
	d["ParentId"] = q.j.ViewID
	if a.Artist.ID != 0 {
		d["AlbumArtist"] = a.Artist.Name
		d["AlbumArtists"] = []any{artistRef(a.Artist)}
		d["ArtistItems"] = []any{artistRef(a.Artist)}
		d["Artists"] = []string{a.Artist.Name}
		d["ParentId"] = JellyfinGUID(guidArtist, a.Artist.ID)
	}
	if y := a.Year(); y > 0 {
		d["ProductionYear"] = y
	}
	if len(a.ReleaseDate) == 10 {
		d["PremiereDate"] = a.ReleaseDate + "T00:00:00.0000000Z"
	}
	if a.Tracks > 0 {
		d["ChildCount"] = a.Tracks
		d["SongCount"] = a.Tracks
	}
	if a.Duration > 0 {
		d["RunTimeTicks"] = int64(a.Duration) * 10_000_000
		d["CumulativeRunTimeTicks"] = int64(a.Duration) * 10_000_000
	}
	if a.Cover != "" {
		d["ImageTags"] = map[string]string{"Primary": "c" + a.Cover}
		d["PrimaryImageAspectRatio"] = 1
	}
	return d
}

func (q *jfReq) trackDTO(t Track, st jfState) map[string]any {
	d := q.base(guidTrack, t.ID, t.Title, st)
	_, suffix := q.s.trackContentType(t)
	d["Type"] = "Audio"
	d["IsFolder"] = false
	d["MediaType"] = "Audio"
	d["Container"] = suffix
	d["HasLyrics"] = false
	d["RunTimeTicks"] = int64(t.Duration) * 10_000_000
	if t.Number > 0 {
		d["IndexNumber"] = t.Number
	}
	vol := t.Volume
	if vol <= 0 {
		vol = 1
	}
	d["ParentIndexNumber"] = vol
	artists := []any{}
	names := []string{}
	if t.Artist.ID != 0 {
		artists = append(artists, artistRef(t.Artist))
		names = append(names, t.Artist.Name)
	}
	d["Artists"] = names
	d["ArtistItems"] = artists
	if t.Album.ID != 0 {
		albumID := JellyfinGUID(guidAlbum, t.Album.ID)
		d["Album"] = t.Album.Title
		d["AlbumId"] = albumID
		d["ParentId"] = albumID
		aa := t.Album.Artist
		if aa.ID == 0 {
			aa = t.Artist
		}
		if aa.ID != 0 {
			d["AlbumArtist"] = aa.Name
			d["AlbumArtists"] = []any{artistRef(aa)}
		}
		if y := t.Album.Year(); y > 0 {
			d["ProductionYear"] = y
		}
		if t.Album.Cover != "" {
			tag := "c" + t.Album.Cover
			d["AlbumPrimaryImageTag"] = tag
			d["ImageTags"] = map[string]string{"Primary": tag}
			d["PrimaryImageAspectRatio"] = 1
		}
	}
	d["MediaSources"] = []any{q.mediaSource(t)}
	return d
}

func (q *jfReq) mediaSource(t Track) map[string]any {
	_, suffix := q.s.trackContentType(t)
	codec := trackCodec(t, suffix)
	guid := JellyfinGUID(guidTrack, t.ID)
	return map[string]any{
		"Protocol": "File", "Id": guid, "Path": guid + "." + suffix, "Type": "Default",
		"Container": suffix, "Name": t.Title, "IsRemote": false,
		"RunTimeTicks":          int64(t.Duration) * 10_000_000,
		"SupportsTranscoding":   false,
		"SupportsDirectStream":  true,
		"SupportsDirectPlay":    true,
		"SupportsProbing":       false,
		"IsInfiniteStream":      false,
		"RequiresOpening":       false,
		"RequiresClosing":       false,
		"RequiresLooping":       false,
		"ReadAtNativeFramerate": false,
		"MediaStreams": []any{map[string]any{
			"Codec": codec, "Type": "Audio", "Index": 0, "IsDefault": true,
			"Channels": trackChannels(t), "SampleRate": trackSampleRate(t), "IsExternal": false,
			"IsTextSubtitleStream": false, "SupportsExternalStream": false,
			"DisplayTitle": strings.ToUpper(codec),
		}},
		"MediaAttachments":        []any{},
		"Formats":                 []any{},
		"DefaultAudioStreamIndex": 0,
	}
}

func (q *jfReq) playbackInfo(t Track) map[string]any {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return map[string]any{"MediaSources": []any{q.mediaSource(t)}, "PlaySessionId": hex.EncodeToString(b[:])}
}

// --- listes ---

func isMusicType(t string) bool {
	switch strings.ToLower(t) {
	case "audio", "musicalbum", "musicartist":
		return true
	}
	return false
}

func hasType(types []string, want string) bool {
	if len(types) == 0 {
		return false
	}
	for _, t := range types {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

func (q *jfReq) firstArtistParam() (int64, bool) {
	for _, k := range []string{"ArtistIds", "AlbumArtistIds", "ContributingArtistIds"} {
		for _, v := range q.list(k) {
			if kind, id, ok := ParseJellyfinGUID(v); ok && kind == guidArtist {
				return id, true
			}
		}
	}
	return 0, false
}

// items traite /Items. Renvoie false si la requête ne vise pas la musique.
func (q *jfReq) items() bool {
	ctx := q.r.Context()
	types := q.list("IncludeItemTypes")
	parent := q.get("ParentId")
	musicOnly := len(types) > 0
	for _, t := range types {
		musicOnly = musicOnly && isMusicType(t)
	}
	var out []any
	st := q.state(ctx)
	switch {
	case len(q.list("Ids")) > 0:
		ids := q.list("Ids")
		for _, v := range ids {
			if !IsJellyfinGUID(v) {
				return false
			}
		}
		for _, v := range ids {
			kind, id, _ := ParseJellyfinGUID(v)
			if d, err := q.dto(ctx, kind, id, st); err == nil {
				out = append(out, d)
			}
		}
	case IsJellyfinGUID(parent) || len(q.list("AlbumIds")) > 0 && IsJellyfinGUID(q.list("AlbumIds")[0]):
		kind, id, _ := ParseJellyfinGUID(parent)
		if parent == "" {
			kind, id, _ = ParseJellyfinGUID(q.list("AlbumIds")[0])
		}
		switch kind {
		case guidPlaylist:
			out, _ = q.playlistTracks(ctx, id)
		case guidAlbum:
			_, tracks, err := q.album(ctx, id)
			if err != nil {
				q.sourceError(err)
				return true
			}
			for _, t := range tracks {
				out = append(out, q.trackDTO(t, st))
			}
		case guidArtist:
			out = q.artistChildren(ctx, id, types, st)
		case guidGenre:
			out = q.genreChildren(ctx, id, types, st)
		}
	case q.genreParam(parent) != 0:
		out = q.genreChildren(ctx, q.genreParam(parent), types, st)
	default:
		if id, ok := q.firstArtistParam(); ok {
			out = q.artistChildren(ctx, id, types, st)
			break
		}
		if !sameGUID(parent, q.j.ViewID) && !(parent == "" && musicOnly) {
			return false
		}
		if hasType(types, "Playlist") {
			for _, pl := range q.s.playlistsOf(ctx, q.viewer) {
				out = append(out, q.playlistDTO(pl, st))
			}
			break
		}
		if term := strings.TrimSpace(q.get("searchTerm")); term != "" {
			out = q.search(ctx, term, types, st)
			break
		}
		out = q.library(ctx, types, st)
	}
	q.page(out)
	return true
}

func (q *jfReq) page(all []any) {
	start := q.num("StartIndex", 0)
	total := len(all)
	start = min(start, total)
	end := total
	if n := q.num("Limit", 0); n > 0 {
		end = min(start+n, total)
	}
	q.result(all[start:end], total, start)
}

func (q *jfReq) artistChildren(ctx context.Context, id int64, types []string, st jfState) []any {
	var out []any
	if hasType(types, "Audio") && !hasType(types, "MusicAlbum") {
		if tt, ok := q.s.o.Source.(TopTracker); ok {
			tracks, _ := memoized(q.s.cache, "top:"+strconv.FormatInt(id, 10), q.s.clock(), func() ([]Track, error) {
				return tt.ArtistTop(ctx, id, 50)
			})
			for _, t := range tracks {
				out = append(out, q.trackDTO(t, st))
			}
		}
		return out
	}
	_, albums, err := q.artist(ctx, id)
	if err != nil {
		return nil
	}
	for _, a := range albums {
		out = append(out, q.albumDTO(a, st))
	}
	return out
}

func (q *jfReq) search(ctx context.Context, term string, types []string, st jfState) []any {
	limit := q.num("Limit", 30)
	if limit == 0 || limit > 100 {
		limit = 100
	}
	all := len(types) == 0
	var out []any
	src := q.s.o.Source
	if all || hasType(types, "MusicArtist") {
		as, _ := src.SearchArtists(ctx, term, limit, 0)
		for _, a := range as {
			out = append(out, q.artistDTO(a, st))
		}
	}
	if all || hasType(types, "MusicAlbum") {
		as, _ := src.SearchAlbums(ctx, term, limit, 0)
		for _, a := range as {
			out = append(out, q.albumDTO(a, st))
		}
	}
	if all || hasType(types, "Audio") {
		ts, _ := src.SearchTracks(ctx, term, limit, 0)
		for _, t := range ts {
			out = append(out, q.trackDTO(t, st))
		}
	}
	return out
}

func (q *jfReq) listOptions() (ListOptions, bool) {
	o := ListOptions{}
	filters := strings.ToLower(q.get("Filters"))
	if strings.Contains(filters, "isfavorite") || strings.EqualFold(q.get("IsFavorite"), "true") {
		o.Starred = true
	}
	if strings.Contains(filters, "isplayed") || strings.EqualFold(q.get("IsPlayed"), "true") {
		o.Played = true
	}
	sortBy := strings.ToLower(q.get("SortBy"))
	switch {
	case strings.Contains(sortBy, "random"):
		o.Order = "random"
	case strings.Contains(sortBy, "dateplayed"):
		o.Order, o.Played = "played", true
	case strings.Contains(sortBy, "playcount"):
		o.Order, o.Played = "frequent", true
	case strings.Contains(sortBy, "datecreated"), strings.Contains(sortBy, "datelastcontentadded"):
		o.Order = "added"
	default:
		o.Order = "name"
	}
	// Les classements ne complètent que les listes générales.
	return o, !o.Starred && !o.Played
}

func (q *jfReq) library(ctx context.Context, types []string, st jfState) []any {
	o, charts := q.listOptions()
	ch, _ := q.s.o.Source.(Charter)
	if ch == nil {
		charts = false
	}
	kind := KindAlbum
	switch {
	case hasType(types, "MusicArtist"):
		kind = KindArtist
	case hasType(types, "Audio") && !hasType(types, "MusicAlbum"):
		kind = KindTrack
	}
	items, _ := q.s.o.Library.List(ctx, q.viewer, kind, o)
	seen := map[int64]bool{}
	var out []any
	add := func(id int64, d map[string]any) {
		if id == 0 || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, d)
	}
	now := q.s.clock()
	switch kind {
	case KindArtist:
		for _, it := range items {
			a := decode[Artist](it)
			add(a.ID, q.artistDTO(a, st))
		}
		if charts {
			as, _ := memoized(q.s.charts, "artists", now, func() ([]Artist, error) { return ch.ChartArtists(ctx, 100) })
			for _, a := range as {
				add(a.ID, q.artistDTO(a, st))
			}
		}
	case KindTrack:
		for _, it := range items {
			t := decode[Track](it)
			add(t.ID, q.trackDTO(t, st))
		}
		if charts {
			ts, _ := memoized(q.s.charts, "tracks", now, func() ([]Track, error) { return ch.ChartTracks(ctx, 100) })
			for _, t := range ts {
				add(t.ID, q.trackDTO(t, st))
			}
		}
	default:
		for _, it := range items {
			a := decode[Album](it)
			add(a.ID, q.albumDTO(a, st))
		}
		if charts {
			as, _ := memoized(q.s.charts, "albums", now, func() ([]Album, error) { return ch.ChartAlbums(ctx, 100) })
			for _, a := range as {
				add(a.ID, q.albumDTO(a, st))
			}
		}
	}
	if o.Order == "name" {
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].(map[string]any)["SortName"].(string) < out[j].(map[string]any)["SortName"].(string)
		})
		if strings.HasPrefix(strings.ToLower(q.get("SortOrder")), "desc") {
			for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// latest répond à /Items/Latest : derniers albums ajoutés, puis classement.
func (q *jfReq) latest() {
	ctx := q.r.Context()
	st := q.state(ctx)
	items, _ := q.s.o.Library.List(ctx, q.viewer, KindAlbum, ListOptions{Order: "added", Limit: 50})
	seen := map[int64]bool{}
	out := []any{}
	for _, it := range items {
		a := decode[Album](it)
		if a.ID != 0 && !seen[a.ID] {
			seen[a.ID] = true
			out = append(out, q.albumDTO(a, st))
		}
	}
	if ch, ok := q.s.o.Source.(Charter); ok {
		as, _ := memoized(q.s.charts, "albums", q.s.clock(), func() ([]Album, error) { return ch.ChartAlbums(ctx, 100) })
		for _, a := range as {
			if !seen[a.ID] {
				seen[a.ID] = true
				out = append(out, q.albumDTO(a, st))
			}
		}
	}
	if n := q.num("Limit", 20); n > 0 && len(out) > n {
		out = out[:n]
	}
	q.reply(out)
}

// artists répond à /Artists et /Artists/AlbumArtists.
func (q *jfReq) artists() {
	ctx := q.r.Context()
	st := q.state(ctx)
	var out []any
	if term := strings.TrimSpace(q.get("searchTerm")); term != "" {
		out = q.search(ctx, term, []string{"MusicArtist"}, st)
	} else {
		out = q.library(ctx, []string{"MusicArtist"}, st)
	}
	q.page(out)
}

// image relaie /Items/{id}/Images/Primary. L'étiquette (tag) porte la
// référence de l'image ; sans elle, l'objet est relu.
func (q *jfReq) image(kind int, id int64) {
	size := 0
	for _, k := range []string{"maxWidth", "fillWidth", "width", "maxHeight", "fillHeight", "height"} {
		if n := q.num(k, 0); n > 0 {
			size = n
			break
		}
	}
	if tag := q.get("tag"); len(tag) > 1 && (tag[0] == 'c' || tag[0] == 'p') {
		q.s.relayImage(q.w, q.r, tag[0], tag[1:], size)
		return
	}
	ctx := q.r.Context()
	switch kind {
	case guidGenre:
		_, albums, _ := q.s.GenreCharts(ctx, id)
		if len(albums) == 0 || albums[0].Cover == "" {
			http.NotFound(q.w, q.r)
			return
		}
		q.s.relayImage(q.w, q.r, 'c', albums[0].Cover, size)
	case guidPlaylist:
		pl, ok := q.s.playlist(ctx, q.viewer, id)
		if !ok || pl.Cover() == "" {
			http.NotFound(q.w, q.r)
			return
		}
		q.s.relayImage(q.w, q.r, 'c', pl.Cover(), size)
	case guidArtist:
		a, _, err := q.artist(ctx, id)
		if err != nil || a.Picture == "" {
			http.NotFound(q.w, q.r)
			return
		}
		q.s.relayImage(q.w, q.r, 'p', a.Picture, size)
	case guidAlbum:
		a, _, err := q.album(ctx, id)
		if err != nil || a.Cover == "" {
			http.NotFound(q.w, q.r)
			return
		}
		q.s.relayImage(q.w, q.r, 'c', a.Cover, size)
	default:
		t, err := q.s.o.Source.Track(ctx, id)
		if err != nil || t.Album.Cover == "" {
			http.NotFound(q.w, q.r)
			return
		}
		q.s.relayImage(q.w, q.r, 'c', t.Album.Cover, size)
	}
}

// report traite /Sessions/Playing[/Progress|/Stopped] pour un titre musical.
// Un arrêt après au moins 30 secondes compte comme une écoute.
func (q *jfReq) report(stopped bool) bool {
	orig := q.r.Body
	raw, err := io.ReadAll(io.LimitReader(orig, 256<<10))
	q.r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(raw), orig), orig}
	if err != nil {
		return false
	}
	var body struct {
		ItemId        string `json:"ItemId"`
		PositionTicks int64  `json:"PositionTicks"`
		IsPaused      bool   `json:"IsPaused"`
	}
	_ = json.Unmarshal(raw, &body)
	kind, id, ok := ParseJellyfinGUID(body.ItemId)
	if !ok {
		return false
	}
	if kind == guidTrack {
		state := "playing"
		if body.IsPaused {
			state = "paused"
		}
		if stopped {
			state = "stopped"
		}
		protocol := q.j.Protocol
		if protocol == "" {
			protocol = "jellyfin"
		}
		ms := body.PositionTicks / 10_000
		q.s.observeMusic(q.r, q.viewer, protocol, id, state, &ms, "")
	}
	if stopped && kind == guidTrack && body.PositionTicks >= 30*10_000_000 {
		ctx := q.r.Context()
		if t, err := q.track(ctx, id); err == nil {
			_ = q.s.o.Library.Played(ctx, q.viewer, t)
		}
	}
	q.w.WriteHeader(http.StatusNoContent)
	return true
}
