package tvdb

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type artworkType struct {
	ID         int64
	Name, Slug string
}

func (c *Client) Images(ctx context.Context, ref core.ProviderRef) (core.Artwork, error) {
	if ref.Type == "" {
		ref.Type = personType
	}
	lang := c.language("")
	r, _, err := c.extended(ctx, ref, lang)
	if err != nil {
		return core.Artwork{}, err
	}
	return c.images(ctx, ref.Type, r, lang)
}

func (c *Client) images(ctx context.Context, typ core.MediaType, r record, lang string) (core.Artwork, error) {
	var out core.Artwork
	items := slices.Concat(r.Artworks, r.Artwork)
	if len(items) > 0 {
		types, _, err := get[[]artworkType](ctx, c, "/artwork/types", cachePolicy{fresh: 7 * 24 * time.Hour})
		if err != nil {
			return out, err
		}
		kinds := make(map[int64]string, len(types))
		for _, t := range types {
			kind := artworkKind(t.Slug)
			if kind == "" {
				kind = artworkKind(t.Name)
			}
			kinds[t.ID] = kind
		}
		rank := func(item artworkRecord) int {
			switch item.Language {
			case lang:
				return 0
			case "":
				return 1
			case "eng":
				return 2
			default:
				return 3
			}
		}
		slices.SortStableFunc(items, func(a, b artworkRecord) int {
			if diff := rank(a) - rank(b); diff != 0 {
				return diff
			}
			return compareScore(a.Score, b.Score)
		})
		for _, item := range items {
			path := imageURL(item.Image)
			if path == "" {
				continue
			}
			im := core.ArtworkImageFile{FilePath: path, Width: item.Width, Height: item.Height, ISO639: iso2(item.Language), VoteAvg: item.Score}
			switch kinds[item.Type] {
			case "poster":
				out.Posters = append(out.Posters, im)
			case "backdrop":
				out.Backdrops = append(out.Backdrops, im)
				if thumb := imageURL(item.Thumbnail); thumb != "" {
					out.Still = append(out.Still, core.ArtworkImageFile{FilePath: thumb, ISO639: im.ISO639, VoteAvg: im.VoteAvg})
				}
			case "logo":
				out.Logos = append(out.Logos, im)
			case "still":
				out.Still = append(out.Still, im)
			}
		}
	}
	if path := imageURL(r.Image); path != "" {
		group := &out.Posters
		if typ == core.TypeEpisode || typ == personType {
			group = &out.Still
		}
		if !slices.ContainsFunc(*group, func(image core.ArtworkImageFile) bool { return image.FilePath == path }) {
			*group = append(*group, core.ArtworkImageFile{FilePath: path})
		}
	}
	return out, nil
}

func artworkKind(value string) string {
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "poster"):
		return "poster"
	case strings.Contains(value, "background"), strings.Contains(value, "backdrop"), strings.Contains(value, "fanart"):
		return "backdrop"
	case strings.Contains(value, "logo"):
		return "logo"
	case strings.Contains(value, "thumbnail"), strings.Contains(value, "still"), value == "episode", strings.Contains(value, "screencap"), value == "photo":
		return "still"
	}
	return ""
}

func compareScore(a, b float64) int {
	if a > b {
		return -1
	}
	if a < b {
		return 1
	}
	return 0
}

func firstImage(images []core.ArtworkImageFile) string {
	if len(images) == 0 {
		return ""
	}
	return images[0].FilePath
}

func (c *Client) credits(ctx context.Context, characters []character, title *core.Title) error {
	if len(characters) == 0 {
		return nil
	}
	characters = slices.Clone(characters)
	slices.SortStableFunc(characters, func(a, b character) int { return a.Sort - b.Sort })
	needsTypes := slices.ContainsFunc(characters, func(ch character) bool { return ch.Type != 0 && ch.PeopleType == "" })
	kinds := make(map[int64]string)
	if needsTypes {
		types, _, err := get[[]namedRecord](ctx, c, "/people/types", cachePolicy{fresh: 7 * 24 * time.Hour})
		if err != nil {
			return err
		}
		for _, t := range types {
			kinds[t.ID] = t.Name
		}
	}
	for _, ch := range characters {
		if ch.PersonName == "" || ch.PeopleID <= 0 {
			continue
		}
		job := first(ch.PeopleType, kinds[ch.Type])
		p := core.Person{ID: "tvdb:" + strconv.FormatInt(ch.PeopleID, 10), Name: ch.PersonName,
			Role: job, ThumbPath: first(imageURL(ch.Image), imageURL(ch.PersonImgURL))}
		switch strings.ToLower(job) {
		case "actor", "guest star", "voice actor", "guest":
			p.Role = ch.Name
			title.Cast = append(title.Cast, p)
		case "director":
			title.Directors = append(title.Directors, p)
		case "writer", "screenwriter":
			title.Writers = append(title.Writers, p)
		default:
			title.Crew = append(title.Crew, p)
		}
	}
	return nil
}
