package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DeezerAPI est l'API publique de Deezer, utilisée sans compte pour le
// catalogue (recherche, artistes, albums, pochettes), comme le fait Remux.
const DeezerAPI = "https://api.deezer.com"

// DeezerImages est l'origine des pochettes Deezer.
const DeezerImages = "https://cdn-images.dzcdn.net/images"

// Resolver trouve le flux audio d'un titre du catalogue.
type Resolver interface {
	Resolve(ctx context.Context, t Track) (Stream, error)
}

// DeezerSource associe le catalogue Deezer à un résolveur de flux
// (addons Eclipse).
type DeezerSource struct {
	API     string
	Images  string
	HTTP    *http.Client
	Streams Resolver
	// Lang : langue des noms renvoyés (genres), en-tête Accept-Language.
	Lang string
}

// NewDeezerSource prépare la source par défaut.
func NewDeezerSource(streams Resolver, hc *http.Client) *DeezerSource {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	return &DeezerSource{API: DeezerAPI, Images: DeezerImages, HTTP: hc, Streams: streams}
}

type dzError struct {
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

func (d *DeezerSource) get(ctx context.Context, path string, q url.Values, into any) error {
	u := strings.TrimRight(d.API, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if d.Lang != "" {
		req.Header.Set("Accept-Language", d.Lang)
	}
	res, err := d.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("music: Deezer injoignable: %w", ue.Err)
		}
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("music: Deezer a répondu %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	var e dzError
	if json.Unmarshal(body, &e) == nil && e.Error != nil {
		if e.Error.Code == 800 || e.Error.Type == "DataException" {
			return ErrNotFound
		}
		return fmt.Errorf("music: Deezer: %s", e.Error.Message)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("music: réponse Deezer illisible: %w", err)
	}
	return nil
}

type dzArtist struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	PictureXL  string `json:"picture_xl"`
	PictureBig string `json:"picture_big"`
	NbAlbum    int    `json:"nb_album"`
}

type dzAlbum struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	MD5         string    `json:"md5_image"`
	ReleaseDate string    `json:"release_date"`
	NbTracks    int       `json:"nb_tracks"`
	Duration    int       `json:"duration"`
	Artist      *dzArtist `json:"artist"`
	RecordType  string    `json:"record_type"`
}

type dzTrack struct {
	ID       int64     `json:"id"`
	Title    string    `json:"title"`
	Duration int       `json:"duration"`
	Position int       `json:"track_position"`
	Disk     int       `json:"disk_number"`
	ISRC     string    `json:"isrc"`
	Explicit bool      `json:"explicit_lyrics"`
	Readable *bool     `json:"readable"`
	Artist   *dzArtist `json:"artist"`
	Album    *dzAlbum  `json:"album"`
}

var dzPictureRe = regexp.MustCompile(`/images/artist/([0-9a-fA-F]{32})/`)

func (a *dzArtist) conv() Artist {
	if a == nil {
		return Artist{}
	}
	out := Artist{ID: a.ID, Name: strings.TrimSpace(a.Name)}
	for _, u := range []string{a.PictureXL, a.PictureBig} {
		if m := dzPictureRe.FindStringSubmatch(u); m != nil {
			out.Picture = strings.ToLower(m[1])
			break
		}
	}
	return out
}

func (a *dzAlbum) conv() Album {
	if a == nil {
		return Album{}
	}
	return Album{ID: a.ID, Title: strings.TrimSpace(a.Title), Cover: a.MD5, Artist: a.Artist.conv(),
		ReleaseDate: a.ReleaseDate, Tracks: a.NbTracks, Duration: a.Duration}
}

func (t *dzTrack) conv() Track {
	out := Track{ID: t.ID, Title: strings.TrimSpace(t.Title), Duration: t.Duration, Number: t.Position, Volume: t.Disk,
		Artist: t.Artist.conv(), ISRC: t.ISRC, Explicit: t.Explicit}
	if t.Album != nil {
		out.Album = t.Album.conv()
		if out.Album.Artist.ID == 0 {
			out.Album.Artist = out.Artist
		}
	}
	return out
}

func page(limit, offset int) url.Values {
	if limit <= 0 {
		limit = 20
	}
	return url.Values{"limit": {strconv.Itoa(limit)}, "index": {strconv.Itoa(offset)}}
}

func (d *DeezerSource) SearchTracks(ctx context.Context, q string, limit, offset int) ([]Track, error) {
	var r struct{ Data []dzTrack }
	v := page(limit, offset)
	v.Set("q", q)
	if err := d.get(ctx, "/search/track", v, &r); err != nil {
		return nil, err
	}
	out := make([]Track, 0, len(r.Data))
	for i := range r.Data {
		if r.Data[i].Readable != nil && !*r.Data[i].Readable {
			continue
		}
		out = append(out, r.Data[i].conv())
	}
	return out, nil
}

func (d *DeezerSource) SearchAlbums(ctx context.Context, q string, limit, offset int) ([]Album, error) {
	var r struct{ Data []dzAlbum }
	v := page(limit, offset)
	v.Set("q", q)
	if err := d.get(ctx, "/search/album", v, &r); err != nil {
		return nil, err
	}
	out := make([]Album, 0, len(r.Data))
	for i := range r.Data {
		out = append(out, r.Data[i].conv())
	}
	return out, nil
}

func (d *DeezerSource) SearchArtists(ctx context.Context, q string, limit, offset int) ([]Artist, error) {
	var r struct{ Data []dzArtist }
	v := page(limit, offset)
	v.Set("q", q)
	if err := d.get(ctx, "/search/artist", v, &r); err != nil {
		return nil, err
	}
	out := make([]Artist, 0, len(r.Data))
	for i := range r.Data {
		out = append(out, r.Data[i].conv())
	}
	return out, nil
}

func (d *DeezerSource) Album(ctx context.Context, id int64) (Album, []Track, error) {
	var a dzAlbum
	if err := d.get(ctx, "/album/"+strconv.FormatInt(id, 10), nil, &a); err != nil {
		return Album{}, nil, err
	}
	var r struct{ Data []dzTrack }
	if err := d.get(ctx, "/album/"+strconv.FormatInt(id, 10)+"/tracks", url.Values{"limit": {"500"}}, &r); err != nil {
		return Album{}, nil, err
	}
	album := a.conv()
	tracks := make([]Track, 0, len(r.Data))
	for i := range r.Data {
		t := r.Data[i].conv()
		t.Album = album
		if t.Artist.ID == 0 {
			t.Artist = album.Artist
		}
		tracks = append(tracks, t)
	}
	if album.Tracks == 0 {
		album.Tracks = len(tracks)
	}
	return album, tracks, nil
}

func (d *DeezerSource) Artist(ctx context.Context, id int64) (Artist, []Album, error) {
	var a dzArtist
	if err := d.get(ctx, "/artist/"+strconv.FormatInt(id, 10), nil, &a); err != nil {
		return Artist{}, nil, err
	}
	var r struct{ Data []dzAlbum }
	if err := d.get(ctx, "/artist/"+strconv.FormatInt(id, 10)+"/albums", url.Values{"limit": {"200"}}, &r); err != nil {
		return Artist{}, nil, err
	}
	artist := a.conv()
	albums := make([]Album, 0, len(r.Data))
	seen := map[int64]bool{}
	for i := range r.Data {
		al := r.Data[i].conv()
		if seen[al.ID] {
			continue
		}
		seen[al.ID] = true
		al.Artist = artist
		albums = append(albums, al)
	}
	return artist, albums, nil
}

func (d *DeezerSource) Track(ctx context.Context, id int64) (Track, error) {
	var t dzTrack
	if err := d.get(ctx, "/track/"+strconv.FormatInt(id, 10), nil, &t); err != nil {
		return Track{}, err
	}
	if t.ID == 0 {
		return Track{}, ErrNotFound
	}
	return t.conv(), nil
}

// Stream résout le flux du titre via les addons Eclipse.
func (d *DeezerSource) Stream(ctx context.Context, id int64, _ string) (Stream, error) {
	if d.Streams == nil {
		return Stream{}, errors.New("music: aucun addon de lecture configuré")
	}
	t, err := d.Track(ctx, id)
	if err != nil {
		return Stream{}, err
	}
	return d.Streams.Resolve(ctx, t)
}

var dzImageSizes = []int{120, 250, 500, 1000, 1400}

// ImageURL construit l'adresse d'une image Deezer.
func (d *DeezerSource) ImageURL(kind byte, ref string, size int) string {
	folder := "cover"
	if kind == 'p' {
		folder = "artist"
	}
	n := strconv.Itoa(pickSize(dzImageSizes, size))
	return strings.TrimRight(d.Images, "/") + "/" + folder + "/" + strings.ToLower(ref) + "/" + n + "x" + n + "-000000-80-0-0.jpg"
}
