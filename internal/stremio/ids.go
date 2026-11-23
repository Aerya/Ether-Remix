package stremio

import (
	"context"
	"fmt"

	"github.com/braedonsaunders/ether/internal/core"
)

const maxAliasLookups = 4

// Lookup is how to ask an add-on's stream route about one search: its
// content type ("movie" or "series") and id, whether only whole-season
// packs are wanted, and, for an anime database id, the episode's number
// inside that entry.
type Lookup struct {
	Type           string
	ID             string
	Season         int
	Episode        int
	Packs          bool
	SpecialEpisode int // mapped IMDb special number, zero for other lookups
	EntryEpisode   int
}

// Route is the stream route path for the lookup.
func (l Lookup) Route() string {
	return "/" + ResourceStream + "/" + l.Type + "/" + escapeID(l.ID) + ".json"
}

// absoluteAliasNumber returns the season-1 absolute form (":1:<absolute>")
// add-ons use for absolute-numbered anime, when absolute numbering applies:
// the title is anime with a known absolute count, or release numbering
// (XEM / owner group) already produced an alias for it. Straight-through
// TMDB numbering (the in-season number is the absolute count past season 1,
// e.g. One Piece S15E583) is reported so the absolute form can go first.
func absoluteAliasNumber(req core.SearchRequest) (core.EpisodeNumber, bool, bool) {
	if req.Type != core.TypeEpisode || req.Season <= 0 || req.Episode <= 0 {
		return core.EpisodeNumber{}, false, false
	}
	abs := req.AbsoluteEpisode
	if abs <= 0 && req.Season == 1 {
		abs = req.Episode
	}
	if abs <= 0 || (req.Season == 1 && req.Episode == abs) {
		return core.EpisodeNumber{}, false, false
	}
	if !req.Anime && len(core.EpisodeAliases(req)) == 0 {
		return core.EpisodeNumber{}, false, false
	}
	straight := req.Season > 1 && req.AbsoluteEpisode > 0 && req.Episode == req.AbsoluteEpisode
	return core.EpisodeNumber{Season: 1, Episode: abs}, straight, true
}

// LookupStreamList returns the bounded stream routes to query for req: the
// canonical lookup (unless req.ReleaseAliasesOnly is set) plus distinct
// alias lookups from core.EpisodeAliases(req), and, where absolute or XEM
// numbering applies, always the ":1:<absolute>" form as well. For
// straight-through numbering the absolute form goes first and the season
// form second; results from every lookup are merged by the caller.
func LookupStreamList(ctx context.Context, m Manifest, req core.SearchRequest, anime AnimeIDs, specials SpecialEpisodes) []Lookup {
	var out []Lookup
	seen := map[string]bool{}
	add := func(l Lookup) {
		key := l.Type + "\x00" + l.ID
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, l)
	}
	aliasCount := 0
	addAlias := func(n core.EpisodeNumber) {
		if aliasCount >= maxAliasLookups {
			return
		}
		aliasCount++
		if look, ok := LookupStreams(ctx, m, core.AliasSearchRequest(req, n), anime, specials); ok {
			add(look)
		}
	}
	absAlias, straight, hasAbs := absoluteAliasNumber(req)
	if hasAbs && straight {
		addAlias(absAlias)
	}
	if req.Type != core.TypeEpisode || !req.ReleaseAliasesOnly {
		if look, ok := LookupStreams(ctx, m, req, anime, specials); ok {
			add(look)
		}
	}
	if req.Type == core.TypeEpisode {
		for _, n := range core.EpisodeAliases(req) {
			if hasAbs && n == absAlias {
				continue // asked explicitly, in its ordered position
			}
			addAlias(n)
		}
		if hasAbs && !straight {
			addAlias(absAlias)
		}
	}
	return out
}

// LookupStreams chooses how to ask the add-on for req's streams: by IMDb id
// when it takes them, else TMDB, else TVDB, else (for anime) an anime
// database id the community mapping knows unambiguously. A season request
// (Episode 0) asks for the season's first episode and keeps only packs.
// ok is false when the add-on cannot be asked, which skips it for this
// title. anime may be nil (no anime lookups).
func LookupStreams(ctx context.Context, m Manifest, req core.SearchRequest, anime AnimeIDs, specials SpecialEpisodes) (Lookup, bool) {
	movie := req.Type == core.TypeMovie
	switch {
	case movie:
	case req.Type == core.TypeEpisode || req.Type == core.TypeSeason:
		if req.Season < 0 || (req.Season == 0 && (req.Type != core.TypeEpisode || req.Episode <= 0)) {
			return Lookup{}, false
		}
	default:
		return Lookup{}, false
	}
	typ := TypeMovie
	if !movie {
		typ = TypeSeries
	}
	if !m.Serves(ResourceStream, typ) {
		return Lookup{}, false
	}
	ep, packs := req.Episode, false
	if !movie && ep <= 0 {
		ep, packs = 1, true
	}
	ask := func(base string) Lookup {
		if movie {
			return Lookup{Type: typ, ID: base}
		}
		return Lookup{Type: typ, ID: fmt.Sprintf("%s:%d:%d", base, req.Season, ep), Season: req.Season, Episode: ep, Packs: packs}
	}
	if imdb := IMDbID(req.IDs.IMDB); imdb != "" && m.takesPrefix(ResourceStream, "tt") {
		if req.Season == 0 && !movie {
			if specials == nil {
				return Lookup{}, false
			}
			mapped, ok := specials.LookupSpecial(ctx, req)
			if !ok || mapped <= 0 {
				return Lookup{}, false
			}
			look := Lookup{Type: typ, ID: fmt.Sprintf("%s:0:%d", imdb, mapped), Episode: mapped, SpecialEpisode: mapped}
			return look, true
		}
		return ask(imdb), true
	}
	if req.IDs.TMDB > 0 && m.takesPrefix(ResourceStream, "tmdb") {
		return ask(fmt.Sprintf("tmdb:%d", req.IDs.TMDB)), true
	}
	if !movie && req.Season == 0 {
		return Lookup{}, false
	}
	if req.IDs.TVDB > 0 && m.takesPrefix(ResourceStream, "tvdb") {
		return ask(fmt.Sprintf("tvdb:%d", req.IDs.TVDB)), true
	}
	// Anime databases number each season or cour separately; whole-season
	// pack requests and non-anime titles never load the mapping.
	if !req.Anime || packs || anime == nil {
		return Lookup{}, false
	}
	for _, db := range animeDatabases {
		if !m.takesPrefix(ResourceStream, db) {
			continue
		}
		id, episode, ok, err := anime.Lookup(ctx, req, db)
		if err != nil || !ok {
			continue
		}
		if movie {
			return Lookup{Type: typ, ID: fmt.Sprintf("%s:%d", db, id)}, true
		}
		return Lookup{Type: typ, ID: fmt.Sprintf("%s:%d:%d", db, id, episode), Season: req.Season, Episode: req.Episode, EntryEpisode: episode}, true
	}
	return Lookup{}, false
}

// IMDbID normalises an IMDb id to "tt" + digits, or "".
func IMDbID(id string) string { return imdbID(id) }

// TitleID is the content id the protocol uses for a catalogue title:
// IMDb when known, else tmdb:N. Episodes add ":season:episode" to their
// show's id. ok is false when the title has neither id.
func TitleID(t *core.Title) (typ, id string, ok bool) {
	switch t.Type {
	case core.TypeMovie:
		typ = TypeMovie
		if imdb := IMDbID(t.IDs.IMDB); imdb != "" {
			return typ, imdb, true
		}
		if t.IDs.TMDB > 0 {
			return typ, fmt.Sprintf("tmdb:%d", t.IDs.TMDB), true
		}
	case core.TypeEpisode:
		typ = TypeSeries
		base := IMDbID(t.ShowIDs.IMDB)
		if base == "" && t.ShowIDs.TMDB > 0 {
			base = fmt.Sprintf("tmdb:%d", t.ShowIDs.TMDB)
		}
		if base != "" {
			return typ, fmt.Sprintf("%s:%d:%d", base, t.Season, t.Episode), true
		}
	case core.TypeShow:
		typ = TypeSeries
		if imdb := IMDbID(t.IDs.IMDB); imdb != "" {
			return typ, imdb, true
		}
		if t.IDs.TMDB > 0 {
			return typ, fmt.Sprintf("tmdb:%d", t.IDs.TMDB), true
		}
	}
	return "", "", false
}
