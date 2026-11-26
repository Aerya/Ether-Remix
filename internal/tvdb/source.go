package tvdb

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

const personType core.MediaType = "person"

var _ core.MetadataSource = (*Client)(nil)

func (c *Client) ID() core.MetadataSourceID { return core.MetadataTVDB }
func (c *Client) Configured() bool          { return c.key() != "" }

func (c *Client) Capabilities() core.MetadataCaps {
	return core.MetadataCaps{
		Movies: true, Shows: true, People: true, Images: true, Search: true,
		EpisodeOrders: []string{core.OrderAired, core.OrderDVD, core.OrderAbsolute, "alternate", "regional"},
	}
}

func (c *Client) Lists(context.Context, core.ListKind, core.MediaType, int) ([]core.MetadataHit, error) {
	return nil, core.ErrUnsupported
}

func reference(id int64, typ core.MediaType) core.ProviderRef {
	return core.ProviderRef{Source: core.MetadataTVDB, ID: strconv.FormatInt(id, 10), Type: typ}
}

func resource(typ core.MediaType) string {
	switch typ {
	case core.TypeMovie:
		return "movies"
	case core.TypeShow:
		return "series"
	case core.TypeSeason:
		return "seasons"
	case core.TypeEpisode:
		return "episodes"
	case personType:
		return "people"
	default:
		return ""
	}
}

func recordPath(ref core.ProviderRef) (string, int64, error) {
	kind := resource(ref.Type)
	if ref.Source != core.MetadataTVDB || kind == "" {
		return "", 0, core.ErrUnsupported
	}
	id, err := strconv.ParseInt(ref.ID, 10, 64)
	if err != nil || id <= 0 {
		return "", 0, core.ErrNotFound
	}
	return "/" + kind + "/" + strconv.FormatInt(id, 10), id, nil
}

func (c *Client) Search(ctx context.Context, q core.MetadataSearch) ([]core.MetadataHit, error) {
	query := strings.TrimSpace(q.Title)
	if query == "" {
		return nil, nil
	}
	params := url.Values{"query": {query}, "limit": {"30"}}
	switch q.Type {
	case core.TypeMovie, personType:
		params.Set("type", string(q.Type))
	case core.TypeShow:
		params.Set("type", "series")
	case "":
	default:
		return nil, core.ErrUnsupported
	}
	if q.Year > 0 {
		params.Set("year", strconv.Itoa(q.Year))
	}
	records, _, err := get[[]searchRecord](ctx, c, "/search?"+params.Encode(), cachePolicy{fresh: time.Hour})
	if errors.Is(err, core.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lang := c.language(q.Language)
	var hits []core.MetadataHit
	for _, record := range records {
		var typ core.MediaType
		switch record.Type {
		case "movie":
			typ = core.TypeMovie
		case "series":
			typ = core.TypeShow
		case "person":
			typ = personType
		default:
			continue
		}
		id, err := strconv.ParseInt(record.ID, 10, 64)
		if err != nil || id <= 0 || (q.Type != "" && q.Type != typ) {
			continue
		}
		released := date(record.FirstAired)
		hits = append(hits, core.MetadataHit{
			Ref: reference(id, typ), IDs: externalIDs(id, record.RemoteIDs),
			Title:   first(record.Translations[lang], record.Translations["eng"], record.Name),
			Summary: first(record.Overviews[lang], record.Overviews["eng"], record.Overview),
			Year:    year(record.Year, released), Released: released, PosterPath: imageURL(record.Image),
		})
	}
	return hits, nil
}

func (c *Client) extended(ctx context.Context, ref core.ProviderRef, lang string) (record, string, error) {
	path, id, err := recordPath(ref)
	if err != nil {
		return record{}, "", err
	}
	query := "?meta=translations"
	switch ref.Type {
	case core.TypeShow:
		query += "&language=" + lang
	case core.TypeSeason:
		query = ""
	}
	out, _, err := get[record](ctx, c, path+"/extended"+query, entityPolicy(resource(ref.Type), id))
	if err == nil && out.ID != id {
		err = ErrInvalidResponse
	}
	return out, path, err
}

func (c *Client) Lookup(ctx context.Context, ref core.ProviderRef) (*core.Title, error) {
	if ref.Type == personType {
		return nil, core.ErrUnsupported
	}
	lang := c.language("")
	r, path, err := c.extended(ctx, ref, lang)
	if err != nil {
		return nil, err
	}
	localized, english, err := c.localized(ctx, path, resource(ref.Type), r, lang)
	if err != nil {
		return nil, err
	}
	title := r.title(ref.Type, localized, english)
	if ref.Type == core.TypeSeason {
		title.Season = r.Number
		title.ShowIDs.TVDB = r.SeriesID
	}
	images, err := c.images(ctx, ref.Type, r, lang)
	if err != nil {
		return nil, err
	}
	title.PosterPath = firstImage(images.Posters)
	title.BackdropPath = firstImage(images.Backdrops)
	title.LogoPath = firstImage(images.Logos)
	if ref.Type == core.TypeEpisode {
		title.StillPath = firstImage(images.Still)
	} else {
		title.ThumbPath = firstImage(images.Still)
	}
	if err := c.credits(ctx, r.Characters, title); err != nil {
		return nil, err
	}
	return title, nil
}

func (c *Client) localized(ctx context.Context, path, kind string, r record, lang string) (translation, translation, error) {
	local := r.Translations.text(lang)
	english := r.Translations.text("eng")
	original := iso3(r.OriginalLanguage)
	resolve := func(lang string, text translation) (translation, error) {
		if lang == original {
			text.Name = first(text.Name, r.Name)
			text.Overview = first(text.Overview, r.Overview)
			return text, nil
		}
		if (text.Name != "" || !slices.Contains(r.NameTranslations, lang)) &&
			(text.Overview != "" || !slices.Contains(r.OverviewTranslations, lang)) {
			return text, nil
		}
		got, _, err := get[translation](ctx, c, path+"/translations/"+lang, entityPolicy(kind, r.ID))
		if errors.Is(err, core.ErrNotFound) {
			return text, nil
		}
		if err == nil {
			text.Name = first(text.Name, got.Name)
			text.Overview = first(text.Overview, got.Overview)
			text.Tagline = first(text.Tagline, got.Tagline)
		}
		return text, err
	}
	english, err := resolve("eng", english)
	if err != nil {
		return local, english, err
	}
	if lang == "eng" {
		return english, english, nil
	}
	local, err = resolve(lang, local)
	return local, english, err
}

func (c *Client) Person(ctx context.Context, ref core.ProviderRef) (*core.Person, error) {
	if ref.Type == "" {
		ref.Type = personType
	}
	if ref.Type != personType {
		return nil, core.ErrUnsupported
	}
	lang := c.language("")
	r, path, err := c.extended(ctx, ref, lang)
	if err != nil {
		return nil, err
	}
	localized, english, err := c.localized(ctx, path, "people", r, lang)
	if err != nil {
		return nil, err
	}
	ids := externalIDs(r.ID, r.RemoteIDs)
	return &core.Person{ID: "tvdb:" + strconv.FormatInt(r.ID, 10), TMDB: ids.TMDB, IMDB: ids.IMDB,
		Name: first(localized.Name, english.Name, r.Name), ThumbPath: imageURL(r.Image)}, nil
}

type externalMatch struct {
	Movie   idRecord
	Series  idRecord
	Episode idRecord
	People  idRecord
	Season  idRecord
}

func (m externalMatch) id(typ core.MediaType) int64 {
	switch typ {
	case core.TypeMovie:
		return m.Movie.ID
	case core.TypeShow:
		return m.Series.ID
	case core.TypeEpisode:
		return m.Episode.ID
	case core.TypeSeason:
		return m.Season.ID
	case personType:
		return m.People.ID
	}
	return 0
}

func (c *Client) FindByExternal(ctx context.Context, ids core.IDs, typ core.MediaType) ([]core.ProviderRef, error) {
	if resource(typ) == "" {
		return nil, core.ErrUnsupported
	}
	if !c.Configured() {
		return nil, ErrKeyRequired
	}
	if ids.TVDB > 0 {
		return []core.ProviderRef{reference(ids.TVDB, typ)}, nil
	}
	if imdbID(ids.IMDB) {
		refs, err := c.findRemote(ctx, ids.IMDB, typ, 0)
		if err != nil || len(refs) > 0 {
			return refs, err
		}
	}
	if ids.TMDB > 0 {
		return c.findRemote(ctx, strconv.FormatInt(ids.TMDB, 10), typ, ids.TMDB)
	}
	return nil, nil
}

func (c *Client) findRemote(ctx context.Context, id string, typ core.MediaType, tmdb int64) ([]core.ProviderRef, error) {
	found, _, err := get[[]externalMatch](ctx, c, "/search/remoteid/"+id, cachePolicy{fresh: time.Hour})
	if errors.Is(err, core.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var refs []core.ProviderRef
	seen := make(map[int64]bool)
	lang := c.language("")
	for _, match := range found {
		candidate := match.id(typ)
		if candidate <= 0 || seen[candidate] {
			continue
		}
		seen[candidate] = true
		ref := reference(candidate, typ)
		if tmdb > 0 {
			// Numeric remote IDs are shared by several sites. Verify the
			// provider name on the candidate before returning its identity.
			r, _, err := c.extended(ctx, ref, lang)
			if errors.Is(err, core.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if externalIDs(r.ID, r.RemoteIDs).TMDB != tmdb {
				continue
			}
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// SeriesPrefix reads a bounded series catalogue for explicit library membership.
// The list's base rows do not require one detail request per entry.
func (c *Client) SeriesPrefix(ctx context.Context, limit int) ([]core.MetadataHit, error) {
	if limit <= 0 {
		return nil, nil
	}
	var hits []core.MetadataHit
	seen := make(map[int64]bool)
	for page := 0; page < 5 && len(hits) < limit; page++ {
		records, links, err := get[[]record](ctx, c, "/series?page="+strconv.Itoa(page), cachePolicy{fresh: time.Hour})
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if record.ID <= 0 || seen[record.ID] {
				continue
			}
			seen[record.ID] = true
			released := record.releaseDate()
			hits = append(hits, core.MetadataHit{
				Ref: reference(record.ID, core.TypeShow), IDs: externalIDs(record.ID, record.RemoteIDs),
				Title: record.Name, Summary: record.Overview, Year: year(record.Year, released),
				Released: released, PosterPath: imageURL(record.Image),
			})
			if len(hits) == limit {
				return hits, nil
			}
		}
		if links.Next == "" || len(records) == 0 {
			break
		}
	}
	return hits, nil
}
