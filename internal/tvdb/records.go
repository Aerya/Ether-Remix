package tvdb

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"golang.org/x/text/language"
)

type idRecord struct{ ID int64 }
type namedRecord struct {
	ID   int64
	Name string
}
type remoteID struct {
	ID         string
	SourceName string
}
type translation struct {
	Language string
	Name     string
	Overview string
	Tagline  string
	IsAlias  bool
}
type translations struct {
	Names     []translation `json:"nameTranslations"`
	Overviews []translation `json:"overviewTranslations"`
}

// Seasons may return a flat list of translations; the other extended
// resources return separate name and overview lists.
func (t *translations) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '[' {
		var entries []translation
		if err := json.Unmarshal(data, &entries); err != nil {
			return err
		}
		*t = translations{Names: entries, Overviews: entries}
		return nil
	}
	type object translations
	return json.Unmarshal(data, (*object)(t))
}

type artworkRecord struct {
	Image, Thumbnail, Language string
	Type                       int64
	Width, Height              int
	Score                      float64
}

type character struct {
	Name, PersonName, PeopleType, PersonImgURL, Image string
	PeopleID, Type                                    int64
	Sort                                              int
}

type record struct {
	ID, SeriesID                         int64
	Name, Overview, Image, Year          string
	FirstAired, LastAired, Aired         string
	OriginalLanguage, OriginalCountry    string
	Score                                float64
	Runtime, AverageRuntime              int
	Number, SeasonNumber, AbsoluteNumber int
	Status                               namedRecord
	Genres, Studios                      []namedRecord
	FirstRelease                         struct{ Date string } `json:"first_release"`
	NameTranslations                     []string
	OverviewTranslations                 []string
	Translations                         translations
	Aliases                              []struct{ Name string }
	RemoteIDs                            []remoteID
	Artworks                             []artworkRecord
	Artwork                              []artworkRecord // season artwork uses a singular API field
	Characters                           []character
	Seasons                              []seasonRecord
	ContentRatings                       []struct {
		Country, Name string
	}
}

type seasonRecord struct {
	ID               int64
	Number           int
	Name, Image      string
	Type             struct{ Type string }
	NameTranslations []string
}

type searchRecord struct {
	ID           string `json:"tvdb_id"`
	Type         string
	Name         string
	Overview     string
	Image        string `json:"image_url"`
	Year         string
	FirstAired   string `json:"first_air_time"`
	Translations map[string]string
	Overviews    map[string]string
	RemoteIDs    []remoteID `json:"remote_ids"`
}

func iso3(locale string) string {
	if locale == "pt" || locale == "zhtw" {
		return locale // TheTVDB also uses these identifiers for translations.
	}
	tag, err := language.Parse(locale)
	if err != nil || tag == language.Und {
		return ""
	}
	base, _ := tag.Base()
	switch base.String() {
	case "nb":
		return "nor"
	case "pt":
		if region, confidence := tag.Region(); confidence == language.Exact && region.String() == "PT" {
			return "pt"
		}
	case "zh":
		if script, _ := tag.Script(); script.String() == "Hant" {
			return "zhtw"
		}
	}
	return base.ISO3()
}

func iso2(locale string) string {
	if locale == "zhtw" {
		return "zh"
	}
	tag, err := language.Parse(locale)
	if err != nil || tag == language.Und {
		return ""
	}
	base, _ := tag.Base()
	return base.String()
}

func (c *Client) language(override string) string {
	if override == "" {
		_, override = c.settings()
	}
	return first(iso3(override), "eng")
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (t translations) text(lang string) (out translation) {
	for _, tr := range t.Names {
		if tr.Language == lang && !tr.IsAlias {
			out.Name = first(out.Name, tr.Name)
			out.Tagline = first(out.Tagline, tr.Tagline)
		}
	}
	for _, tr := range t.Overviews {
		if tr.Language == lang {
			out.Overview = first(out.Overview, tr.Overview)
		}
	}
	return out
}

func date(raw string) time.Time {
	if parsed, err := time.Parse(time.DateOnly, raw); err == nil {
		return parsed
	}
	return time.Time{}
}

func year(raw string, released time.Time) int {
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n
	}
	if released.IsZero() {
		return 0
	}
	return released.Year()
}

func imdbID(id string) bool {
	digits, ok := strings.CutPrefix(id, "tt")
	if !ok {
		digits, ok = strings.CutPrefix(id, "nm")
	}
	if !ok || digits == "" {
		return false
	}
	for _, ch := range digits {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

func externalIDs(id int64, remote []remoteID) core.IDs {
	ids := core.IDs{TVDB: id}
	for _, item := range remote {
		switch strings.ToLower(strings.TrimSpace(item.SourceName)) {
		case "imdb":
			if imdbID(item.ID) {
				ids.IMDB = item.ID
			}
		case "themoviedb.com", "themoviedb", "tmdb":
			if n, err := strconv.ParseInt(item.ID, 10, 64); err == nil && n > 0 {
				ids.TMDB = n
			}
		}
	}
	return ids
}

const artworkHost = "artworks.thetvdb.com"

func imageURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Path == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	if u.Host == "" && u.Scheme == "" {
		u.Host = artworkHost
		u.Scheme = "https"
		u.Path = "/" + strings.TrimLeft(u.Path, "/")
		if !strings.HasPrefix(u.Path, "/banners/") {
			u.Path = "/banners" + u.Path
		}
		u.RawPath = ""
	}
	if u.Host != artworkHost || strings.HasPrefix(u.Path, "/banners/images/missing/") ||
		(u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "") {
		return ""
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == ".." || part == "." || strings.Contains(part, "\\") {
			return ""
		}
	}
	u.Scheme = "https"
	return u.String()
}

func (r record) releaseDate() time.Time {
	for _, raw := range []string{r.FirstRelease.Date, r.FirstAired, r.Aired} {
		if released := date(raw); !released.IsZero() {
			return released
		}
	}
	return time.Time{}
}

func (r record) title(typ core.MediaType, localized, english translation) *core.Title {
	if iso3(r.OriginalLanguage) == "eng" {
		english.Name = first(english.Name, r.Name)
	}
	released := r.releaseDate()
	out := &core.Title{
		Type: typ, IDs: externalIDs(r.ID, r.RemoteIDs),
		Title:         first(localized.Name, english.Name, r.Name),
		OriginalTitle: first(r.Translations.text(iso3(r.OriginalLanguage)).Name, r.Name),
		EnglishTitle:  english.Name, Summary: first(localized.Overview, english.Overview, r.Overview),
		Tagline: first(localized.Tagline, english.Tagline), Year: year(r.Year, released),
		Released: released, PrimaryReleased: released, LastAirDate: date(r.LastAired),
		Season: r.SeasonNumber, Episode: r.Number, AbsoluteEpisode: r.AbsoluteNumber,
		Status: r.Status.Name, Runtime: time.Duration(max(r.Runtime, r.AverageRuntime)) * time.Minute,
		Popularity: r.Score, Language: iso2(r.OriginalLanguage),
	}
	if typ == core.TypeEpisode {
		out.ShowIDs.TVDB = r.SeriesID
	}
	if region, err := language.ParseRegion(r.OriginalCountry); err == nil {
		out.Countries = []string{region.String()}
	}
	for _, alias := range r.Aliases {
		if alias.Name != "" {
			out.AlternativeTitles = append(out.AlternativeTitles, alias.Name)
		}
	}
	for _, genre := range r.Genres {
		if genre.Name != "" {
			out.Genres = append(out.Genres, genre.Name)
		}
	}
	for _, studio := range r.Studios {
		if studio.Name != "" {
			out.Studios = append(out.Studios, studio.Name)
		}
	}
	for _, rating := range r.ContentRatings {
		region, err := language.ParseRegion(rating.Country)
		if err != nil || rating.Name == "" {
			continue
		}
		if out.ContentRatings == nil {
			out.ContentRatings = make(map[string]string)
		}
		out.ContentRatings[region.String()] = rating.Name
		if region.String() == "US" {
			out.ContentRating = rating.Name
			out.ContentRatingSource = "tvdb:US"
		}
	}
	return out
}
