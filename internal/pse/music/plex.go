package music

import (
	"context"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plex/model"
)

// Bibliothèque « Musique » de PSE pour les clients Plex (Plexamp, Plex Web,
// applications Plex) — voir docs/pse/FORK.fr.md. Même catalogue et même
// bibliothèque personnelle que l'API Subsonic et les clients Jellyfin. Un
// middleware placé après l'authentification d'Ether ne prend que les
// requêtes qui visent la musique ; tout le reste suit le routeur d'Ether.

const (
	pseMusicSection = frontend.PSEMusicSection
	pseMusicTitle   = "Musique"
	pseMusicUUID    = "50534d55-0000-4000-8000-000000005053"

	// Les ratingKey musique utilisent des genres absents d'Ether (1 à 9) :
	// genre × 10^15 + identifiant Deezer.
	psemArtist = 50
	psemAlbum  = 51
	psemTrack  = 52
	psemBase   = 1_000_000_000_000_000
)

func psemKey(kind int, id int64) string { return strconv.FormatInt(int64(kind)*psemBase+id, 10) }

func psemParse(v string) (kind int, id int64, ok bool) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, 0, false
	}
	kind, id = int(n/psemBase), n%psemBase
	if kind < psemArtist || kind > psemPlaylist || id == 0 {
		return 0, 0, false
	}
	return kind, id, true
}

func psemLibKind(kind int) string {
	switch kind {
	case psemArtist:
		return KindArtist
	case psemAlbum:
		return KindAlbum
	}
	return KindTrack
}

var (
	psemMetaRe  = regexp.MustCompile(`/library/metadata/([0-9,]+)`)
	psemKeyRe   = regexp.MustCompile(`(?:^|[^0-9])5[0-3][0-9]{15}(?:[^0-9]|$)`)
	psemQueueRe = regexp.MustCompile(`^/playQueues/7[0-9]{9}(?:/|$)`)
)

// psemReq porte une requête musique.
type psemReq struct {
	m      *Server
	p      frontend.PSEMusicPlex
	w      http.ResponseWriter
	r      *http.Request
	viewer int64
	st     map[string]Item
}

func (q *psemReq) ctx() context.Context { return q.r.Context() }

func (q *psemReq) state() map[string]Item {
	if q.st != nil {
		return q.st
	}
	q.st = map[string]Item{}
	lib := q.m.LibraryOf()
	for _, k := range []string{KindArtist, KindAlbum, KindTrack} {
		items, _ := lib.List(q.ctx(), q.viewer, k, ListOptions{})
		for _, it := range items {
			q.st[k+":"+strconv.FormatInt(it.ID, 10)] = it
		}
	}
	return q.st
}

func (q *psemReq) userState(md *model.Metadata, kind string, id int64) {
	it := q.state()[kind+":"+strconv.FormatInt(id, 10)]
	if it.StarredAt > 0 {
		md.UserRating = 10
	}
	md.ViewCount = it.PlayCount
	md.LastViewedAt = it.PlayedAt
	if it.AddedAt > 0 {
		md.AddedAt = it.AddedAt
	}
}

func psemThumb(rk string, kind byte, ref string) string {
	if ref == "" {
		return ""
	}
	return "/library/metadata/" + rk + "/thumb/" + string(kind) + ref
}

func (q *psemReq) section(md *model.Metadata) {
	md.LibrarySectionID = pseMusicSection
	md.LibrarySectionTitle = pseMusicTitle
	md.LibrarySectionKey = "/library/sections/" + strconv.Itoa(pseMusicSection)
	md.LibrarySectionUUID = pseMusicUUID
}

func (q *psemReq) artistMD(a Artist) model.Metadata {
	rk := psemKey(psemArtist, a.ID)
	md := model.Metadata{RatingKey: rk, Key: "/library/metadata/" + rk + "/children", GUID: "deezer://artist/" + strconv.FormatInt(a.ID, 10),
		Type: "artist", Title: a.Name, TitleSort: strings.ToLower(strings.TrimSpace(a.Name)), Thumb: psemThumb(rk, 'p', a.Picture)}
	md.SetElement("Directory")
	q.section(&md)
	q.userState(&md, KindArtist, a.ID)
	return md
}

func (q *psemReq) albumMD(a Album) model.Metadata {
	rk := psemKey(psemAlbum, a.ID)
	md := model.Metadata{RatingKey: rk, Key: "/library/metadata/" + rk + "/children", GUID: "deezer://album/" + strconv.FormatInt(a.ID, 10),
		Type: "album", Title: a.Title, TitleSort: strings.ToLower(strings.TrimSpace(a.Title)), Thumb: psemThumb(rk, 'c', a.Cover), Year: a.Year()}
	if a.Artist.ID != 0 {
		ark := psemKey(psemArtist, a.Artist.ID)
		md.ParentRatingKey, md.ParentKey, md.ParentTitle = ark, "/library/metadata/"+ark, a.Artist.Name
		md.ParentThumb = psemThumb(ark, 'p', a.Artist.Picture)
	}
	if len(a.ReleaseDate) == 10 {
		md.OriginallyAvailableAt = a.ReleaseDate
	}
	if a.Tracks > 0 {
		md.LeafCount = model.Ptr(a.Tracks)
	}
	md.SetElement("Directory")
	q.section(&md)
	q.userState(&md, KindAlbum, a.ID)
	return md
}

func (q *psemReq) trackMD(t Track) model.Metadata {
	rk := psemKey(psemTrack, t.ID)
	_, suffix := q.m.Container()
	codec := suffix
	if suffix == "m4a" {
		codec = "aac"
	}
	dur := int64(t.Duration) * 1000
	md := model.Metadata{RatingKey: rk, Key: "/library/metadata/" + rk, GUID: "deezer://track/" + strconv.FormatInt(t.ID, 10),
		Type: "track", Title: t.Title, TitleSort: strings.ToLower(strings.TrimSpace(t.Title)), Duration: dur}
	if t.Number > 0 {
		md.Index = model.Ptr(t.Number)
	}
	disc := t.Volume
	if disc <= 0 {
		disc = 1
	}
	md.ParentIndex = model.Ptr(disc)
	if t.Album.ID != 0 {
		ark := psemKey(psemAlbum, t.Album.ID)
		md.ParentRatingKey, md.ParentKey, md.ParentTitle = ark, "/library/metadata/"+ark, t.Album.Title
		md.ParentThumb = psemThumb(ark, 'c', t.Album.Cover)
		md.Thumb = md.ParentThumb
		md.ParentYear = t.Album.Year()
	}
	artist := t.Album.Artist
	if artist.ID == 0 {
		artist = t.Artist
	}
	if artist.ID != 0 {
		grk := psemKey(psemArtist, artist.ID)
		md.GrandparentRatingKey, md.GrandparentKey, md.GrandparentTitle = grk, "/library/metadata/"+grk, artist.Name
		md.GrandparentThumb = psemThumb(grk, 'p', artist.Picture)
	}
	if t.Artist.ID != 0 && t.Artist.ID != artist.ID {
		md.OriginalTitle = t.Artist.Name
	}
	pid := psemBase*int64(psemTrack) + t.ID
	md.Media = []model.Media{{ID: pid, Duration: dur, AudioChannels: 2, AudioCodec: codec, Container: suffix,
		Part: []model.Part{{ID: pid, Key: "/library/parts/" + rk + "/0/file." + suffix, Duration: dur, Container: suffix,
			File:   "/ether/musique/" + strconv.FormatInt(t.ID, 10) + "." + suffix,
			Stream: []model.Stream{{ID: pid, StreamType: 2, Selected: model.Ptr(true), Default: model.Ptr(true), Codec: codec, Index: model.Ptr(0), Channels: 2, DisplayTitle: strings.ToUpper(codec)}}}}}}
	md.SetElement("Track")
	q.section(&md)
	q.userState(&md, KindTrack, t.ID)
	return md
}

func (q *psemReq) container(items []model.Metadata, title string) *model.MediaContainer {
	mc := psemContainer()
	mc.LibrarySectionID = model.Ptr(pseMusicSection)
	mc.LibrarySectionTitle = pseMusicTitle
	mc.LibrarySectionUUID = pseMusicUUID
	mc.Title1 = pseMusicTitle
	mc.Title2 = title
	total := len(items)
	start, end := 0, total
	if q.p.HasPage {
		start = min(max(q.p.Start, 0), total)
		if q.p.Size >= 0 {
			end = min(start+q.p.Size, total)
		}
		mc.Offset = model.Ptr(start)
		mc.TotalSize = model.Ptr(total)
	}
	mc.Metadata = items[start:end]
	mc.Size = len(mc.Metadata)
	return mc
}

func (q *psemReq) empty() { q.p.Write(q.w, q.r, &model.MediaContainer{}) }

// --- intercepteur ---

// ServePlex répond aux requêtes Plex qui concernent la musique et renvoie
// false pour toutes les autres, qu'Ether traite normalement.
func (s *Server) ServePlex(w http.ResponseWriter, r *http.Request, viewer int64, p frontend.PSEMusicPlex) bool {
	if viewer == 0 || !psemCandidate(r) {
		return false
	}
	q := &psemReq{m: s, p: p, w: w, r: r, viewer: viewer}
	return q.serve()
}

// PlexSearch fournit la musique à la recherche globale de Plex.
func (s *Server) PlexSearch(ctx context.Context, viewer int64, query string, limit int) (artists, albums, tracks []any) {
	query = strings.TrimSpace(query)
	if viewer == 0 || query == "" {
		return nil, nil, nil
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	q := &psemReq{m: s, r: r, viewer: viewer}
	ar, al, tr := s.Search(ctx, query, limit)
	for _, a := range ar {
		artists = append(artists, q.artistMD(a))
	}
	for _, a := range al {
		albums = append(albums, q.albumMD(a))
	}
	for _, t := range tr {
		tracks = append(tracks, q.trackMD(t))
	}
	return artists, albums, tracks
}

// psemCandidate écarte vite ce qui ne peut pas concerner la musique.
func psemCandidate(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/music/"), strings.HasPrefix(p, "/library/sections/"+strconv.Itoa(pseMusicSection)),
		strings.HasPrefix(p, "/hubs/sections/"+strconv.Itoa(pseMusicSection)), p == "/hubs/search":
		return true
	case strings.HasPrefix(p, "/playQueues/"):
		return psemQueueRe.MatchString(p)
	case p == "/playlists" || p == "/playlists/all":
		return strings.Contains(r.URL.Query().Get("playlistType"), "audio")
	case strings.HasPrefix(p, "/playlists/"):
		return psemKeyRe.MatchString(p)
	case strings.HasPrefix(p, "/hubs/metadata/"):
		return psemKeyRe.MatchString(p)
	case strings.HasPrefix(p, "/library/metadata/"), strings.HasPrefix(p, "/library/parts/"), p == "/photo/:/transcode",
		p == "/playQueues", p == "/:/timeline", p == "/:/scrobble", p == "/:/unscrobble", p == "/:/rate":
		return psemKeyRe.MatchString(p + "?" + r.URL.RawQuery)
	}
	return false
}

func (q *psemReq) serve() bool {
	p := q.r.URL.Path
	v := q.r.URL.Query()
	sec := "/library/sections/" + strconv.Itoa(pseMusicSection)
	switch {
	case p == sec || p == sec+"/":
		q.sectionRoot()
		return true
	case strings.HasPrefix(p, sec+"/"):
		q.sectionSub(strings.Trim(strings.TrimPrefix(p, sec+"/"), "/"))
		return true
	case strings.HasPrefix(p, "/hubs/sections/"+strconv.Itoa(pseMusicSection)):
		q.sectionHubs()
		return true
	case p == "/hubs/search":
		// Recherche dans la section Musique, ou limitée à la musique ; la
		// recherche globale reste à Ether, qui y ajoute la musique.
		if v.Get("sectionId") != strconv.Itoa(pseMusicSection) && strings.TrimSpace(v.Get("searchTypes")) != "music" {
			return false
		}
		q.searchHubs(v.Get("query"))
		return true
	case strings.HasPrefix(p, "/hubs/metadata/"):
		return q.relatedHubs(strings.Split(strings.TrimPrefix(p, "/hubs/metadata/"), "/")[0])
	case strings.HasPrefix(p, "/library/metadata/"):
		return q.metadata(strings.Split(strings.TrimPrefix(p, "/library/metadata/"), "/"))
	case strings.HasPrefix(p, "/library/parts/"):
		parts := strings.Split(strings.TrimPrefix(p, "/library/parts/"), "/")
		kind, id, ok := psemParse(parts[0])
		if !ok || kind != psemTrack {
			return false
		}
		q.audio(id)
		return true
	case p == "/photo/:/transcode":
		return q.photo(v)
	case strings.HasPrefix(p, "/music/:/transcode/universal/"):
		return q.transcode(strings.TrimPrefix(p, "/music/:/transcode/universal/"), v)
	case strings.HasPrefix(p, "/playlists"):
		return q.playlistsRoute(p, v)
	case strings.HasPrefix(p, "/playQueues"):
		return q.playQueue(strings.Trim(strings.TrimPrefix(p, "/playQueues"), "/"), v)
	case p == "/:/timeline", p == "/:/scrobble", p == "/:/unscrobble", p == "/:/rate":
		return q.state0(p, v)
	}
	return false
}

// --- section ---

func (q *psemReq) sectionRoot() {
	mc := psemContainer()
	mc.LibrarySectionID = model.Ptr(pseMusicSection)
	mc.Title1 = pseMusicTitle
	mc.ViewMode = 65592 // liste
	mc.Content = "secondary"
	mc.ViewGroup = "secondary"
	for _, d := range []struct{ key, title string }{
		{"all", "Tous les artistes"}, {"albums", "Albums"}, {"genre", "Par style"}, {"recentlyAdded", "Ajoutés récemment"}, {"recentlyViewed", "Écoutés récemment"},
	} {
		mc.Directory = append(mc.Directory, model.Directory{Key: d.key, Title: d.title})
	}
	mc.Directory = append(mc.Directory, model.Directory{Key: "search?type=8", Prompt: "Rechercher", Search: model.Ptr(true), Title: "Rechercher…"})
	mc.Size = len(mc.Directory)
	q.p.Write(q.w, q.r, mc)
}

func (q *psemReq) sectionSub(sub string) {
	v := q.r.URL.Query()
	typ := v.Get("type")
	term := strings.TrimSpace(v.Get("query"))
	if term == "" {
		term = strings.TrimSpace(v.Get("title"))
	}
	if sub == "genre" {
		q.genres()
		return
	}
	if g := v.Get("genre"); g != "" && (sub == "all" || sub == "albums") {
		q.genreItems(g, typ)
		return
	}
	switch sub {
	case "all", "search", "albums", "recentlyAdded", "recentlyViewed", "newest":
	default:
		q.empty()
		return
	}
	if sub == "albums" && typ == "" {
		typ = "9"
	}
	if (sub == "recentlyAdded" || sub == "recentlyViewed" || sub == "newest") && typ == "" {
		typ = "9"
	}
	if typ == "" {
		typ = "8"
	}
	if term != "" {
		q.p.Write(q.w, q.r, q.container(q.search(term, typ), "Recherche"))
		return
	}
	order := "name"
	played := false
	sortKey := v.Get("sort")
	switch {
	case sub == "recentlyAdded" || sub == "newest" || strings.HasPrefix(sortKey, "addedAt"):
		order = "added"
	case sub == "recentlyViewed" || strings.HasPrefix(sortKey, "lastViewedAt"):
		order, played = "played", true
	case strings.HasPrefix(sortKey, "viewCount"):
		order, played = "frequent", true
	case strings.HasPrefix(sortKey, "random"):
		order = "random"
	}
	starred := v.Get("userRating>>") != "" || v.Get("track.userRating>>") != "" || v.Get("album.userRating>>") != "" || v.Get("artist.userRating>>") != ""
	q.p.Write(q.w, q.r, q.container(q.library(typ, ListOptions{Order: order, Played: played, Starred: starred}, !played && !starred), ""))
}

// library liste les favoris et écoutes, puis le classement.
func (q *psemReq) library(typ string, o ListOptions, charts bool) []model.Metadata {
	lib := q.m.LibraryOf()
	var chArtists []Artist
	var chAlbums []Album
	var chTracks []Track
	if charts {
		chArtists, chAlbums, chTracks = q.m.Charts(q.ctx())
	}
	seen := map[int64]bool{}
	var out []model.Metadata
	switch typ {
	case "10":
		items, _ := lib.List(q.ctx(), q.viewer, KindTrack, o)
		for _, it := range items {
			if t := DecodeTrack(it); !seen[t.ID] && t.ID != 0 {
				seen[t.ID] = true
				out = append(out, q.trackMD(t))
			}
		}
		for _, t := range chTracks {
			if !seen[t.ID] {
				seen[t.ID] = true
				out = append(out, q.trackMD(t))
			}
		}
	case "9":
		items, _ := lib.List(q.ctx(), q.viewer, KindAlbum, o)
		for _, it := range items {
			if a := DecodeAlbum(it); !seen[a.ID] && a.ID != 0 {
				seen[a.ID] = true
				out = append(out, q.albumMD(a))
			}
		}
		for _, a := range chAlbums {
			if !seen[a.ID] {
				seen[a.ID] = true
				out = append(out, q.albumMD(a))
			}
		}
	default:
		items, _ := lib.List(q.ctx(), q.viewer, KindArtist, o)
		for _, it := range items {
			if a := DecodeArtist(it); !seen[a.ID] && a.ID != 0 {
				seen[a.ID] = true
				out = append(out, q.artistMD(a))
			}
		}
		for _, a := range chArtists {
			if !seen[a.ID] {
				seen[a.ID] = true
				out = append(out, q.artistMD(a))
			}
		}
	}
	if o.Order == "name" {
		sort.SliceStable(out, func(i, j int) bool { return out[i].TitleSort < out[j].TitleSort })
		if strings.HasSuffix(q.r.URL.Query().Get("sort"), ":desc") {
			for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (q *psemReq) search(term, typ string) []model.Metadata {
	ar, al, tr := q.m.Search(q.ctx(), term, 30)
	var out []model.Metadata
	if typ == "" || typ == "8" {
		for _, a := range ar {
			out = append(out, q.artistMD(a))
		}
	}
	if typ == "" || typ == "9" {
		for _, a := range al {
			out = append(out, q.albumMD(a))
		}
	}
	if typ == "" || typ == "10" {
		for _, t := range tr {
			out = append(out, q.trackMD(t))
		}
	}
	return out
}

func psemHub(id, title, typ string, items []model.Metadata) model.Hub {
	if len(items) > 30 {
		items = items[:30]
	}
	return model.Hub{HubKey: "/hubs/sections/" + strconv.Itoa(pseMusicSection) + "/" + id, Title: title, Type: typ,
		HubIdentifier: "music." + id, Context: "hub.music." + id, Size: len(items), More: false, Metadata: items}
}

func (q *psemReq) sectionHubs() {
	mc := psemContainer()
	mc.LibrarySectionID = model.Ptr(pseMusicSection)
	mc.LibrarySectionTitle = pseMusicTitle
	mc.LibrarySectionUUID = pseMusicUUID
	if played := q.library("9", ListOptions{Order: "played", Played: true}, false); len(played) > 0 {
		mc.Hub = append(mc.Hub, psemHub("recent", "Écoutés récemment", "album", played))
	}
	if fav := q.library("9", ListOptions{Order: "starred", Starred: true}, false); len(fav) > 0 {
		mc.Hub = append(mc.Hub, psemHub("favorites", "Albums favoris", "album", fav))
	}
	mc.Hub = append(mc.Hub, q.playlistHubs()...)
	ar, al, tr := q.m.Charts(q.ctx())
	var albums, artists, tracks []model.Metadata
	for _, a := range al {
		albums = append(albums, q.albumMD(a))
	}
	for _, a := range ar {
		artists = append(artists, q.artistMD(a))
	}
	for _, t := range tr {
		tracks = append(tracks, q.trackMD(t))
	}
	mc.Hub = append(mc.Hub, psemHub("charts.albums", "Albums populaires", "album", albums),
		psemHub("charts.artists", "Artistes populaires", "artist", artists),
		psemHub("charts.tracks", "Titres populaires", "track", tracks))
	mc.Size = len(mc.Hub)
	q.p.Write(q.w, q.r, mc)
}

func (q *psemReq) searchHubs(term string) {
	mc := &model.MediaContainer{}
	if term = strings.TrimSpace(term); term != "" {
		ar, al, tr := q.m.Search(q.ctx(), term, 20)
		var artists, albums, tracks []model.Metadata
		for _, a := range ar {
			artists = append(artists, q.artistMD(a))
		}
		for _, a := range al {
			albums = append(albums, q.albumMD(a))
		}
		for _, t := range tr {
			tracks = append(tracks, q.trackMD(t))
		}
		mc.Hub = []model.Hub{psemHub("search.artist", "Artistes", "artist", artists), psemHub("search.album", "Albums", "album", albums), psemHub("search.track", "Titres", "track", tracks)}
	}
	mc.Size = len(mc.Hub)
	q.p.Write(q.w, q.r, mc)
}

// --- objets ---

func (q *psemReq) item(kind int, id int64) (model.Metadata, error) {
	ctx := q.ctx()
	switch kind {
	case psemPlaylist:
		pl, ok := q.m.playlist(ctx, q.viewer, id)
		if !ok {
			return model.Metadata{}, ErrNotFound
		}
		return q.playlistMD(pl), nil
	case psemArtist:
		a, albums, err := q.m.ArtistAlbums(ctx, id)
		if err != nil {
			return model.Metadata{}, err
		}
		md := q.artistMD(a)
		md.ChildCount = model.Ptr(len(albums))
		return md, nil
	case psemAlbum:
		a, tracks, err := q.m.AlbumTracks(ctx, id)
		if err != nil {
			return model.Metadata{}, err
		}
		if a.Tracks == 0 {
			a.Tracks = len(tracks)
		}
		return q.albumMD(a), nil
	}
	t, err := q.m.TrackFor(ctx, q.viewer, id)
	if err != nil {
		return model.Metadata{}, err
	}
	return q.trackMD(t), nil
}

func (q *psemReq) tracksOf(kind int, id int64) []Track {
	switch kind {
	case psemPlaylist:
		pl, _ := q.m.playlist(q.ctx(), q.viewer, id)
		return pl.Tracks
	case psemAlbum:
		_, tracks, _ := q.m.AlbumTracks(q.ctx(), id)
		return tracks
	case psemArtist:
		return q.m.ArtistTopTracks(q.ctx(), id)
	}
	if t, err := q.m.TrackFor(q.ctx(), q.viewer, id); err == nil {
		return []Track{t}
	}
	return nil
}

func (q *psemReq) metadata(segs []string) bool {
	keys := strings.Split(segs[0], ",")
	type ref struct {
		kind int
		id   int64
	}
	refs := make([]ref, 0, len(keys))
	for _, k := range keys {
		kind, id, ok := psemParse(k)
		if !ok {
			return false
		}
		refs = append(refs, ref{kind, id})
	}
	sub := ""
	if len(segs) > 1 {
		sub = segs[1]
	}
	first := refs[0]
	switch sub {
	case "":
		var out []model.Metadata
		for _, rf := range refs {
			md, err := q.item(rf.kind, rf.id)
			if err != nil {
				continue
			}
			out = append(out, md)
		}
		if len(out) == 0 {
			http.Error(q.w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return true
		}
		q.p.Write(q.w, q.r, q.container(out, ""))
	case "children":
		var out []model.Metadata
		mc := (*model.MediaContainer)(nil)
		switch first.kind {
		case psemArtist:
			a, albums, err := q.m.ArtistAlbums(q.ctx(), first.id)
			if err == nil {
				for _, al := range albums {
					out = append(out, q.albumMD(al))
				}
			}
			mc = q.container(out, a.Name)
			mc.ParentTitle = a.Name
			mc.ParentRatingKey = psemKey(psemArtist, first.id)
		case psemAlbum:
			a, tracks, err := q.m.AlbumTracks(q.ctx(), first.id)
			if err == nil {
				for _, t := range tracks {
					out = append(out, q.trackMD(t))
				}
			}
			mc = q.container(out, a.Title)
			mc.ParentTitle = a.Title
			mc.ParentRatingKey = psemKey(psemAlbum, first.id)
			mc.ParentYear = a.Year()
			mc.GrandparentTitle = a.Artist.Name
			if a.Cover != "" {
				mc.Thumb = psemThumb(psemKey(psemAlbum, first.id), 'c', a.Cover)
			}
		default:
			mc = q.container(nil, "")
		}
		q.p.Write(q.w, q.r, mc)
	case "allLeaves", "grandchildren":
		var out []model.Metadata
		for _, t := range q.tracksOf(first.kind, first.id) {
			out = append(out, q.trackMD(t))
		}
		q.p.Write(q.w, q.r, q.container(out, ""))
	case "thumb", "art", "poster", "banner":
		tag := ""
		if len(segs) > 2 {
			tag = segs[2]
		}
		q.image(first.kind, first.id, tag, 0)
	default:
		q.empty()
	}
	return true
}

func (q *psemReq) image(kind int, id int64, tag string, size int) {
	if len(tag) > 1 && (tag[0] == 'c' || tag[0] == 'p') {
		q.m.RelayImage(q.w, q.r, tag[0], tag[1:], size)
		return
	}
	switch kind {
	case psemPlaylist:
		if pl, ok := q.m.playlist(q.ctx(), q.viewer, id); ok && pl.Cover() != "" {
			q.m.RelayImage(q.w, q.r, 'c', pl.Cover(), size)
			return
		}
	case psemArtist:
		if a, _, err := q.m.ArtistAlbums(q.ctx(), id); err == nil && a.Picture != "" {
			q.m.RelayImage(q.w, q.r, 'p', a.Picture, size)
			return
		}
	case psemAlbum:
		if a, _, err := q.m.AlbumTracks(q.ctx(), id); err == nil && a.Cover != "" {
			q.m.RelayImage(q.w, q.r, 'c', a.Cover, size)
			return
		}
	default:
		if t, err := q.m.TrackFor(q.ctx(), q.viewer, id); err == nil && t.Album.Cover != "" {
			q.m.RelayImage(q.w, q.r, 'c', t.Album.Cover, size)
			return
		}
	}
	http.Error(q.w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
}

// photo traite /photo/:/transcode?url=/library/metadata/{rk}/thumb/{tag}.
func (q *psemReq) photo(v url.Values) bool {
	raw := v.Get("url")
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		raw = u.Path
	}
	if m := psemPlaylistRe.FindStringSubmatch(raw); m != nil && strings.HasPrefix(raw, "/playlists/") {
		kind, id, ok := psemParse(m[1])
		if !ok || kind != psemPlaylist {
			return false
		}
		size, _ := strconv.Atoi(v.Get("width"))
		q.image(kind, id, "", size)
		return true
	}
	if !strings.HasPrefix(raw, "/library/metadata/") {
		return false
	}
	segs := strings.Split(strings.TrimPrefix(raw, "/library/metadata/"), "/")
	kind, id, ok := psemParse(segs[0])
	if !ok {
		return false
	}
	tag := ""
	if len(segs) > 2 {
		tag = segs[2]
	}
	size, _ := strconv.Atoi(v.Get("width"))
	q.image(kind, id, tag, size)
	return true
}

func (q *psemReq) audio(id int64) {
	if err := q.m.RelayAudio(q.w, q.r, id); err != nil {
		http.Error(q.w, http.StatusText(http.StatusBadGateway), http.StatusBadGateway)
	}
}

// transcode répond à la décision de lecture (lecture directe) et relaie le
// flux d'origine pour /start.
func (q *psemReq) transcode(sub string, v url.Values) bool {
	m := psemMetaRe.FindStringSubmatch(v.Get("path"))
	if m == nil {
		return false
	}
	kind, id, ok := psemParse(strings.Split(m[1], ",")[0])
	if !ok || kind != psemTrack {
		return false
	}
	if strings.HasPrefix(sub, "start") {
		q.audio(id)
		return true
	}
	if strings.HasPrefix(sub, "decision") {
		md, err := q.item(kind, id)
		if err != nil {
			http.Error(q.w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return true
		}
		for i := range md.Media {
			md.Media[i].Selected = model.Ptr(true)
			for j := range md.Media[i].Part {
				md.Media[i].Part[j].Decision = "directplay"
				md.Media[i].Part[j].Selected = model.Ptr(true)
			}
		}
		mc := psemContainer()
		mc.Extra = []model.Attr{
			model.A("directPlayDecisionCode", 1000), model.A("directPlayDecisionText", "Direct play OK."),
			model.A("generalDecisionCode", 1000), model.A("generalDecisionText", "Direct play OK."),
		}
		mc.Metadata = []model.Metadata{md}
		mc.Size = 1
		q.p.Write(q.w, q.r, mc)
		return true
	}
	q.empty()
	return true
}

// --- état ---

func (q *psemReq) state0(p string, v url.Values) bool {
	key := v.Get("ratingKey")
	if key == "" {
		key = v.Get("key")
	}
	if m := psemMetaRe.FindStringSubmatch(key); m != nil {
		key = m[1]
	}
	kind, id, ok := psemParse(key)
	if !ok {
		return false
	}
	ctx := q.ctx()
	switch p {
	case "/:/timeline":
		if v.Get("state") == "stopped" && kind == psemTrack {
			if ms, _ := strconv.ParseInt(v.Get("time"), 10, 64); ms >= 30_000 {
				q.m.Played(ctx, q.viewer, id)
			}
		}
	case "/:/scrobble":
		if kind == psemTrack {
			q.m.Played(ctx, q.viewer, id)
		}
	case "/:/rate":
		rating, _ := strconv.ParseFloat(v.Get("rating"), 64)
		_ = q.m.Star(ctx, q.viewer, psemLibKind(kind), id, rating > 0)
	}
	q.empty()
	return true
}

// --- files de lecture ---

type psemQueue struct {
	id       int64
	owner    int64
	tracks   []Track
	selected int
	shuffled bool
	uri      string
	version  int
}

var psemQueues = struct {
	sync.Mutex
	next  int64
	byID  map[int64]*psemQueue
	order []int64
}{next: 7_000_000_000, byID: map[int64]*psemQueue{}}

func psemStore(pq *psemQueue) {
	psemQueues.Lock()
	defer psemQueues.Unlock()
	psemQueues.next++
	pq.id = psemQueues.next
	psemQueues.byID[pq.id] = pq
	psemQueues.order = append(psemQueues.order, pq.id)
	if len(psemQueues.order) > 200 {
		delete(psemQueues.byID, psemQueues.order[0])
		psemQueues.order = psemQueues.order[1:]
	}
}

func psemLoad(id int64, owner int64) (*psemQueue, bool) {
	psemQueues.Lock()
	defer psemQueues.Unlock()
	pq, ok := psemQueues.byID[id]
	return pq, ok && pq.owner == owner
}

// tracksForURI développe un uri Plex (album, artiste, titre, section).
func (q *psemReq) tracksForURI(uri string) ([]Track, bool) {
	if u, err := url.PathUnescape(uri); err == nil {
		uri = u
	}
	if strings.Contains(uri, "/library/sections/"+strconv.Itoa(pseMusicSection)) {
		var out []Track
		for _, md := range q.library("10", ListOptions{Order: "random"}, true) {
			_, id, _ := psemParse(md.RatingKey)
			if t, err := q.m.TrackFor(q.ctx(), q.viewer, id); err == nil {
				out = append(out, t)
			}
		}
		return out, true
	}
	m := psemMetaRe.FindStringSubmatch(uri)
	if m == nil {
		return nil, false
	}
	var out []Track
	for _, k := range strings.Split(m[1], ",") {
		kind, id, ok := psemParse(k)
		if !ok {
			return nil, false
		}
		out = append(out, q.tracksOf(kind, id)...)
	}
	return out, true
}

func (q *psemReq) playQueue(rest string, v url.Values) bool {
	if rest == "" {
		if q.r.Method != http.MethodPost {
			return false
		}
		tracks, ok := q.playlistTracksFor(v)
		if !ok {
			tracks, ok = q.tracksForURI(v.Get("uri"))
		}
		if !ok {
			return false
		}
		pq := &psemQueue{owner: q.viewer, tracks: tracks, uri: v.Get("uri"), version: 1}
		if m := psemMetaRe.FindStringSubmatch(v.Get("key")); m != nil {
			if _, id, ok := psemParse(m[1]); ok {
				for i, t := range tracks {
					if t.ID == id {
						pq.selected = i
						break
					}
				}
			}
		}
		if v.Get("shuffle") == "1" {
			psemShuffle(pq)
		}
		psemStore(pq)
		q.writeQueue(pq)
		return true
	}
	parts := strings.Split(rest, "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false
	}
	pq, ok := psemLoad(id, q.viewer)
	if !ok {
		return false
	}
	psemQueues.Lock()
	switch {
	case q.r.Method == http.MethodPut && len(parts) == 1:
		if more, ok := q.tracksForURI(v.Get("uri")); ok {
			if v.Get("next") == "1" {
				at := min(pq.selected+1, len(pq.tracks))
				pq.tracks = append(pq.tracks[:at], append(more, pq.tracks[at:]...)...)
			} else {
				pq.tracks = append(pq.tracks, more...)
			}
			pq.version++
		}
	case len(parts) == 2 && parts[1] == "shuffle":
		psemShuffle(pq)
		pq.version++
	case len(parts) == 2 && parts[1] == "unshuffle":
		pq.shuffled = false
		pq.version++
	case q.r.Method == http.MethodDelete && len(parts) == 3 && parts[1] == "items":
		if item, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
			i := int(item - pq.id*1000 - 1)
			if i >= 0 && i < len(pq.tracks) {
				pq.tracks = append(pq.tracks[:i], pq.tracks[i+1:]...)
				if pq.selected >= len(pq.tracks) {
					pq.selected = max(0, len(pq.tracks)-1)
				}
				pq.version++
			}
		}
	}
	if sel, err := strconv.ParseInt(v.Get("playQueueItemID"), 10, 64); err == nil {
		if i := int(sel - pq.id*1000 - 1); i >= 0 && i < len(pq.tracks) {
			pq.selected = i
		}
	}
	psemQueues.Unlock()
	q.writeQueue(pq)
	return true
}

func psemShuffle(pq *psemQueue) {
	if len(pq.tracks) < 2 {
		return
	}
	sel := pq.tracks[pq.selected]
	rest := append([]Track{}, pq.tracks[:pq.selected]...)
	rest = append(rest, pq.tracks[pq.selected+1:]...)
	rand.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	pq.tracks = append([]Track{sel}, rest...)
	pq.selected = 0
	pq.shuffled = true
}

func (q *psemReq) writeQueue(pq *psemQueue) {
	psemQueues.Lock()
	tracks := append([]Track{}, pq.tracks...)
	sel, id, shuffled, uri, version := pq.selected, pq.id, pq.shuffled, pq.uri, pq.version
	psemQueues.Unlock()
	mc := psemContainer()
	selItem, selKey := int64(0), ""
	if sel < len(tracks) {
		selItem, selKey = id*1000+int64(sel)+1, psemKey(psemTrack, tracks[sel].ID)
	}
	mc.Extra = []model.Attr{
		model.A("playQueueID", id),
		model.A("playQueueSelectedItemID", selItem),
		model.A("playQueueSelectedItemOffset", sel),
		model.A("playQueueSelectedMetadataItemID", selKey),
		model.A("playQueueShuffled", shuffled),
		model.A("playQueueSourceURI", uri),
		model.A("playQueueTotalCount", len(tracks)),
		model.A("playQueueVersion", version),
	}
	for i, t := range tracks {
		md := q.trackMD(t)
		md.PlayQueueItemID = id*1000 + int64(i) + 1
		mc.Metadata = append(mc.Metadata, md)
	}
	mc.Size = len(mc.Metadata)
	q.p.Write(q.w, q.r, mc)
}

func psemContainer() *model.MediaContainer {
	return &model.MediaContainer{AllowSync: model.Ptr(false), Identifier: model.LibraryIdentifier,
		MediaTagPrefix: model.MediaTagPrefix, MediaTagVersion: model.MediaTagVersion}
}
