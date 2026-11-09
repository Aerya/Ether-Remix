package music

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Accès au catalogue pour les frontaux hors Subsonic (Plex). Les réponses
// de la source sont gardées quelques minutes, comme pour Jellyfin.

// LibraryOf renvoie la bibliothèque personnelle (favoris, écoutes).
func (s *Server) LibraryOf() *Library { return s.o.Library }

// Container renvoie le type MIME et l'extension annoncés pour les flux.
func (s *Server) Container() (mime, suffix string) { return s.contentType() }

// AlbumTracks renvoie un album et ses titres.
func (s *Server) AlbumTracks(ctx context.Context, id int64) (Album, []Track, error) {
	type pair struct {
		a Album
		t []Track
	}
	p, err := memoized(s.cache, "al:"+strconv.FormatInt(id, 10), s.clock(), func() (pair, error) {
		a, t, err := s.o.Source.Album(ctx, id)
		return pair{a, t}, err
	})
	return p.a, p.t, err
}

// ArtistAlbums renvoie un artiste et ses albums.
func (s *Server) ArtistAlbums(ctx context.Context, id int64) (Artist, []Album, error) {
	type pair struct {
		a  Artist
		al []Album
	}
	p, err := memoized(s.cache, "ar:"+strconv.FormatInt(id, 10), s.clock(), func() (pair, error) {
		a, al, err := s.o.Source.Artist(ctx, id)
		return pair{a, al}, err
	})
	return p.a, p.al, err
}

// TrackFor renvoie un titre, depuis la bibliothèque du spectateur si elle
// le connaît.
func (s *Server) TrackFor(ctx context.Context, viewer, id int64) (Track, error) {
	if it, ok, _ := s.o.Library.Get(ctx, viewer, KindTrack, id); ok {
		if t := decode[Track](it); t.Title != "" {
			return t, nil
		}
	}
	return memoized(s.cache, "tr:"+strconv.FormatInt(id, 10), s.clock(), func() (Track, error) {
		return s.o.Source.Track(ctx, id)
	})
}

// ArtistTopTracks renvoie les titres populaires d'un artiste (vide si la
// source n'en publie pas).
func (s *Server) ArtistTopTracks(ctx context.Context, id int64) []Track {
	tt, ok := s.o.Source.(TopTracker)
	if !ok {
		return nil
	}
	tracks, _ := memoized(s.cache, "top:"+strconv.FormatInt(id, 10), s.clock(), func() ([]Track, error) {
		return tt.ArtistTop(ctx, id, 50)
	})
	return tracks
}

// Charts renvoie les classements (vides si la source n'en publie pas).
func (s *Server) Charts(ctx context.Context) ([]Artist, []Album, []Track) {
	ch, ok := s.o.Source.(Charter)
	if !ok {
		return nil, nil, nil
	}
	now := s.clock()
	ar, _ := memoized(s.charts, "artists", now, func() ([]Artist, error) { return ch.ChartArtists(ctx, 100) })
	al, _ := memoized(s.charts, "albums", now, func() ([]Album, error) { return ch.ChartAlbums(ctx, 100) })
	tr, _ := memoized(s.charts, "tracks", now, func() ([]Track, error) { return ch.ChartTracks(ctx, 100) })
	return ar, al, tr
}

// Search interroge tout le catalogue : trois recherches en parallèle,
// bornées à 2,5 s (une source lente ne retarde pas la recherche d'Ether),
// gardées 2 minutes (les clients cherchent à chaque frappe).
func (s *Server) Search(ctx context.Context, q string, limit int) ([]Artist, []Album, []Track) {
	type res struct {
		ar []Artist
		al []Album
		tr []Track
	}
	key := strconv.Itoa(limit) + ":" + strings.ToLower(strings.TrimSpace(q))
	r, _ := memoized(s.searches, key, s.clock(), func() (res, error) {
		ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		defer cancel()
		src := s.o.Source
		var out res
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); out.ar, _ = src.SearchArtists(ctx, q, limit, 0) }()
		go func() { defer wg.Done(); out.al, _ = src.SearchAlbums(ctx, q, limit, 0) }()
		go func() { defer wg.Done(); out.tr, _ = src.SearchTracks(ctx, q, limit, 0) }()
		wg.Wait()
		return out, nil
	})
	return r.ar, r.al, r.tr
}

// Star met en favori (ou retire) un artiste, un album ou un titre.
func (s *Server) Star(ctx context.Context, viewer int64, kind string, id int64, on bool) error {
	switch kind {
	case KindArtist:
		a, err := s.artistSnapshot(ctx, viewer, id, on)
		if err != nil {
			return err
		}
		return s.o.Library.StarArtist(ctx, viewer, a, on)
	case KindAlbum:
		a, err := s.albumSnapshot(ctx, viewer, id, on)
		if err != nil {
			return err
		}
		return s.o.Library.StarAlbum(ctx, viewer, a, on)
	}
	t, err := s.trackSnapshot(ctx, viewer, id, on)
	if err != nil {
		return err
	}
	return s.o.Library.StarTrack(ctx, viewer, t, on)
}

// Played enregistre une écoute.
func (s *Server) Played(ctx context.Context, viewer, id int64) {
	if t, err := s.TrackFor(ctx, viewer, id); err == nil {
		_ = s.o.Library.Played(ctx, viewer, t)
	}
}

// RelayAudio relaie le flux d'un titre ; une erreur signifie que rien n'a
// été écrit.
func (s *Server) RelayAudio(w http.ResponseWriter, r *http.Request, id int64) error {
	return s.relayAudio(w, r, id)
}

// RelayImage relaie une pochette ('c') ou une photo d'artiste ('p').
func (s *Server) RelayImage(w http.ResponseWriter, r *http.Request, kind byte, ref string, size int) {
	s.relayImage(w, r, kind, ref, size)
}

// Decode lit l'instantané d'une entrée de bibliothèque.
func DecodeArtist(it Item) Artist { return decode[Artist](it) }
func DecodeAlbum(it Item) Album   { return decode[Album](it) }
func DecodeTrack(it Item) Track   { return decode[Track](it) }
