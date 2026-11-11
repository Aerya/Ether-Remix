package music

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// Source fournit le catalogue et les flux servis par l'API Subsonic.
type Source interface {
	SearchTracks(ctx context.Context, q string, limit, offset int) ([]Track, error)
	SearchAlbums(ctx context.Context, q string, limit, offset int) ([]Album, error)
	SearchArtists(ctx context.Context, q string, limit, offset int) ([]Artist, error)
	Album(ctx context.Context, id int64) (Album, []Track, error)
	Artist(ctx context.Context, id int64) (Artist, []Album, error)
	Track(ctx context.Context, id int64) (Track, error)
	Stream(ctx context.Context, id int64, quality string) (Stream, error)
	// ImageURL renvoie l'adresse d'une pochette (kind 'c') ou d'une photo
	// d'artiste (kind 'p') pour la référence ref, au plus près de size pixels.
	ImageURL(kind byte, ref string, size int) string
}

// imageRefRe accepte les références d'image des deux catalogues : UUID
// TIDAL ou empreinte MD5 Deezer.
var imageRefRe = regexp.MustCompile(`^(?:ib-[1-9][0-9]{0,18}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9a-fA-F]{32})$`)

var coverSizes = []int{80, 160, 320, 640, 1280}
var pictureSizes = []int{160, 320, 480, 750}

func pickSize(sizes []int, want int) int {
	if want <= 0 {
		return sizes[len(sizes)-2]
	}
	for _, s := range sizes {
		if s >= want {
			return s
		}
	}
	return sizes[len(sizes)-1]
}

// HiFiSource adosse l'API Subsonic à une HiFi API (catalogue TIDAL).
type HiFiSource struct {
	*Client
	CoverBase string // https://resources.tidal.com/images par défaut
}

// NewHiFiSource prépare une source HiFi API.
func NewHiFiSource(c *Client, coverBase string) *HiFiSource {
	if coverBase == "" {
		coverBase = "https://resources.tidal.com/images"
	}
	return &HiFiSource{Client: c, CoverBase: strings.TrimRight(coverBase, "/")}
}

// ImageURL construit l'adresse d'une image TIDAL.
func (h *HiFiSource) ImageURL(kind byte, ref string, size int) string {
	sizes := coverSizes
	if kind == 'p' {
		sizes = pictureSizes
	}
	n := strconv.Itoa(pickSize(sizes, size))
	return h.CoverBase + "/" + strings.ReplaceAll(ref, "-", "/") + "/" + n + "x" + n + ".jpg"
}
