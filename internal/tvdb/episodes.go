package tvdb

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func seasonType(order core.EpisodeOrder) (string, error) {
	if order.Source != "" && order.Source != core.MetadataTVDB {
		return "", core.ErrUnsupported
	}
	switch order.Kind {
	case "", core.OrderAired, "official":
		return "official", nil
	case core.OrderDVD, core.OrderAbsolute, "alternate", "regional":
		return order.Kind, nil
	default:
		return "", core.ErrUnsupported
	}
}

func (c *Client) Seasons(ctx context.Context, show core.ProviderRef, order core.EpisodeOrder) ([]core.SeasonSummary, error) {
	if show.Type != core.TypeShow {
		return nil, core.ErrUnsupported
	}
	kind, err := seasonType(order)
	if err != nil {
		return nil, err
	}
	lang := c.language("")
	r, _, err := c.extended(ctx, show, lang)
	if err != nil {
		return nil, err
	}
	needsEnglish := func(s seasonRecord) bool {
		return lang != "eng" && s.Type.Type == kind && !slices.Contains(s.NameTranslations, lang) && slices.Contains(s.NameTranslations, "eng")
	}
	englishNames := make(map[int64]string)
	if slices.ContainsFunc(r.Seasons, needsEnglish) {
		english, _, err := c.extended(ctx, show, "eng")
		if err != nil {
			return nil, err
		}
		for _, s := range english.Seasons {
			englishNames[s.ID] = s.Name
		}
	}
	var out []core.SeasonSummary
	for _, s := range r.Seasons {
		if s.ID <= 0 || s.Number < 0 || s.Type.Type != kind {
			continue
		}
		name := s.Name
		if needsEnglish(s) {
			name = first(englishNames[s.ID], name)
		}
		count, airDate := c.cachedSeason(episodePrefix(r.ID, kind, s.Number))
		item := core.SeasonSummary{
			Ref: reference(s.ID, core.TypeSeason), Season: s.Number, Title: name,
			PosterPath: imageURL(s.Image), EpisodeCount: count, AirDate: airDate,
		}
		// The series extended endpoint translates season names in bulk.
		// Summaries are available when the season itself is opened with Lookup.
		out = append(out, item)
	}
	slices.SortStableFunc(out, func(a, b core.SeasonSummary) int { return a.Season - b.Season })
	return out, nil
}

func episodeOrderPath(show int64, kind string) string {
	return "/series/" + strconv.FormatInt(show, 10) + "/episodes/" + kind
}

func episodePrefix(show int64, kind string, season int) string {
	return episodeOrderPath(show, kind) + "?season=" + strconv.Itoa(season)
}

type episodePage struct {
	Series   record
	Episodes []record
}

func (c *Client) Episodes(ctx context.Context, show core.ProviderRef, order core.EpisodeOrder, season int) ([]core.EpisodeDetail, error) {
	if show.Type != core.TypeShow || season < 0 {
		return nil, core.ErrUnsupported
	}
	_, id, err := recordPath(show)
	if err != nil {
		return nil, err
	}
	kind, err := seasonType(order)
	if err != nil {
		return nil, err
	}
	prefix := episodePrefix(id, kind, season)
	policy := cachePolicy{fresh: entityFresh, changes: changeRef{kind: "episodes", id: id, seriesID: true}}
	lang := c.language("")
	var records []record
	seen := make(map[int64]bool)
	var original string
	for page := 0; ; page++ {
		path := prefix + "&page=" + strconv.Itoa(page)
		data, links, err := get[episodePage](ctx, c, path, policy)
		if errors.Is(err, core.ErrNotFound) && page > 0 {
			break
		}
		if err != nil {
			return nil, err
		}
		if data.Series.ID != id {
			return nil, ErrInvalidResponse
		}
		original = first(original, iso3(data.Series.OriginalLanguage))
		before := len(seen)
		for _, ep := range data.Episodes {
			if ep.ID <= 0 || seen[ep.ID] {
				continue
			}
			seen[ep.ID] = true
			if ep.SeasonNumber == season {
				records = append(records, ep)
			}
		}
		if len(seen) == before && len(data.Episodes) > 0 {
			// A repeated page cannot establish completeness.
			return nil, ErrInvalidResponse
		}
		// The endpoint has a 500-record page size. Some successful responses
		// omit links, so a full unmarked page also needs a following probe.
		if len(data.Episodes) == 0 || (links.Next == "" && !moreEpisodes(links, len(data.Episodes), len(seen))) {
			break
		}
	}
	episodes, err := c.mapEpisodes(ctx, episodeOrderPath(id, kind), policy, records, original, lang, kind)
	if err != nil {
		return nil, err
	}
	var airDate string
	for _, ep := range episodes {
		if ep.AirDate != "" && (airDate == "" || ep.AirDate < airDate) {
			airDate = ep.AirDate
		}
	}
	c.rememberSeason(prefix, len(episodes), airDate)
	slices.SortStableFunc(episodes, func(a, b core.EpisodeDetail) int { return a.Episode - b.Episode })
	return episodes, nil
}

func moreEpisodes(links pageLinks, size, read int) bool {
	if links.Total != nil {
		return *links.Total > read
	}
	return size >= 500
}

func (c *Client) mapEpisodes(ctx context.Context, orderPath string, policy cachePolicy, records []record, original, lang, kind string) ([]core.EpisodeDetail, error) {
	local, err := c.episodeTranslations(ctx, orderPath, policy, records, original, lang, nil)
	if err != nil {
		return nil, err
	}
	english := local
	if lang != "eng" {
		english, err = c.episodeTranslations(ctx, orderPath, policy, records, original, "eng", local)
		if err != nil {
			return nil, err
		}
	}
	out := make([]core.EpisodeDetail, 0, len(records))
	for _, ep := range records {
		air := ""
		if aired := date(ep.Aired); !aired.IsZero() {
			air = aired.Format(time.DateOnly)
		}
		absolute := ep.AbsoluteNumber
		if kind == core.OrderAbsolute && absolute <= 0 && ep.SeasonNumber != 0 {
			absolute = ep.Number
		}
		out = append(out, core.EpisodeDetail{
			Ref: reference(ep.ID, core.TypeEpisode), IDs: externalIDs(ep.ID, ep.RemoteIDs),
			Season: ep.SeasonNumber, Episode: ep.Number, Absolute: absolute, AirDate: air,
			Title:     first(local[ep.ID].Name, english[ep.ID].Name, ep.Name),
			Summary:   first(local[ep.ID].Overview, english[ep.ID].Overview, ep.Overview),
			StillPath: imageURL(ep.Image), RuntimeMinutes: ep.Runtime,
		})
	}
	return out, nil
}

type translatedEpisodes struct {
	Episodes []record
}

// The translated endpoint pages the entire order even when a single season
// was requested. Reuse those pages across seasons and join by episode ID,
// preserving coordinates from the season-filtered response.
func (c *Client) episodeTranslations(ctx context.Context, orderPath string, policy cachePolicy, records []record, original, lang string, preferred map[int64]translation) (map[int64]translation, error) {
	type fields struct{ name, overview bool }

	if original == lang {
		out := make(map[int64]translation, len(records))
		for _, ep := range records {
			out[ep.ID] = translation{Name: ep.Name, Overview: ep.Overview}
		}
		return out, nil
	}
	pending := make(map[int64]fields)
	for _, ep := range records {
		firstChoice := preferred[ep.ID]
		want := fields{
			name:     firstChoice.Name == "" && slices.Contains(ep.NameTranslations, lang),
			overview: firstChoice.Overview == "" && slices.Contains(ep.OverviewTranslations, lang),
		}
		if want.name || want.overview {
			pending[ep.ID] = want
		}
	}
	out := make(map[int64]translation, len(pending))
	seen := make(map[int64]bool)
	for page := 0; len(pending) > 0; page++ {
		path := orderPath + "/" + lang + "?page=" + strconv.Itoa(page)
		data, links, err := get[translatedEpisodes](ctx, c, path, policy)
		if errors.Is(err, core.ErrNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		before := len(seen)
		for _, ep := range data.Episodes {
			seen[ep.ID] = true
			want, ok := pending[ep.ID]
			if !ok {
				continue
			}
			var tr translation
			if want.name {
				tr.Name = ep.Name
			}
			if want.overview {
				tr.Overview = ep.Overview
			}
			out[ep.ID] = tr
			delete(pending, ep.ID)
		}
		if len(seen) == before || (links.Next == "" && !moreEpisodes(links, len(data.Episodes), len(seen))) {
			break
		}
	}
	return out, nil
}

type seasonObservation struct {
	Count   int
	AirDate string
}

func (c *Client) cachedSeason(prefix string) (int, string) {
	record, ok := c.readCache(sha256.Sum256([]byte("observed:" + prefix)))
	if !ok || time.Since(record.Checked) >= entityFresh {
		return 0, ""
	}
	var observation seasonObservation
	if json.Unmarshal(record.Response.Data, &observation) != nil {
		return 0, ""
	}
	return observation.Count, observation.AirDate
}

func (c *Client) rememberSeason(prefix string, count int, airDate string) {
	digest := sha256.Sum256([]byte("observed:" + prefix))
	// Multiple viewers may list a season from the same cached pages.
	if previous, ok := c.readCache(digest); ok && time.Since(previous.Checked) < entityFresh {
		var observation seasonObservation
		if json.Unmarshal(previous.Response.Data, &observation) == nil && observation.Count == count && observation.AirDate == airDate {
			return
		}
	}
	data, _ := json.Marshal(seasonObservation{Count: count, AirDate: airDate})
	now := time.Now()
	c.writeCache(cacheRecord{Digest: digest, Fetched: now, Checked: now, Response: response{Status: "success", Data: data}})
}
