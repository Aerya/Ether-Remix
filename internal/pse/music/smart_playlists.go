package music

import (
	"context"
	"encoding/json"
)

// Playlists automatiques de chaque spectateur, calculées à la demande à
// partir de ses favoris et de ses écoutes (rien n'est stocké, aucun
// service externe). Elles sont en lecture seule.

// SourceSmart marque une playlist automatique.
const SourceSmart = "smart"

// smartSize borne chaque playlist automatique.
const smartSize = 100

type smartDef struct {
	ref, name, comment string
	opts               ListOptions
	keep               func(Item) bool
}

var smartDefs = []smartDef{
	{"starred", "Titres favoris", "Vos titres favoris, du plus récent au plus ancien",
		ListOptions{Starred: true, Order: "starred"}, nil},
	{"recent", "Écoutés récemment", "Vos dernières écoutes",
		ListOptions{Played: true, Order: "played"}, nil},
	{"frequent", "Les plus écoutés", "Vos titres les plus écoutés",
		ListOptions{Played: true, Order: "frequent"}, nil},
	{"forgotten", "Favoris peu écoutés", "Favoris écoutés une fois ou jamais",
		ListOptions{Starred: true, Order: "random"}, func(it Item) bool { return it.PlayCount <= 1 }},
}

func smartComment(ref string) string {
	if ref == freshRef {
		return freshComment
	}
	for _, d := range smartDefs {
		if d.ref == ref {
			return d.comment
		}
	}
	return "Playlist automatique"
}

// smartPlaylists calcule les playlists automatiques non vides du
// spectateur.
func (s *Server) smartPlaylists(ctx context.Context, viewer int64) []Playlist {
	if s.o.Library == nil || viewer == 0 {
		return nil
	}
	var out []Playlist
	if pl, ok := s.freshPlaylist(ctx, viewer); ok {
		out = append(out, pl)
	}
	for _, d := range smartDefs {
		if pl, ok := s.smartPlaylist(ctx, viewer, d); ok {
			out = append(out, pl)
		}
	}
	return out
}

func (s *Server) smartPlaylist(ctx context.Context, viewer int64, d smartDef) (Playlist, bool) {
	o := d.opts
	o.Limit = smartSize
	if d.keep != nil {
		o.Limit = smartSize * 5
	}
	items, err := s.o.Library.List(ctx, viewer, KindTrack, o)
	if err != nil {
		return Playlist{}, false
	}
	pl := Playlist{ID: PlaylistID(viewer, SourceSmart, d.ref), Viewer: viewer, Source: SourceSmart, Ref: d.ref, Name: d.name}
	for _, it := range items {
		if d.keep != nil && !d.keep(it) {
			continue
		}
		var t Track
		if json.Unmarshal(it.Data, &t) != nil || t.ID == 0 {
			continue
		}
		pl.Tracks = append(pl.Tracks, t)
		if len(pl.Tracks) >= smartSize {
			break
		}
		if it.PlayedAt > pl.SyncedAt {
			pl.SyncedAt = it.PlayedAt
		}
	}
	return pl, len(pl.Tracks) > 0
}

// smartByID retrouve une playlist automatique par son identifiant.
func (s *Server) smartByID(ctx context.Context, viewer, id int64) (Playlist, bool) {
	if PlaylistID(viewer, SourceSmart, freshRef) == id {
		return s.freshPlaylist(ctx, viewer)
	}
	for _, d := range smartDefs {
		if PlaylistID(viewer, SourceSmart, d.ref) == id {
			return s.smartPlaylist(ctx, viewer, d)
		}
	}
	return Playlist{}, false
}
