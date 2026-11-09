package music

import (
	"strconv"

	"github.com/braedonsaunders/ether/internal/plex/model"
)

// Découverte côté Plex : rangées « Découvertes » et tops par pays sur
// l'accueil Musique, navigation « Par style », artistes similaires et
// radio sur la page d'un artiste.

// playlistHubs met en rangées les playlists système du spectateur.
func (q *psemReq) playlistHubs() []model.Hub {
	var hubs []model.Hub
	for _, pl := range q.m.playlistsOf(q.ctx(), q.viewer) {
		if pl.Source != SourceDiscover && pl.Source != SourceCharts {
			continue
		}
		items := make([]model.Metadata, 0, min(len(pl.Tracks), 30))
		for _, t := range pl.Tracks {
			if len(items) == 30 {
				break
			}
			items = append(items, q.trackMD(t))
		}
		if len(items) > 0 {
			hubs = append(hubs, psemHub("pl."+pl.Ref, pl.Name, "track", items))
		}
	}
	return hubs
}

// genres liste les styles (/library/sections/5053/genre).
func (q *psemReq) genres() {
	mc := psemContainer()
	mc.LibrarySectionID = model.Ptr(pseMusicSection)
	mc.Title1 = pseMusicTitle
	mc.Title2 = "Par style"
	sec := "/library/sections/" + strconv.Itoa(pseMusicSection)
	for _, g := range q.m.Genres(q.ctx()) {
		id := strconv.FormatInt(g.ID, 10)
		mc.Directory = append(mc.Directory, model.Directory{Key: id, ID: id, Title: g.Name, Type: "genre",
			FastKey: sec + "/all?type=9&genre=" + id})
	}
	mc.Size = len(mc.Directory)
	q.p.Write(q.w, q.r, mc)
}

// genreItems sert all?genre=ID : albums (par défaut), artistes (type=8)
// ou titres (type=10) du moment pour ce style.
func (q *psemReq) genreItems(raw, typ string) {
	g, ok := q.m.GenreByName(q.ctx(), raw)
	if !ok {
		q.empty()
		return
	}
	if typ == "" {
		typ = "9"
	}
	ar, al, tr := q.m.GenreCharts(q.ctx(), g.ID)
	var out []model.Metadata
	switch typ {
	case "8":
		for _, a := range ar {
			out = append(out, q.artistMD(a))
		}
	case "10":
		for _, t := range tr {
			out = append(out, q.trackMD(t))
		}
	default:
		for _, a := range al {
			out = append(out, q.albumMD(a))
		}
	}
	q.p.Write(q.w, q.r, q.container(out, g.Name))
}

// relatedHubs sert /hubs/metadata/{artiste}[/related] : artistes proches
// et radio de l'artiste.
func (q *psemReq) relatedHubs(key string) bool {
	kind, id, ok := psemParse(key)
	if !ok {
		return false
	}
	artist := int64(0)
	switch kind {
	case psemArtist:
		artist = id
	case psemAlbum:
		if a, _, err := q.m.o.Source.Album(q.ctx(), id); err == nil {
			artist = a.Artist.ID
		}
	case psemTrack:
		if t, err := q.m.o.Source.Track(q.ctx(), id); err == nil {
			artist = t.Artist.ID
		}
	}
	mc := psemContainer()
	mc.LibrarySectionID = model.Ptr(pseMusicSection)
	mc.LibrarySectionTitle = pseMusicTitle
	mc.LibrarySectionUUID = pseMusicUUID
	var similar, radio []model.Metadata
	for _, a := range q.m.Similar(q.ctx(), artist, 20) {
		similar = append(similar, q.artistMD(a))
	}
	for _, t := range q.m.Radio(q.ctx(), artist, 30) {
		radio = append(radio, q.trackMD(t))
	}
	if len(similar) > 0 {
		mc.Hub = append(mc.Hub, psemHub("similar", "Artistes similaires", "artist", similar))
	}
	if len(radio) > 0 {
		mc.Hub = append(mc.Hub, psemHub("radio", "Dans le même esprit", "track", radio))
	}
	mc.Size = len(mc.Hub)
	q.p.Write(q.w, q.r, mc)
	return true
}
