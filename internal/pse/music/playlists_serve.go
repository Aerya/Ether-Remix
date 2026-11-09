package music

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/plex/model"
)

// Playlists importées servies aux clients Subsonic, Jellyfin et Plex, en
// lecture seule.

const guidPlaylist = 4

const psemPlaylist = 53

var psemPlaylistRe = regexp.MustCompile(`/playlists/(5[0-3][0-9]{15})`)

func playlistSubsonicID(id int64) string { return "pl-" + strconv.FormatInt(id, 10) }

// playlistsOf renvoie les playlists d'un spectateur (aucune sans stockage).
func (s *Server) playlistsOf(ctx context.Context, viewer int64) []Playlist {
	if viewer == 0 {
		return nil
	}
	var pls []Playlist
	if s.o.Playlists != nil {
		var err error
		if pls, err = s.o.Playlists.List(ctx, viewer); err != nil {
			s.o.Logger.Warn("playlists illisibles", "err", err)
		}
	}
	// Personnelles d'abord, puis les automatiques, puis les importées.
	n := 0
	for n < len(pls) && pls[n].Source == SourceUser {
		n++
	}
	smart := s.smartPlaylists(ctx, viewer)
	out := make([]Playlist, 0, len(pls)+len(smart))
	out = append(out, pls[:n]...)
	out = append(out, smart...)
	return append(out, pls[n:]...)
}

func (s *Server) playlist(ctx context.Context, viewer, id int64) (Playlist, bool) {
	if viewer == 0 {
		return Playlist{}, false
	}
	if s.o.Playlists != nil {
		if pl, ok, err := s.o.Playlists.Get(ctx, viewer, id); ok && err == nil {
			return pl, true
		}
	}
	return s.smartByID(ctx, viewer, id)
}

func playlistComment(pl Playlist) string {
	switch pl.Source {
	case SourceUser:
		return "Playlist personnelle"
	case SourceSmart:
		return smartComment(pl.Ref)
	case ServiceListenBrainz:
		return "Importée de ListenBrainz"
	case ServiceLastFM:
		return "Importée de Last.fm"
	case SourceCharts:
		return "Classement Deezer, mis à jour chaque jour"
	case SourceDiscover:
		return "Artistes proches de vos écoutes et de vos playlists"
	}
	return "Importée de Deezer"
}

// --- Subsonic ---

func (s *Server) playlistEl(c *call, pl Playlist) *el {
	e := node("playlist", "id", playlistSubsonicID(pl.ID), "name", pl.Name, "comment", playlistComment(pl),
		"owner", c.user, "public", false, "readonly", !pl.Editable(), "songCount", len(pl.Tracks), "duration", pl.Duration())
	e.set("created", iso(pl.SyncedAt)).set("changed", iso(pl.SyncedAt))
	e.set("coverArt", coverID(pl.Cover()))
	return e
}

func (s *Server) getPlaylists(c *call) {
	e := node("playlists")
	for _, pl := range s.playlistsOf(c.r.Context(), c.viewer) {
		e.item(s.playlistEl(c, pl))
	}
	c.ok(e)
}

func (s *Server) getPlaylist(c *call) {
	raw := c.param("id")
	id, err := strconv.ParseInt(strings.TrimPrefix(raw, "pl-"), 10, 64)
	if err != nil || !strings.HasPrefix(raw, "pl-") {
		c.fail(errNotFound, "Playlist introuvable")
		return
	}
	ctx := c.r.Context()
	pl, ok := s.playlist(ctx, c.viewer, id)
	if !ok {
		c.fail(errNotFound, "Playlist introuvable")
		return
	}
	st := map[int64]Item{}
	items, _ := s.o.Library.List(ctx, c.viewer, KindTrack, ListOptions{})
	for _, it := range items {
		st[it.ID] = it
	}
	e := s.playlistEl(c, pl)
	for _, t := range pl.Tracks {
		it := st[t.ID]
		e.item(s.songEl("entry", t, userState{it.StarredAt, it.PlayCount, it.PlayedAt}))
	}
	c.ok(e)
}

// maxAddPerCall borne les titres résolus par requête d'édition.
const maxAddPerCall = 500

// songsParam lit les titres songId d'une requête, dans l'ordre.
func (s *Server) songsParam(c *call) ([]Track, bool) {
	ids := c.params("songId")
	if len(ids) > maxAddPerCall {
		c.fail(errGeneric, "Trop de titres en une fois")
		return nil, false
	}
	out := make([]Track, 0, len(ids))
	for _, raw := range ids {
		kind, id, ok := parseID(raw)
		if !ok || kind != KindTrack {
			c.fail(errNotFound, "Titre inconnu")
			return nil, false
		}
		t, err := s.o.Source.Track(c.r.Context(), id)
		if err != nil {
			c.fail(errNotFound, "Titre introuvable")
			return nil, false
		}
		out = append(out, t)
	}
	return out, true
}

func playlistIDParam(raw string) (int64, bool) {
	if !strings.HasPrefix(raw, "pl-") {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(raw, "pl-"), 10, 64)
	return id, err == nil
}

func (s *Server) playlistFail(c *call, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		c.fail(errNotFound, "Playlist introuvable")
	case errors.Is(err, ErrReadOnly):
		c.fail(errNotAuthorized, "Playlist importée ou automatique : non modifiable. Créez une playlist personnelle.")
	default:
		c.fail(errGeneric, "Playlist indisponible")
	}
}

// createPlaylist crée une playlist personnelle, ou remplace les titres
// d'une playlist existante (playlistId) : c'est ainsi que les clients
// Subsonic la réordonnent.
func (s *Server) createPlaylist(c *call) {
	if s.o.Playlists == nil {
		c.fail(errNotAuthorized, "Playlists indisponibles")
		return
	}
	tracks, ok := s.songsParam(c)
	if !ok {
		return
	}
	ctx := c.r.Context()
	var pl Playlist
	var err error
	if raw := c.param("playlistId"); raw != "" {
		id, ok := playlistIDParam(raw)
		if !ok {
			c.fail(errNotFound, "Playlist introuvable")
			return
		}
		e := PlaylistEdit{Replace: tracks}
		if n := c.param("name"); n != "" {
			e.Name = &n
		}
		pl, err = s.o.Playlists.UpdatePersonal(ctx, c.viewer, id, e)
	} else {
		if strings.TrimSpace(c.param("name")) == "" {
			c.fail(errMissingParam, "Paramètre name requis")
			return
		}
		pl, err = s.o.Playlists.CreatePersonal(ctx, c.viewer, c.param("name"), tracks)
	}
	if err != nil {
		s.playlistFail(c, err)
		return
	}
	e := s.playlistEl(c, pl)
	for _, t := range pl.Tracks {
		e.item(s.songEl("entry", t, userState{}))
	}
	c.ok(e)
}

// updatePlaylist renomme une playlist personnelle, y ajoute ou en retire
// des titres (songIdToAdd, songIndexToRemove).
func (s *Server) updatePlaylist(c *call) {
	if s.o.Playlists == nil {
		c.fail(errNotAuthorized, "Playlists indisponibles")
		return
	}
	id, ok := playlistIDParam(c.param("playlistId"))
	if !ok {
		c.fail(errNotFound, "Playlist introuvable")
		return
	}
	e := PlaylistEdit{}
	if c.r.Form.Has("name") {
		n := c.param("name")
		e.Name = &n
	}
	for _, raw := range c.params("songIndexToRemove") {
		if i, err := strconv.Atoi(raw); err == nil && i >= 0 {
			e.Remove = append(e.Remove, i)
		}
	}
	adds := c.params("songIdToAdd")
	if len(adds) > maxAddPerCall {
		c.fail(errGeneric, "Trop de titres en une fois")
		return
	}
	for _, raw := range adds {
		kind, tid, ok := parseID(raw)
		if !ok || kind != KindTrack {
			c.fail(errNotFound, "Titre inconnu")
			return
		}
		t, err := s.o.Source.Track(c.r.Context(), tid)
		if err != nil {
			c.fail(errNotFound, "Titre introuvable")
			return
		}
		e.Add = append(e.Add, t)
	}
	if _, err := s.o.Playlists.UpdatePersonal(c.r.Context(), c.viewer, id, e); err != nil {
		s.playlistFail(c, err)
		return
	}
	c.ok(nil)
}

func (s *Server) deletePlaylist(c *call) {
	if s.o.Playlists == nil {
		c.fail(errNotAuthorized, "Playlists indisponibles")
		return
	}
	id, ok := playlistIDParam(c.param("id"))
	if !ok {
		c.fail(errNotFound, "Playlist introuvable")
		return
	}
	if err := s.o.Playlists.DeletePersonal(c.r.Context(), c.viewer, id); err != nil {
		s.playlistFail(c, err)
		return
	}
	c.ok(nil)
}

// --- Jellyfin ---

func (q *jfReq) playlistDTO(pl Playlist, st jfState) map[string]any {
	d := q.base(guidPlaylist, pl.ID, pl.Name, st)
	d["Type"] = "Playlist"
	d["IsFolder"] = true
	d["MediaType"] = "Audio"
	d["ProviderIds"] = map[string]string{}
	d["ChildCount"] = len(pl.Tracks)
	d["SongCount"] = len(pl.Tracks)
	d["Overview"] = playlistComment(pl)
	d["RunTimeTicks"] = int64(pl.Duration()) * 10_000_000
	d["CumulativeRunTimeTicks"] = int64(pl.Duration()) * 10_000_000
	if c := pl.Cover(); c != "" {
		d["ImageTags"] = map[string]string{"Primary": "c" + c}
		d["PrimaryImageAspectRatio"] = 1
	}
	return d
}

// JellyfinPlaylists renvoie les playlists du spectateur, à ajouter à
// celles d'Ether.
func (s *Server) JellyfinPlaylists(ctx context.Context, viewer int64, j Jellyfin) []any {
	pls := s.playlistsOf(ctx, viewer)
	if len(pls) == 0 {
		return nil
	}
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	q := &jfReq{s: s, j: j, r: r, viewer: viewer, q: map[string]string{}}
	out := make([]any, 0, len(pls))
	for _, pl := range pls {
		out = append(out, q.playlistDTO(pl, jfState{}))
	}
	return out
}

func (q *jfReq) playlistTracks(ctx context.Context, id int64) ([]any, bool) {
	pl, ok := q.s.playlist(ctx, q.viewer, id)
	if !ok {
		return nil, false
	}
	st := q.state(ctx)
	out := make([]any, 0, len(pl.Tracks))
	for i, t := range pl.Tracks {
		d := q.trackDTO(t, st)
		d["PlaylistItemId"] = strconv.Itoa(i + 1)
		out = append(out, d)
	}
	return out, true
}

// playlists traite /Playlists/{id}[/Items] pour une playlist PSE.
func (q *jfReq) playlists(rest []string, id int64) {
	ctx := q.r.Context()
	get := q.r.Method == http.MethodGet || q.r.Method == http.MethodHead
	if !get {
		http.Error(q.w, "Playlist importée : lecture seule", http.StatusForbidden)
		return
	}
	pl, ok := q.s.playlist(ctx, q.viewer, id)
	if !ok {
		http.NotFound(q.w, q.r)
		return
	}
	if len(rest) == 0 {
		ids := make([]string, 0, len(pl.Tracks))
		for _, t := range pl.Tracks {
			ids = append(ids, JellyfinGUID(guidTrack, t.ID))
		}
		q.reply(map[string]any{"OpenAccess": false, "Shares": []any{}, "ItemIds": ids})
		return
	}
	if strings.EqualFold(rest[0], "items") {
		out, _ := q.playlistTracks(ctx, id)
		q.page(out)
		return
	}
	q.result(nil, 0, 0)
}

// --- Plex ---

func (q *psemReq) playlistMD(pl Playlist) model.Metadata {
	rk := psemKey(psemPlaylist, pl.ID)
	stamp := pl.SyncedAt
	if stamp == 0 {
		stamp = 1
	}
	md := model.Metadata{
		RatingKey:    rk,
		Key:          "/playlists/" + rk + "/items",
		GUID:         "com.plexapp.agents.none://playlist-" + rk,
		Type:         "playlist",
		Title:        pl.Name,
		Summary:      playlistComment(pl),
		Smart:        model.Ptr(false),
		PlaylistType: "audio",
		Composite:    "/playlists/" + rk + "/composite/" + strconv.FormatInt(stamp, 10),
		LeafCount:    model.Ptr(len(pl.Tracks)),
		Duration:     int64(pl.Duration()) * 1000,
		AddedAt:      pl.SyncedAt,
		UpdatedAt:    pl.SyncedAt,
	}
	if c := pl.Cover(); c != "" {
		md.Thumb = psemThumb(rk, 'c', c)
	}
	return md
}

// PlexPlaylists renvoie les playlists du spectateur, à ajouter à celles
// d'Ether.
func (s *Server) PlexPlaylists(ctx context.Context, viewer int64) []any {
	pls := s.playlistsOf(ctx, viewer)
	if len(pls) == 0 {
		return nil
	}
	q := &psemReq{m: s, viewer: viewer}
	out := make([]any, 0, len(pls))
	for _, pl := range pls {
		out = append(out, q.playlistMD(pl))
	}
	return out
}

// playlistsRoute traite /playlists (liste audio) et /playlists/{clé}/….
func (q *psemReq) playlistsRoute(p string, v url.Values) bool {
	get := q.r.Method == http.MethodGet || q.r.Method == http.MethodHead
	if p == "/playlists" || p == "/playlists/all" {
		if !get || !strings.Contains(v.Get("playlistType"), "audio") {
			return false
		}
		var out []model.Metadata
		for _, pl := range q.m.playlistsOf(q.ctx(), q.viewer) {
			out = append(out, q.playlistMD(pl))
		}
		mc := q.container(out, "Playlists")
		mc.LibrarySectionID, mc.LibrarySectionTitle, mc.LibrarySectionUUID, mc.Title1, mc.Title2 = nil, "", "", "", ""
		q.p.Write(q.w, q.r, mc)
		return true
	}
	segs := strings.Split(strings.Trim(strings.TrimPrefix(p, "/playlists/"), "/"), "/")
	kind, id, ok := psemParse(segs[0])
	if !ok || kind != psemPlaylist {
		return false
	}
	if !get {
		http.Error(q.w, "Playlist importée : lecture seule", http.StatusForbidden)
		return true
	}
	pl, ok := q.m.playlist(q.ctx(), q.viewer, id)
	if !ok {
		http.NotFound(q.w, q.r)
		return true
	}
	sub := ""
	if len(segs) > 1 {
		sub = segs[1]
	}
	switch sub {
	case "":
		mc := q.container([]model.Metadata{q.playlistMD(pl)}, pl.Name)
		mc.LibrarySectionID, mc.LibrarySectionTitle, mc.LibrarySectionUUID = nil, "", ""
		q.p.Write(q.w, q.r, mc)
	case "items":
		out := make([]model.Metadata, 0, len(pl.Tracks))
		for i, t := range pl.Tracks {
			md := q.trackMD(t)
			md.PlaylistItemID = int64(i + 1)
			out = append(out, md)
		}
		mc := q.container(out, pl.Name)
		mc.RatingKey = psemKey(psemPlaylist, pl.ID)
		mc.Key = "/playlists/" + mc.RatingKey + "/items"
		mc.Title1 = pl.Name
		mc.PlaylistType = "audio"
		mc.Smart = model.Ptr(false)
		mc.LeafCount = model.Ptr(len(pl.Tracks))
		mc.Duration = int64(pl.Duration()) * 1000
		mc.Composite = q.playlistMD(pl).Composite
		q.p.Write(q.w, q.r, mc)
	case "composite":
		if c := pl.Cover(); c != "" {
			q.m.RelayImage(q.w, q.r, 'c', c, q.num(v, "width"))
			return true
		}
		http.NotFound(q.w, q.r)
	default:
		q.empty()
	}
	return true
}

func (q *psemReq) num(v url.Values, k string) int {
	n, _ := strconv.Atoi(v.Get(k))
	return max(n, 0)
}

// playlistTracksFor renvoie les titres d'une playlist désignée par un
// paramètre playlistID ou un uri « …/playlists/{clé}/items ».
func (q *psemReq) playlistTracksFor(v url.Values) ([]Track, bool) {
	key := v.Get("playlistID")
	if key == "" {
		uri := v.Get("uri")
		if u, err := url.PathUnescape(uri); err == nil {
			uri = u
		}
		if m := psemPlaylistRe.FindStringSubmatch(uri); m != nil {
			key = m[1]
		}
	}
	kind, id, ok := psemParse(key)
	if !ok || kind != psemPlaylist {
		return nil, false
	}
	pl, ok := q.m.playlist(q.ctx(), q.viewer, id)
	return pl.Tracks, ok
}
