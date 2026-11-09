package music

import (
	"context"
	"strconv"
	"strings"
)

// Découverte côté Jellyfin : styles Deezer (MusicGenres, filtre GenreIds),
// mix instantané (radio d'artiste) et artistes similaires.

// guidGenre : GUID des styles.
const guidGenre = 5

func (q *jfReq) genreBase(g Genre) map[string]any {
	d := q.base(guidGenre, g.ID, g.Name, jfState{})
	d["Type"] = "MusicGenre"
	d["IsFolder"] = true
	d["MediaType"] = "Unknown"
	d["ParentId"] = q.j.ViewID
	d["ChildCount"] = genreCount
	d["AlbumCount"] = genreCount
	d["SongCount"] = genreCount
	d["ImageTags"] = map[string]string{"Primary": "g" + strconv.FormatInt(g.ID, 10)}
	return d
}

func (q *jfReq) genreDTOs() []any {
	gs := q.s.Genres(q.r.Context())
	out := make([]any, 0, len(gs))
	for _, g := range gs {
		out = append(out, q.genreBase(g))
	}
	return out
}

func (q *jfReq) genreDTO(ctx context.Context, id int64) (map[string]any, error) {
	for _, g := range q.s.Genres(ctx) {
		if g.ID == id {
			return q.genreBase(g), nil
		}
	}
	return nil, ErrNotFound
}

// genreParam lit le style demandé : ParentId, GenreIds ou Genres (noms).
func (q *jfReq) genreParam(parent string) int64 {
	if kind, id, ok := ParseJellyfinGUID(parent); ok && kind == guidGenre {
		return id
	}
	for _, v := range q.list("GenreIds") {
		if kind, id, ok := ParseJellyfinGUID(v); ok && kind == guidGenre {
			return id
		}
	}
	for _, v := range q.list("Genres") {
		if g, ok := q.s.GenreByName(q.r.Context(), v); ok {
			return g.ID
		}
	}
	return 0
}

// genreChildren renvoie les albums, artistes ou titres du moment d'un style.
func (q *jfReq) genreChildren(ctx context.Context, id int64, types []string, st jfState) []any {
	artists, albums, tracks := q.s.GenreCharts(ctx, id)
	var out []any
	switch {
	case hasType(types, "MusicArtist"):
		for _, a := range artists {
			out = append(out, q.artistDTO(a, st))
		}
	case hasType(types, "Audio") && !hasType(types, "MusicAlbum"):
		for _, t := range tracks {
			out = append(out, q.trackDTO(t, st))
		}
	default:
		for _, a := range albums {
			out = append(out, q.albumDTO(a, st))
		}
	}
	return out
}

// genreMix sert /MusicGenres/{nom}/InstantMix : les titres du moment.
func (q *jfReq) genreMix(name string) bool {
	ctx := q.r.Context()
	id := int64(0)
	if kind, gid, ok := ParseJellyfinGUID(name); ok && kind == guidGenre {
		id = gid
	} else if g, ok := q.s.GenreByName(ctx, name); ok {
		id = g.ID
	}
	var out []any
	if id != 0 {
		_, _, tracks := q.s.GenreCharts(ctx, id)
		st := q.state(ctx)
		for _, t := range tracks {
			out = append(out, q.trackDTO(t, st))
		}
	}
	q.page(out)
	return true
}

// discover sert …/{id}/InstantMix (radio de l'artiste de l'objet) et
// …/{id}/Similar (artistes proches). Faux pour tout autre chemin.
func (q *jfReq) discover(guid, what string) bool {
	what = strings.ToLower(what)
	if what != "instantmix" && what != "similar" {
		return false
	}
	ctx := q.r.Context()
	kind, id, ok := ParseJellyfinGUID(guid)
	if !ok {
		return false
	}
	if kind == guidGenre {
		if what == "instantmix" {
			return q.genreMix(guid)
		}
		q.page(nil)
		return true
	}
	artist := int64(0)
	switch kind {
	case guidArtist:
		artist = id
	case guidAlbum:
		if a, _, err := q.album(ctx, id); err == nil {
			artist = a.Artist.ID
		}
	case guidTrack:
		if t, err := q.track(ctx, id); err == nil {
			artist = t.Artist.ID
		}
	}
	st := q.state(ctx)
	var out []any
	if what == "instantmix" {
		for _, t := range q.s.Radio(ctx, artist, 0) {
			out = append(out, q.trackDTO(t, st))
		}
	} else if kind == guidArtist {
		for _, a := range q.s.Similar(ctx, artist, 0) {
			out = append(out, q.artistDTO(a, st))
		}
	}
	q.page(out)
	return true
}
