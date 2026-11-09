package music

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Découverte musicale : styles (genres Deezer et leurs classements),
// artistes similaires et radio d'artiste. Servie par les clients Subsonic
// (getGenres, getSimilarSongs, getTopSongs, getArtistInfo), Jellyfin
// (genres, mix instantané) et Plex. Rien n'est téléchargé : seules des
// références au catalogue Deezer circulent.

// Genre est un style musical du catalogue.
type Genre struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Picture string `json:"picture,omitempty"`
}

// Discoverer est implémenté par les sources qui savent proposer des styles
// et des artistes proches.
type Discoverer interface {
	Genres(ctx context.Context) ([]Genre, error)
	GenreArtists(ctx context.Context, genre int64, limit int) ([]Artist, error)
	GenreAlbums(ctx context.Context, genre int64, limit int) ([]Album, error)
	GenreTracks(ctx context.Context, genre int64, limit int) ([]Track, error)
	Related(ctx context.Context, artist int64, limit int) ([]Artist, error)
	ArtistRadio(ctx context.Context, artist int64, limit int) ([]Track, error)
}

// genresSkipped : genres Deezer sans musique (livres audio) ou « Tous ».
var genresSkipped = map[int64]bool{0: true, 457: true}

// Genres renvoie les styles Deezer, nommés en français.
func (d *DeezerSource) Genres(ctx context.Context) ([]Genre, error) {
	fr := *d
	if fr.Lang == "" {
		fr.Lang = "fr"
	}
	var r struct {
		Data []struct {
			ID      int64  `json:"id"`
			Name    string `json:"name"`
			Picture string `json:"picture_xl"`
		}
	}
	if err := fr.get(ctx, "/genre", nil, &r); err != nil {
		return nil, err
	}
	out := make([]Genre, 0, len(r.Data))
	for _, g := range r.Data {
		if genresSkipped[g.ID] || strings.TrimSpace(g.Name) == "" {
			continue
		}
		out = append(out, Genre{ID: g.ID, Name: g.Name, Picture: g.Picture})
	}
	return out, nil
}

func chartPath(genre int64, what string) string {
	return "/chart/" + strconv.FormatInt(genre, 10) + "/" + what
}

func (d *DeezerSource) GenreArtists(ctx context.Context, genre int64, limit int) ([]Artist, error) {
	return d.artists(ctx, chartPath(genre, "artists"), limit)
}

func (d *DeezerSource) GenreAlbums(ctx context.Context, genre int64, limit int) ([]Album, error) {
	var r struct{ Data []dzAlbum }
	if err := d.get(ctx, chartPath(genre, "albums"), url.Values{"limit": {strconv.Itoa(limit)}}, &r); err != nil {
		return nil, err
	}
	out := make([]Album, 0, len(r.Data))
	for i := range r.Data {
		out = append(out, r.Data[i].conv())
	}
	return out, nil
}

func (d *DeezerSource) GenreTracks(ctx context.Context, genre int64, limit int) ([]Track, error) {
	return d.tracks(ctx, chartPath(genre, "tracks"), limit)
}

// Related renvoie les artistes proches d'un artiste.
func (d *DeezerSource) Related(ctx context.Context, artist int64, limit int) ([]Artist, error) {
	return d.artists(ctx, "/artist/"+strconv.FormatInt(artist, 10)+"/related", limit)
}

// ArtistRadio renvoie une sélection de titres dans l'esprit d'un artiste.
func (d *DeezerSource) ArtistRadio(ctx context.Context, artist int64, limit int) ([]Track, error) {
	return d.tracks(ctx, "/artist/"+strconv.FormatInt(artist, 10)+"/radio", limit)
}

func (d *DeezerSource) artists(ctx context.Context, path string, limit int) ([]Artist, error) {
	var r struct{ Data []dzArtist }
	if err := d.get(ctx, path, url.Values{"limit": {strconv.Itoa(limit)}}, &r); err != nil {
		return nil, err
	}
	out := make([]Artist, 0, len(r.Data))
	for i := range r.Data {
		out = append(out, r.Data[i].conv())
	}
	return out, nil
}

func (d *DeezerSource) tracks(ctx context.Context, path string, limit int) ([]Track, error) {
	var r struct{ Data []dzTrack }
	if err := d.get(ctx, path, url.Values{"limit": {strconv.Itoa(limit)}}, &r); err != nil {
		return nil, err
	}
	return readable(r.Data), nil
}

// --- côté serveur : mémorisation et repli ---

// discoverTTL : les styles et les classements changent peu dans l'heure.
const discoverTTL = time.Hour

func (s *Server) discoverer() (Discoverer, bool) {
	d, ok := s.o.Source.(Discoverer)
	return d, ok
}

// Genres renvoie les styles (vide si la source n'en publie pas).
func (s *Server) Genres(ctx context.Context) []Genre {
	d, ok := s.discoverer()
	if !ok {
		return nil
	}
	g, _ := memoized(s.discover, "genres", s.clock(), func() ([]Genre, error) { return d.Genres(ctx) })
	return g
}

// GenreByName retrouve un style par son nom (insensible à la casse) ou par
// son identifiant.
func (s *Server) GenreByName(ctx context.Context, name string) (Genre, bool) {
	name = strings.TrimSpace(name)
	for _, g := range s.Genres(ctx) {
		if strings.EqualFold(g.Name, name) || strconv.FormatInt(g.ID, 10) == name {
			return g, true
		}
	}
	return Genre{}, false
}

// GenreCharts renvoie les artistes, albums et titres du moment d'un style.
func (s *Server) GenreCharts(ctx context.Context, genre int64) ([]Artist, []Album, []Track) {
	d, ok := s.discoverer()
	if !ok {
		return nil, nil, nil
	}
	k := strconv.FormatInt(genre, 10)
	now := s.clock()
	ar, _ := memoized(s.discover, "ga:"+k, now, func() ([]Artist, error) { return d.GenreArtists(ctx, genre, 50) })
	al, _ := memoized(s.discover, "gl:"+k, now, func() ([]Album, error) { return d.GenreAlbums(ctx, genre, 100) })
	tr, _ := memoized(s.discover, "gt:"+k, now, func() ([]Track, error) { return d.GenreTracks(ctx, genre, 100) })
	return ar, al, tr
}

// Similar renvoie les artistes proches d'un artiste.
func (s *Server) Similar(ctx context.Context, artist int64, limit int) []Artist {
	d, ok := s.discoverer()
	if !ok || artist <= 0 {
		return nil
	}
	ar, _ := memoized(s.discover, "rel:"+strconv.FormatInt(artist, 10), s.clock(), func() ([]Artist, error) { return d.Related(ctx, artist, 20) })
	if limit > 0 && len(ar) > limit {
		ar = ar[:limit]
	}
	return ar
}

// Radio renvoie des titres dans l'esprit d'un artiste.
func (s *Server) Radio(ctx context.Context, artist int64, limit int) []Track {
	d, ok := s.discoverer()
	if !ok || artist <= 0 {
		return nil
	}
	tr, _ := memoized(s.discover, "radio:"+strconv.FormatInt(artist, 10), s.clock(), func() ([]Track, error) { return d.ArtistRadio(ctx, artist, 50) })
	if limit > 0 && len(tr) > limit {
		tr = tr[:limit]
	}
	return tr
}

// TopTracks renvoie les titres les plus écoutés d'un artiste.
func (s *Server) TopTracks(ctx context.Context, artist int64, limit int) []Track {
	t, ok := s.o.Source.(TopTracker)
	if !ok || artist <= 0 {
		return nil
	}
	tr, _ := memoized(s.discover, "top:"+strconv.FormatInt(artist, 10), s.clock(), func() ([]Track, error) { return t.ArtistTop(ctx, artist, 50) })
	if limit > 0 && len(tr) > limit {
		tr = tr[:limit]
	}
	return tr
}
