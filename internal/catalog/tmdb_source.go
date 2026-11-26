package catalog

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type tmdbSource struct{ catalog *Catalog }

func NewTMDBSource(c *Catalog) core.MetadataSource { return tmdbSource{c} }
func (s tmdbSource) ID() core.MetadataSourceID     { return core.MetadataTMDB }
func (s tmdbSource) Capabilities() core.MetadataCaps {
	return core.MetadataCaps{Movies: true, Shows: true, People: true, Images: true, Search: true, Lists: true,
		EpisodeOrders: []string{core.OrderAired, "group:*"}}
}
func (s tmdbSource) Configured() bool {
	c := s.catalog.tmdb()
	if keyed, ok := c.(interface{ HasKey() bool }); ok {
		return keyed.HasKey()
	}
	return c != nil
}

func tmdbRefFor(id int64, typ core.MediaType) core.ProviderRef {
	return core.ProviderRef{Source: core.MetadataTMDB, ID: strconv.FormatInt(id, 10), Type: typ}
}

func tmdbRefID(ref core.ProviderRef) (int64, error) {
	id, err := strconv.ParseInt(ref.ID, 10, 64)
	if ref.Source != core.MetadataTMDB || err != nil || id <= 0 {
		return 0, core.ErrNotFound
	}
	return id, nil
}

func metadataDate(date string) time.Time {
	d, _ := time.Parse(time.DateOnly, date)
	return d
}

func tmdbHit(id int64, typ core.MediaType, title, summary, poster, date string, adult bool) core.MetadataHit {
	d := metadataDate(date)
	h := core.MetadataHit{Ref: tmdbRefFor(id, typ), IDs: core.IDs{TMDB: id}, Title: title, Summary: summary, PosterPath: poster, Released: d, Adult: adult}
	if !d.IsZero() {
		h.Year = d.Year()
	}
	return h
}

func movieHits(rows *tmdb.Paged[tmdb.Movie], err error, year int) ([]core.MetadataHit, error) {
	if err != nil || rows == nil {
		return nil, err
	}
	out := make([]core.MetadataHit, 0, len(rows.Results))
	for _, m := range rows.Results {
		h := tmdbHit(m.ID, core.TypeMovie, m.Title, m.Overview, m.PosterPath, m.ReleaseDate, m.Adult)
		if m.ID > 0 && !h.Adult && (year == 0 || h.Year == year) {
			out = append(out, h)
		}
	}
	return out, nil
}

func showHits(rows *tmdb.Paged[tmdb.TVShow], err error, year int) ([]core.MetadataHit, error) {
	if err != nil || rows == nil {
		return nil, err
	}
	out := make([]core.MetadataHit, 0, len(rows.Results))
	for _, t := range rows.Results {
		h := tmdbHit(t.ID, core.TypeShow, t.Name, t.Overview, t.PosterPath, t.FirstAirDate, t.Adult)
		if t.ID > 0 && !h.Adult && (year == 0 || h.Year == year) {
			out = append(out, h)
		}
	}
	return out, nil
}

func (s tmdbSource) Search(ctx context.Context, q core.MetadataSearch) ([]core.MetadataHit, error) {
	c := s.catalog.tmdb()
	if c == nil || !s.Configured() {
		return nil, core.ErrUnsupported
	}
	switch q.Type {
	case core.TypeMovie:
		var rows *tmdb.Paged[tmdb.Movie]
		var err error
		if localized, ok := c.(*tmdb.Client); ok {
			rows, err = localized.SearchMoviesInLanguage(ctx, q.Title, 1, q.Year, q.Language)
		} else {
			rows, err = c.SearchMovies(ctx, q.Title, 1)
		}
		return movieHits(rows, err, q.Year)
	case core.TypeShow:
		var rows *tmdb.Paged[tmdb.TVShow]
		var err error
		if localized, ok := c.(*tmdb.Client); ok {
			rows, err = localized.SearchTVInLanguage(ctx, q.Title, 1, q.Year, q.Language)
		} else {
			rows, err = c.SearchTV(ctx, q.Title, 1)
		}
		return showHits(rows, err, q.Year)
	case "":
		movies, err := s.Search(ctx, core.MetadataSearch{Title: q.Title, Year: q.Year, Type: core.TypeMovie, Language: q.Language})
		if err != nil {
			return nil, err
		}
		q.Type = core.TypeShow
		shows, err := s.Search(ctx, q)
		return append(movies, shows...), err
	}
	return nil, core.ErrUnsupported
}

func (s tmdbSource) Lookup(ctx context.Context, ref core.ProviderRef) (*core.Title, error) {
	c := s.catalog.tmdb()
	if c == nil || !s.Configured() {
		return nil, core.ErrUnsupported
	}
	id, err := tmdbRefID(ref)
	if err != nil {
		return nil, err
	}
	switch ref.Type {
	case core.TypeMovie:
		d, err := c.MovieDetails(ctx, id)
		if err != nil {
			return nil, err
		}
		if d == nil || d.ID != id {
			return nil, core.ErrNotFound
		}
		t := s.catalog.movieDetailsToTitle(ctx, d)
		return &t, nil
	case core.TypeShow:
		d, err := c.TVDetails(ctx, id)
		if err != nil {
			return nil, err
		}
		if d == nil || d.ID != id {
			return nil, core.ErrNotFound
		}
		t := s.catalog.tvDetailsToTitle(ctx, d)
		return &t, nil
	}
	return nil, core.ErrUnsupported
}

func (s tmdbSource) Seasons(ctx context.Context, ref core.ProviderRef, order core.EpisodeOrder) ([]core.SeasonSummary, error) {
	c := s.catalog.tmdb()
	if c == nil || !s.Configured() || ref.Type != core.TypeShow || order.Source != s.ID() {
		return nil, core.ErrUnsupported
	}
	id, err := tmdbRefID(ref)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(order.Kind, "group:") {
		g, err := s.catalog.metadataEpisodeGroup(ctx, id, strings.TrimPrefix(order.Kind, "group:"))
		if err != nil {
			return nil, err
		}
		offset := tmdb.GroupSeasonOffset(g)
		out := make([]core.SeasonSummary, 0, len(g.Groups))
		for _, sub := range g.Groups {
			summary := core.SeasonSummary{Ref: core.ProviderRef{Source: s.ID(), ID: sub.ID, Type: core.TypeSeason}, Season: sub.Order + offset, Title: sub.Name, EpisodeCount: len(sub.Episodes)}
			for _, e := range sub.Episodes {
				if e.AirDate != "" && (summary.AirDate == "" || e.AirDate < summary.AirDate) {
					summary.AirDate = e.AirDate
				}
			}
			out = append(out, summary)
		}
		return out, nil
	}
	if order.Kind != core.OrderAired {
		return nil, core.ErrUnsupported
	}
	d, err := c.TVDetails(ctx, id)
	if err != nil {
		return nil, err
	}
	if d == nil || d.ID != id {
		return nil, core.ErrNotFound
	}
	out := make([]core.SeasonSummary, 0, len(d.Seasons))
	for _, season := range d.Seasons {
		out = append(out, core.SeasonSummary{Ref: tmdbRefFor(season.ID, core.TypeSeason), Season: season.SeasonNumber, Title: season.Name, Summary: season.Overview, PosterPath: season.PosterPath, AirDate: season.AirDate, EpisodeCount: season.EpisodeCount})
	}
	return out, nil
}

func tmdbEpisode(e tmdb.Episode) core.EpisodeDetail {
	out := core.EpisodeDetail{Ref: tmdbRefFor(e.ID, core.TypeEpisode), IDs: core.IDs{TMDB: e.ID}, Season: e.SeasonNumber, Episode: e.EpisodeNumber,
		Aired: &core.EpisodeNumber{Season: e.SeasonNumber, Episode: e.EpisodeNumber}, AirDate: e.AirDate, Title: e.Name, Summary: e.Overview, StillPath: e.StillPath}
	if e.Runtime != nil {
		out.RuntimeMinutes = *e.Runtime
	}
	return out
}

func (s tmdbSource) Episodes(ctx context.Context, ref core.ProviderRef, order core.EpisodeOrder, season int) ([]core.EpisodeDetail, error) {
	c := s.catalog.tmdb()
	if c == nil || !s.Configured() || ref.Type != core.TypeShow || order.Source != s.ID() {
		return nil, core.ErrUnsupported
	}
	id, err := tmdbRefID(ref)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(order.Kind, "group:") {
		g, err := s.catalog.metadataEpisodeGroup(ctx, id, strings.TrimPrefix(order.Kind, "group:"))
		if err != nil {
			return nil, err
		}
		offset := tmdb.GroupSeasonOffset(g)
		for _, sub := range g.Groups {
			if sub.Order+offset != season {
				continue
			}
			out := make([]core.EpisodeDetail, 0, len(sub.Episodes))
			for _, e := range sub.Episodes {
				row := tmdbEpisode(e.Episode)
				row.Season, row.Episode = season, e.Order+1
				if g.Type == 2 && season > 0 {
					row.Absolute = row.Episode
				}
				out = append(out, row)
			}
			return out, nil
		}
		return nil, core.ErrNotFound
	}
	if order.Kind != core.OrderAired {
		return nil, core.ErrUnsupported
	}
	d, err := c.SeasonDetails(ctx, id, season)
	if err != nil {
		return nil, err
	}
	if d == nil || d.SeasonNumber != season {
		return nil, core.ErrNotFound
	}
	out := make([]core.EpisodeDetail, 0, len(d.Episodes))
	for _, e := range d.Episodes {
		out = append(out, tmdbEpisode(e))
	}
	return out, nil
}

func (s tmdbSource) FindByExternal(ctx context.Context, ids core.IDs, typ core.MediaType) ([]core.ProviderRef, error) {
	c := s.catalog.tmdb()
	if c == nil || !s.Configured() {
		return nil, core.ErrUnsupported
	}
	if typ != core.TypeMovie && typ != core.TypeShow && typ != core.TypeEpisode && typ != core.TypeSeason {
		return nil, core.ErrUnsupported
	}
	if ids.TMDB > 0 {
		return []core.ProviderRef{tmdbRefFor(ids.TMDB, typ)}, nil
	}
	out := []core.ProviderRef{}
	seen := map[int64]bool{}
	add := func(id int64) {
		if id > 0 && !seen[id] {
			out = append(out, tmdbRefFor(id, typ))
			seen[id] = true
		}
	}
	for _, external := range []struct{ source, id string }{{"imdb", ids.IMDB}, {"tvdb", strconv.FormatInt(ids.TVDB, 10)}} {
		if external.id == "" || external.id == "0" {
			continue
		}
		found, err := c.FindByExternalID(ctx, external.source, external.id)
		if err != nil {
			return nil, err
		}
		if found == nil {
			continue
		}
		switch typ {
		case core.TypeMovie:
			for _, m := range found.MovieResults {
				if !m.Adult {
					add(m.ID)
				}
			}
		case core.TypeShow:
			for _, t := range found.TVResults {
				if !t.Adult {
					add(t.ID)
				}
			}
		case core.TypeEpisode:
			for _, e := range found.TVEpisodeResults {
				add(e.ID)
			}
		case core.TypeSeason:
			for _, season := range found.TVSeasonResults {
				add(season.ID)
			}
		}
	}
	return out, nil
}

func (s tmdbSource) Images(ctx context.Context, ref core.ProviderRef) (core.Artwork, error) {
	c, ok := s.catalog.tmdb().(interface {
		ArtworkVariants(context.Context, *core.Title, string) (*tmdb.Images, error)
	})
	if !ok || !s.Configured() || (ref.Type != core.TypeMovie && ref.Type != core.TypeShow) {
		return core.Artwork{}, core.ErrUnsupported
	}
	id, err := tmdbRefID(ref)
	if err != nil {
		return core.Artwork{}, err
	}
	out, err := c.ArtworkVariants(ctx, &core.Title{Type: ref.Type, IDs: core.IDs{TMDB: id}}, s.catalog.config().Language)
	if out == nil {
		return core.Artwork{}, err
	}
	return *out, err
}

func (s tmdbSource) Person(ctx context.Context, ref core.ProviderRef) (*core.Person, error) {
	c, ok := s.catalog.tmdb().(interface {
		PersonDetails(context.Context, int64) (*tmdb.Person, error)
	})
	if !ok || !s.Configured() {
		return nil, core.ErrUnsupported
	}
	id, err := tmdbRefID(ref)
	if err != nil {
		return nil, err
	}
	p, err := c.PersonDetails(ctx, id)
	if err != nil {
		return nil, err
	}
	if p == nil || p.ID != id {
		return nil, core.ErrNotFound
	}
	return &core.Person{TMDB: id, IMDB: p.IMDBID, Name: p.Name, ThumbPath: p.ProfilePath}, nil
}

func (s tmdbSource) Lists(ctx context.Context, kind core.ListKind, typ core.MediaType, page int) ([]core.MetadataHit, error) {
	c := s.catalog.tmdb()
	if c == nil || !s.Configured() {
		return nil, core.ErrUnsupported
	}
	switch typ {
	case core.TypeMovie:
		var rows *tmdb.Paged[tmdb.Movie]
		var err error
		switch kind {
		case core.ListTrending:
			rows, err = c.TrendingMovies(ctx, "week", page)
		case core.ListPopular:
			rows, err = c.MovieList(ctx, "popular", page)
		case core.ListNew:
			rows, err = c.MovieList(ctx, "now_playing", page)
		default:
			return nil, core.ErrUnsupported
		}
		return movieHits(rows, err, 0)
	case core.TypeShow:
		var rows *tmdb.Paged[tmdb.TVShow]
		var err error
		switch kind {
		case core.ListTrending:
			rows, err = c.TrendingTV(ctx, "week", page)
		case core.ListPopular:
			rows, err = c.TVList(ctx, "popular", page)
		case core.ListNew:
			rows, err = c.TVList(ctx, "on_the_air", page)
		default:
			return nil, core.ErrUnsupported
		}
		return showHits(rows, err, 0)
	}
	return nil, core.ErrUnsupported
}
