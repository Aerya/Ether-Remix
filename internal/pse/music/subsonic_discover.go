package music

import "strings"

// Découverte côté Subsonic (Feishin, Symfonium…) : styles Deezer
// (getGenres, getSongsByGenre, getAlbumList2 type=byGenre), meilleurs
// titres d'un artiste (getTopSongs), radio d'artiste (getSimilarSongs) et
// artistes proches (getArtistInfo).

// genreCount : taille des classements par style annoncée aux clients.
const genreCount = 100

func (s *Server) genres(c *call) {
	e := node("genres")
	for _, g := range s.Genres(c.r.Context()) {
		e.item(node("genre", "songCount", genreCount, "albumCount", genreCount).value(g.Name))
	}
	c.ok(e)
}

func window[T any](items []T, offset, size int) []T {
	if offset >= len(items) {
		return nil
	}
	items = items[offset:]
	if size > 0 && len(items) > size {
		items = items[:size]
	}
	return items
}

func (s *Server) songsByGenre(c *call) {
	ctx := c.r.Context()
	e := node("songsByGenre")
	if g, ok := s.GenreByName(ctx, c.param("genre")); ok {
		_, _, tracks := s.GenreCharts(ctx, g.ID)
		for _, t := range window(tracks, c.num("offset", 0, 0), c.num("count", 10, 500)) {
			e.item(s.songEl("song", t, s.state(ctx, c.viewer, KindTrack, t.ID)))
		}
	}
	c.ok(e)
}

// genreAlbums sert getAlbumList(2) type=byGenre ; faux si le style est
// inconnu (la liste de la bibliothèque prend le relais).
func (s *Server) genreAlbums(c *call, container string, id3 bool, o ListOptions) bool {
	ctx := c.r.Context()
	g, ok := s.GenreByName(ctx, c.param("genre"))
	if !ok {
		return false
	}
	_, albums, _ := s.GenreCharts(ctx, g.ID)
	e := node(container)
	for _, a := range window(albums, o.Offset, o.Limit) {
		e.item(s.albumEl("album", a, s.state(ctx, c.viewer, KindAlbum, a.ID), id3))
	}
	c.ok(e)
	return true
}

func (s *Server) topSongs(c *call) {
	ctx := c.r.Context()
	e := node("topSongs")
	name := strings.TrimSpace(c.param("artist"))
	if name == "" {
		c.fail(errMissingParam, "Paramètre artist requis")
		return
	}
	if as, err := s.o.Source.SearchArtists(ctx, name, 5, 0); err == nil && len(as) > 0 {
		pick := as[0] // le nom exact, sinon le premier résultat
		for _, a := range as {
			if foldWords(a.Name) == foldWords(name) {
				pick = a
				break
			}
		}
		for _, t := range s.TopTracks(ctx, pick.ID, c.num("count", 50, 100)) {
			e.item(s.songEl("song", t, s.state(ctx, c.viewer, KindTrack, t.ID)))
		}
	}
	c.ok(e)
}

// artistOf retrouve l'artiste d'un identifiant d'artiste, d'album ou de
// titre.
func (s *Server) artistOf(c *call) (int64, bool) {
	kind, id, ok := parseID(c.param("id"))
	if !ok {
		return 0, false
	}
	ctx := c.r.Context()
	switch kind {
	case KindArtist:
		return id, true
	case KindAlbum:
		a, _, err := s.o.Source.Album(ctx, id)
		return a.Artist.ID, err == nil && a.Artist.ID > 0
	case KindTrack:
		t, err := s.o.Source.Track(ctx, id)
		return t.Artist.ID, err == nil && t.Artist.ID > 0
	}
	return 0, false
}

func (s *Server) similarSongs(container string) func(*call) {
	return func(c *call) {
		ctx := c.r.Context()
		e := node(container)
		if artist, ok := s.artistOf(c); ok {
			for _, t := range s.Radio(ctx, artist, c.num("count", 50, 100)) {
				e.item(s.songEl("song", t, s.state(ctx, c.viewer, KindTrack, t.ID)))
			}
		}
		c.ok(e)
	}
}

func (s *Server) artistInfo(container string) func(*call) {
	return func(c *call) {
		ctx := c.r.Context()
		e := node(container)
		if artist, ok := s.artistOf(c); ok {
			for _, a := range s.Similar(ctx, artist, c.num("count", 20, 50)) {
				e.item(s.artistEl("similarArtist", a, -1, s.state(ctx, c.viewer, KindArtist, a.ID)))
			}
		}
		c.ok(e)
	}
}
