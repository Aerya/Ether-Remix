package catalog

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// SortTitle returns the normalized lowercase sort key for a display title:
// strips invisible format characters, ignores leading non-alphanumerics,
// removes a leading English article ("the ", "a ", "an "), folds Latin
// accents/diacritics and ligatures to base ASCII letters, and applies numeric
// collation so every frontend's title sort and firstCharacter share one order.
func SortTitle(s string) string {
	return core.TitleSortKey(s)
}

func sortTitle(s string) string {
	return core.TitleSortKey(s)
}

func foldedSortTitleForRow(title, stored string) string {
	return core.FoldedSortTitleForRow(title, stored)
}

// FoldSortTitle normalizes an explicit sort title to lowercase with Latin
// diacritics and ligatures folded to base ASCII letters and numeric collation.
func FoldSortTitle(s string) string {
	return core.FoldSortTitle(s)
}

func foldSortTitle(s string) string {
	return core.FoldSortTitle(s)
}

func (c *Catalog) genreNames(ctx context.Context, mediaType string, ids []int, full []tmdb.Genre) []string {
	if len(full) > 0 {
		out := make([]string, 0, len(full))
		for _, g := range full {
			out = appendUnique(out, g.Name)
		}
		return out
	}
	if len(ids) == 0 {
		return nil
	}
	m := c.genreMap(ctx, mediaType)
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if n, ok := m[id]; ok {
			out = append(out, n)
		}
	}
	return out
}

func (c *Catalog) genreMap(ctx context.Context, mediaType string) map[int]string {
	c.genreMu.Lock()
	defer c.genreMu.Unlock()
	if m, ok := c.genres[mediaType]; ok {
		return m
	}
	m := map[int]string{}
	if c.tmdb() != nil {
		if gs, err := c.tmdb().Genres(ctx, mediaType); err == nil {
			for _, g := range gs {
				m[g.ID] = g.Name
			}
		} else {
			c.log.Debug("catalog: genre list failed", "type", mediaType, "err", err)
			return m // don't cache failures
		}
	}
	c.genres[mediaType] = m
	return m
}

// SeedGenres pre-populates the genre id->name cache (tests, offline warm-up).
func (c *Catalog) SeedGenres(mediaType string, m map[int]string) {
	c.genreMu.Lock()
	c.genres[mediaType] = m
	c.genreMu.Unlock()
}

func logoPath(img *tmdb.Images) string {
	if img == nil || len(img.Logos) == 0 {
		return ""
	}
	// Prefer English logos, then language-neutral, then any.
	for _, l := range img.Logos {
		if l.ISO639 == "en" {
			return l.FilePath
		}
	}
	for _, l := range img.Logos {
		if l.ISO639 == "" {
			return l.FilePath
		}
	}
	return img.Logos[0].FilePath
}

func creditsToPeople(cr *tmdb.Credits) (cast, directors, writers, crew []core.Person) {
	if cr == nil {
		return nil, nil, nil, nil
	}
	for _, m := range cr.Cast {
		role := m.Character
		if role == "" && len(m.Roles) > 0 {
			role = m.Roles[0].Character
		}
		cast = append(cast, core.Person{TMDB: m.ID, Name: m.Name, Role: role, ThumbPath: m.ProfilePath})
		if len(cast) >= 30 {
			break
		}
	}
	seenCrew := map[[2]any]bool{}
	addCrew := func(id int64, name, role, thumb string) {
		if id == 0 || name == "" || seenCrew[[2]any{id, role}] {
			return
		}
		seenCrew[[2]any{id, role}] = true
		crew = append(crew, core.Person{TMDB: id, Name: name, Role: role, ThumbPath: thumb})
	}
	var producers []core.Person
	seenProducer := map[int64]bool{}
	for _, m := range cr.Crew {
		job := m.Job
		if job == "" && len(m.Jobs) > 0 {
			job = m.Jobs[0].Job
		}
		p := core.Person{TMDB: m.ID, Name: m.Name, Role: job, ThumbPath: m.ProfilePath}
		switch strings.ToLower(job) {
		case "director":
			directors = append(directors, p)
		case "writer", "screenplay", "author", "novel", "story", "teleplay":
			writers = append(writers, p)
		case "director of photography", "cinematography":
			addCrew(m.ID, m.Name, "Cinematographer", m.ProfilePath)
		case "original music composer", "music", "composer":
			addCrew(m.ID, m.Name, "Composer", m.ProfilePath)
		case "creator", "series composition", "showrunner":
			addCrew(m.ID, m.Name, "Creator", m.ProfilePath)
		case "producer":
			if m.ID != 0 && m.Name != "" && !seenProducer[m.ID] && len(producers) < 12 {
				seenProducer[m.ID] = true
				producers = append(producers, core.Person{TMDB: m.ID, Name: m.Name, Role: "Producer", ThumbPath: m.ProfilePath})
			}
		}
	}
	crew = append(crew, producers...)
	return cast, directors, writers, crew
}

func companies(cs []tmdb.Company) []string {
	var out []string
	for _, co := range cs {
		if co.Name != "" {
			out = append(out, co.Name)
		}
	}
	return out
}
func countryNames(cs []tmdb.Country) []string {
	var out []string
	for _, co := range cs {
		if co.Name != "" {
			out = append(out, co.Name)
		}
	}
	return out
}

func (c *Catalog) movieToTitle(ctx context.Context, m tmdb.Movie) core.Title {
	rel := tmdb.ParseDate(m.ReleaseDate)
	t := core.Title{
		Key: core.MovieKey(m.ID), Type: core.TypeMovie, IDs: core.IDs{TMDB: m.ID},
		Title: m.Title, OriginalTitle: m.OriginalTitle, SortTitle: sortTitle(m.Title),
		Summary: m.Overview, Released: rel, Language: m.OriginalLanguage,
		Rating: m.VoteAverage, VoteCount: m.VoteCount, Popularity: m.Popularity,
		PosterPath: m.PosterPath, BackdropPath: m.BackdropPath,
		Genres: c.genreNames(ctx, "movie", m.GenreIDs, m.Genres), Adult: m.Adult,
	}
	if m.Adult {
		t.EnforcementRating = contentrating.UncertifiedAdult
	}
	if !rel.IsZero() {
		t.Year = rel.Year()
	}
	return t
}

// earliestDatedRelease is the earliest calendar day among the given TMDB
// release types. Type 1 is a premiere, 2 a limited theatrical release and 3
// a theatrical release. Home-video types are not title years or date added.
func earliestDatedRelease(dates *tmdb.ReleaseDates, kinds ...int) time.Time {
	if dates == nil {
		return time.Time{}
	}
	want := map[int]bool{}
	for _, kind := range kinds {
		want[kind] = true
	}
	var earliest time.Time
	for _, country := range dates.Results {
		for _, release := range country.ReleaseDate {
			if !want[release.Type] {
				continue
			}
			date := tmdb.ParseDate(strings.SplitN(release.ReleaseDate, "T", 2)[0])
			if !date.IsZero() && (earliest.IsZero() || date.Before(earliest)) {
				earliest = date
			}
		}
	}
	return earliest
}

const (
	yearSourcePrimary  = "primary"
	yearSourcePremiere = "premiere"
	yearSourceReleased = "released"
	yearSourceNone     = "none"
	yearSourceMissing  = "missing"
)

// applyMovieYear sets the title year from the primary release_date, then
// the earliest premiere, then the date already chosen as date added. It
// records that choice so a later summary cannot put the theatrical year
// back, and so an upgrade does not fetch the movie again.
func applyMovieYear(t *core.Title, primary time.Time, dates *tmdb.ReleaseDates) {
	if !primary.IsZero() {
		t.Year = primary.Year()
		t.PrimaryReleased = primary
		t.YearSource = yearSourcePrimary
		return
	}
	t.PrimaryReleased = time.Time{}
	if premiere := earliestDatedRelease(dates, 1); !premiere.IsZero() {
		t.Year = premiere.Year()
		t.YearSource = yearSourcePremiere
		return
	}
	if !t.Released.IsZero() {
		t.Year = t.Released.Year()
		t.YearSource = yearSourceReleased
		return
	}
	t.Year = 0
	t.YearSource = yearSourceNone
}

func (c *Catalog) movieDetailsToTitle(ctx context.Context, d *tmdb.MovieDetails) core.Title {
	t := c.movieToTitle(ctx, d.Movie)
	c.applyTranslatedNames(&t, d.Translations, d.AlternativeTitles)
	// Date added is the earliest theatrical date. The title year is the
	// primary release_date, which is what TMDB, IMDb and Plex match on.
	// A later regional theatrical date must not move that year. With no
	// primary date, the earliest premiere supplies the year.
	if theatrical := earliestDatedRelease(d.ReleaseDates, 2, 3); !theatrical.IsZero() {
		t.Released = theatrical
	}
	applyMovieYear(&t, tmdb.ParseDate(d.ReleaseDate), d.ReleaseDates)
	t.Keywords = keywordNames(d.Keywords)
	t.Tagline = d.Tagline
	t.Status = d.Status
	if d.Runtime > 0 {
		t.Runtime = time.Duration(d.Runtime) * time.Minute
	}
	t.IDs.IMDB = d.IMDBID
	if d.ExternalIDs != nil {
		if d.ExternalIDs.IMDBID != "" {
			t.IDs.IMDB = d.ExternalIDs.IMDBID
		}
		t.IDs.TVDB = d.ExternalIDs.TVDBID
	}
	t.Studios = companies(d.ProductionCompany)
	t.Countries = countryNames(d.ProductionCountry)
	t.Cast, t.Directors, t.Writers, t.Crew = creditsToPeople(d.Credits)
	t.LogoPath = logoPath(d.Images)
	if d.BelongsTo != nil {
		t.CollectionID = d.BelongsTo.ID
		t.CollectionName = d.BelongsTo.Name
	}
	certs := movieCertifications(d.ReleaseDates)
	region := c.config().Region
	sel := contentrating.Select(certs, region)
	t.ContentRating, t.ContentRatingSource = sel.Value, sel.Source
	t.ContentRatings = contentrating.ByCountry(certs)
	t.EnforcementRating = contentrating.EffectiveEnforcement(contentrating.EnforceCertifications(certs, region), sel.Value, t.Adult, t.Keywords)
	return t
}

func (c *Catalog) tvToTitle(ctx context.Context, s tmdb.TVShow) core.Title {
	rel := tmdb.ParseDate(s.FirstAirDate)
	t := core.Title{
		Key: core.ShowKey(s.ID), Type: core.TypeShow, IDs: core.IDs{TMDB: s.ID},
		Title: s.Name, OriginalTitle: s.OriginalName, SortTitle: sortTitle(s.Name),
		Summary: s.Overview, Released: rel, Language: s.OriginalLanguage,
		Rating: s.VoteAverage, VoteCount: s.VoteCount, Popularity: s.Popularity,
		PosterPath: s.PosterPath, BackdropPath: s.BackdropPath,
		Genres: c.genreNames(ctx, "tv", s.GenreIDs, s.Genres), Countries: s.OriginCountry, Adult: s.Adult,
	}
	if s.Adult {
		t.EnforcementRating = contentrating.UncertifiedAdult
	}
	if !rel.IsZero() {
		t.Year = rel.Year()
	}
	return t
}

func (c *Catalog) tvDetailsToTitle(ctx context.Context, d *tmdb.TVDetails) core.Title {
	t := c.tvToTitle(ctx, d.TVShow)
	c.applyTranslatedNames(&t, d.Translations, d.AlternativeTitles)
	t.Keywords = keywordNames(d.Keywords)
	t.Tagline = d.Tagline
	t.Status = d.Status
	t.LastAirDate = tmdb.ParseDate(d.LastAirDate)
	if len(d.EpisodeRunTime) > 0 && d.EpisodeRunTime[0] > 0 {
		t.Runtime = time.Duration(d.EpisodeRunTime[0]) * time.Minute
	}
	if d.ExternalIDs != nil {
		t.IDs.IMDB = d.ExternalIDs.IMDBID
		t.IDs.TVDB = d.ExternalIDs.TVDBID
	}
	t.Studios = companies(d.ProductionCompany)
	for _, n := range d.Networks {
		if n.Name == "" {
			continue
		}
		dup := false
		for _, s := range t.Studios {
			if strings.EqualFold(s, n.Name) {
				dup = true
				break
			}
		}
		if !dup {
			t.Studios = append(t.Studios, n.Name)
		}
	}
	if names := countryNames(d.ProductionCountry); len(names) > 0 {
		t.Countries = names
	}
	credits := d.Credits
	if credits == nil || (len(credits.Cast) == 0 && d.AggregateCredits != nil) {
		credits = d.AggregateCredits
	}
	t.Cast, t.Directors, t.Writers, t.Crew = creditsToPeople(credits)
	// Aggregate credits supplement cast when regular credits are sparse.
	if d.AggregateCredits != nil && len(t.Cast) == 0 {
		t.Cast, _, _, _ = creditsToPeople(d.AggregateCredits)
	}
	if len(d.CreatedBy) > 0 {
		var creators []core.Person
		seen := map[int64]bool{}
		for _, cb := range d.CreatedBy {
			if cb.ID != 0 && cb.Name != "" && !seen[cb.ID] {
				seen[cb.ID] = true
				creators = append(creators, core.Person{TMDB: cb.ID, Name: cb.Name, Role: "Creator", ThumbPath: cb.ProfilePath})
			}
		}
		for _, p := range t.Crew {
			if !(p.Role == "Creator" && seen[p.TMDB]) {
				creators = append(creators, p)
			}
		}
		t.Crew = creators
	}
	t.LogoPath = logoPath(d.Images)
	t.ChildCount = d.NumberOfSeasons
	t.LeafCount = d.NumberOfEpisodes
	certs := tvCertifications(d.ContentRatings)
	region := c.config().Region
	sel := contentrating.Select(certs, region)
	t.ContentRating, t.ContentRatingSource = sel.Value, sel.Source
	t.ContentRatings = contentrating.ByCountry(certs)
	t.EnforcementRating = contentrating.EffectiveEnforcement(contentrating.EnforceCertifications(certs, region), sel.Value, t.Adult, t.Keywords)
	return t
}

func seasonToTitle(show core.Title, s tmdb.SeasonSummary) core.Title {
	rel := tmdb.ParseDate(s.AirDate)
	name := s.Name
	if name == "" {
		if s.SeasonNumber == 0 {
			name = "Specials"
		} else {
			name = "Season " + itoa(s.SeasonNumber)
		}
	}
	t := core.Title{
		Key: core.SeasonKey(show.IDs.TMDB, s.SeasonNumber), Type: core.TypeSeason,
		IDs: core.IDs{TMDB: s.ID}, ShowKey: show.Key, ShowIDs: show.IDs, ShowTitle: show.Title, ShowOriginalTitle: show.OriginalTitle, ShowEnglishTitle: show.EnglishTitle, ShowYear: show.Year,
		SeasonTitle: name, Season: s.SeasonNumber, Title: name, SortTitle: sortTitle(name),
		Summary: s.Overview, Released: rel, PosterPath: s.PosterPath, BackdropPath: show.BackdropPath,
		ChildCount: s.EpisodeCount, Rating: s.VoteAverage,
		Genres: show.Genres, ContentRating: show.ContentRating, ContentRatings: show.ContentRatings, EnforcementRating: show.EnforcementRating, ContentRatingSource: show.ContentRatingSource, Language: show.Language, Adult: show.Adult,
	}
	if t.PosterPath == "" {
		t.PosterPath = show.PosterPath
	}
	if !rel.IsZero() {
		t.Year = rel.Year()
	}
	return t
}

func episodeToTitle(show core.Title, seasonName string, e tmdb.Episode) core.Title {
	rel := tmdb.ParseDate(e.AirDate)
	title := e.Name
	if title == "" {
		title = "Episode " + itoa(e.EpisodeNumber)
	}
	t := core.Title{
		Key: core.EpisodeKey(show.IDs.TMDB, e.SeasonNumber, e.EpisodeNumber), Type: core.TypeEpisode,
		IDs: core.IDs{TMDB: e.ID}, ShowKey: show.Key, ShowIDs: show.IDs, ShowTitle: show.Title, ShowOriginalTitle: show.OriginalTitle, ShowEnglishTitle: show.EnglishTitle, ShowYear: show.Year,
		SeasonTitle: seasonName, Season: e.SeasonNumber, Episode: e.EpisodeNumber,
		Title: title, SortTitle: sortTitle(title), Summary: e.Overview, Released: rel,
		StillPath: e.StillPath, PosterPath: e.StillPath, BackdropPath: show.BackdropPath,
		Rating: e.VoteAverage, VoteCount: e.VoteCount,
		Genres: show.Genres, ContentRating: show.ContentRating, ContentRatings: show.ContentRatings, EnforcementRating: show.EnforcementRating, ContentRatingSource: show.ContentRatingSource, Language: show.Language, Adult: show.Adult,
	}
	if e.Runtime != nil && *e.Runtime > 0 {
		t.Runtime = time.Duration(*e.Runtime) * time.Minute
	} else if show.Runtime > 0 {
		t.Runtime = show.Runtime
	}
	if !rel.IsZero() {
		t.Year = rel.Year()
	}
	// Crew directors/writers for the episode.
	for _, m := range e.Crew {
		p := core.Person{TMDB: m.ID, Name: m.Name, Role: m.Job, ThumbPath: m.ProfilePath}
		switch strings.ToLower(m.Job) {
		case "director":
			t.Directors = append(t.Directors, p)
		case "writer", "screenplay":
			t.Writers = append(t.Writers, p)
		}
	}
	for _, m := range e.GuestStars {
		t.Cast = append(t.Cast, core.Person{TMDB: m.ID, Name: m.Name, Role: m.Character, ThumbPath: m.ProfilePath})
	}
	return t
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// maxAltTitles bounds the alternative titles kept per title; maxAltTitleLen
// bounds each one. Popular titles list a hundred or more.
const (
	maxAltTitles   = 24
	maxAltTitleLen = 200
)

// altTitles keeps the alternative titles release names can use: written in
// Latin script, plus preferred localized names in any script; not the title
// or original title again, unique after normalization, at most maxAltTitles.
// Romanized originals ("Sousou no Frieren") are what anime and foreign
// releases are named by. Non-nil when TMDB sent the block, so an empty list
// replaces stale rows.
func altTitles(a *tmdb.AltTitles, title, original string, preferred ...string) []string {
	if a == nil && preferred == nil {
		return nil
	}
	out := []string{}
	seen := map[string]bool{releaseparse.NormalizeTitle(title): true, releaseparse.NormalizeTitle(original): true}
	// Requested-language translations and regional names go first, including
	// native scripts. Other aliases keep the existing Latin-script policy.
	lists := [][]tmdb.AltTitle{{}}
	for _, name := range preferred {
		lists[0] = append(lists[0], tmdb.AltTitle{Title: name})
	}
	if a != nil {
		lists = append(lists, a.Titles, a.Results)
	}
	for i, list := range lists {
		for _, alt := range list {
			name := strings.TrimSpace(alt.Title)
			if name == "" || len(name) > maxAltTitleLen || (i > 0 && !latinScript(name)) {
				continue
			}
			norm := releaseparse.NormalizeTitle(name)
			if norm == "" || seen[norm] {
				continue
			}
			seen[norm] = true
			out = append(out, name)
			if len(out) == maxAltTitles {
				return out
			}
		}
	}
	return out
}

// latinScript reports a name whose letters are all Latin.
func latinScript(s string) bool {
	letters := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			if !unicode.Is(unicode.Latin, r) {
				return false
			}
			letters++
		}
	}
	return letters > 0
}

// keywordNames flattens TMDB keywords (movies use "keywords", TV "results").
// It returns a non-nil slice when TMDB sent the block, so an empty list
// still replaces stale rows.
func keywordNames(k *tmdb.Keywords) []string {
	if k == nil {
		return nil
	}
	out := []string{}
	for _, list := range [][]tmdb.Keyword{k.Keywords, k.Results} {
		for _, kw := range list {
			out = appendUnique(out, kw.Name)
		}
	}
	return out
}
