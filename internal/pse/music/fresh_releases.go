package music

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

// « Nouveautés de vos artistes » : titres des albums sortis ces derniers
// mois par les artistes favoris et les plus écoutés du spectateur. Le
// calcul interroge le catalogue (un appel par artiste et par album) : il
// tourne en arrière-plan et son résultat est gardé quelques heures, pour
// que la liste des playlists reste instantanée.

const (
	freshRef      = "fresh"
	freshName     = "Nouveautés de vos artistes"
	freshComment  = "Albums sortis ces 4 derniers mois par vos artistes favoris et les plus écoutés"
	freshWindow   = 120 * 24 * time.Hour
	freshTTL      = 6 * time.Hour
	freshArtists  = 25
	freshPerAlbum = 5
)

type freshEntry struct {
	at      time.Time
	tracks  []Track
	running bool
}

type freshCache struct {
	mu sync.Mutex
	m  map[int64]*freshEntry
}

// freshPlaylist renvoie la playlist calculée (vide si elle ne l'est pas
// encore) et relance son calcul en arrière-plan si elle est périmée.
func (s *Server) freshPlaylist(ctx context.Context, viewer int64) (Playlist, bool) {
	pl := Playlist{ID: PlaylistID(viewer, SourceSmart, freshRef), Viewer: viewer, Source: SourceSmart, Ref: freshRef, Name: freshName}
	if s.o.Library == nil || s.o.Source == nil || viewer == 0 {
		return pl, false
	}
	now := s.clock()
	s.fresh.mu.Lock()
	if s.fresh.m == nil {
		s.fresh.m = map[int64]*freshEntry{}
	}
	e := s.fresh.m[viewer]
	if e == nil {
		e = &freshEntry{}
		s.fresh.m[viewer] = e
	}
	stale := now.Sub(e.at) >= freshTTL
	if stale && !e.running {
		e.running = true
		go s.computeFresh(viewer)
	}
	pl.Tracks, pl.SyncedAt = e.tracks, e.at.Unix()
	s.fresh.mu.Unlock()
	return pl, len(pl.Tracks) > 0
}

func (s *Server) computeFresh(viewer int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var tracks []Track
	done := false
	defer func() {
		// Une erreur du catalogue ne doit jamais arrêter Ether-Remix.
		if r := recover(); r != nil {
			s.o.Logger.Warn("nouveautés des artistes : calcul interrompu", "err", r)
		}
		s.fresh.mu.Lock()
		defer s.fresh.mu.Unlock()
		e := s.fresh.m[viewer]
		e.running = false
		if done && (ctx.Err() == nil || len(tracks) > 0) {
			e.at, e.tracks = s.clock(), tracks
		}
	}()
	tracks = s.freshTracks(ctx, viewer)
	done = true
}

// freshTracks cherche les albums récents des artistes du spectateur.
func (s *Server) freshTracks(ctx context.Context, viewer int64) []Track {
	seen := map[int64]bool{}
	var artists []int64
	for _, o := range []ListOptions{{Starred: true, Order: "starred"}, {Played: true, Order: "frequent"}} {
		o.Limit = freshArtists
		items, _ := s.o.Library.List(ctx, viewer, KindArtist, o)
		for _, it := range items {
			var a Artist
			if json.Unmarshal(it.Data, &a) == nil && a.ID != 0 && !seen[a.ID] && len(artists) < freshArtists {
				seen[a.ID] = true
				artists = append(artists, a.ID)
			}
		}
	}
	since := s.clock().Add(-freshWindow).Format("2006-01-02")
	var recent []Album
	for _, id := range artists {
		_, albums, err := s.o.Source.Artist(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			continue
		}
		for _, a := range albums {
			if a.ReleaseDate >= since && (a.Artist.ID == 0 || a.Artist.ID == id) {
				recent = append(recent, a)
			}
		}
	}
	sort.SliceStable(recent, func(i, j int) bool { return recent[i].ReleaseDate > recent[j].ReleaseDate })
	var out []Track
	for _, a := range recent {
		if len(out) >= smartSize {
			break
		}
		_, tracks, err := s.o.Source.Album(ctx, a.ID)
		if err != nil {
			continue
		}
		if len(tracks) > freshPerAlbum {
			tracks = tracks[:freshPerAlbum]
		}
		out = append(out, tracks...)
	}
	if len(out) > smartSize {
		out = out[:smartSize]
	}
	return out
}
