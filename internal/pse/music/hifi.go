// Package music expose une API Subsonic sans compte, comme Remux : le
// catalogue vient de l'API publique Deezer et la lecture d'addons Eclipse
// (Monochrome, Tido, SpotiFLAC…). Une « HiFi API » reste utilisable en
// option. Ajout PSE : voir docs/pse/FORK.fr.md.
package music

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Artist, Album et Track sont les formes normalisées des objets HiFi.
type Artist struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Picture string `json:"picture,omitempty"`
}

type Album struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Cover       string `json:"cover,omitempty"`
	Artist      Artist `json:"artist"`
	ReleaseDate string `json:"releaseDate,omitempty"`
	Tracks      int    `json:"tracks,omitempty"`
	Duration    int    `json:"duration,omitempty"`
}

type Track struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Duration int    `json:"duration"`
	Number   int    `json:"number,omitempty"`
	Volume   int    `json:"volume,omitempty"`
	Artist   Artist `json:"artist"`
	Album    Album  `json:"album"`
	ISRC     string `json:"isrc,omitempty"`
	Explicit bool   `json:"explicit,omitempty"`
}

// Year renvoie l'année de sortie, ou 0.
func (a Album) Year() int {
	if len(a.ReleaseDate) >= 4 {
		y, _ := strconv.Atoi(a.ReleaseDate[:4])
		return y
	}
	return 0
}

// ErrNotFound signale un objet absent côté HiFi.
var ErrNotFound = errors.New("music: introuvable")

// errDASH signale un manifeste DASH, que PSE ne sait pas relayer tel quel.
var errDASH = errors.New("music: manifeste DASH non pris en charge")

// Client interroge une HiFi API.
type Client struct {
	base string
	http *http.Client
}

// NewClient prépare un client pour l'URL de base fournie.
func NewClient(base string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	return &Client{base: strings.TrimRight(base, "/"), http: hc}
}

func (c *Client) get(ctx context.Context, path string) (any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, redactErr(err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("music: HiFi API a répondu %d", res.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(res.Body, 16<<20))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("music: réponse HiFi illisible: %w", err)
	}
	return v, nil
}

// redactErr retire l'URL (qui peut contenir des identifiants) des erreurs.
func redactErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return fmt.Errorf("music: HiFi API injoignable: %w", ue.Err)
	}
	return err
}

// Search cherche des titres, albums ou artistes (kind: tracks, albums, artists).
func (c *Client) Search(ctx context.Context, kind, query string, limit, offset int) ([]map[string]any, int, error) {
	param := map[string]string{"tracks": "s", "albums": "al", "artists": "a"}[kind]
	if param == "" {
		return nil, 0, fmt.Errorf("music: recherche %q inconnue", kind)
	}
	q := url.Values{param: {query}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	v, err := c.get(ctx, "/search/?"+q.Encode())
	if err != nil {
		return nil, 0, err
	}
	section := findSection(v, kind, 0)
	if section == nil {
		return nil, 0, nil
	}
	items := objects(section["items"])
	total := int(num(section["totalNumberOfItems"]))
	if total < len(items) {
		total = len(items)
	}
	return items, total, nil
}

// SearchTracks, SearchAlbums et SearchArtists normalisent les résultats.
func (c *Client) SearchTracks(ctx context.Context, q string, limit, offset int) ([]Track, error) {
	items, _, err := c.Search(ctx, "tracks", q, limit, offset)
	out := make([]Track, 0, len(items))
	for _, m := range items {
		if t := parseTrack(m); t.ID != 0 {
			out = append(out, t)
		}
	}
	return out, err
}

func (c *Client) SearchAlbums(ctx context.Context, q string, limit, offset int) ([]Album, error) {
	items, _, err := c.Search(ctx, "albums", q, limit, offset)
	out := make([]Album, 0, len(items))
	for _, m := range items {
		if a := parseAlbum(m); a.ID != 0 {
			out = append(out, a)
		}
	}
	return out, err
}

func (c *Client) SearchArtists(ctx context.Context, q string, limit, offset int) ([]Artist, error) {
	items, _, err := c.Search(ctx, "artists", q, limit, offset)
	out := make([]Artist, 0, len(items))
	for _, m := range items {
		if a := parseArtist(m); a.ID != 0 {
			out = append(out, a)
		}
	}
	return out, err
}

// Album renvoie un album et ses titres, page par page si nécessaire.
func (c *Client) Album(ctx context.Context, id int64) (Album, []Track, error) {
	v, err := c.get(ctx, "/album/?id="+strconv.FormatInt(id, 10))
	if err != nil {
		return Album{}, nil, err
	}
	data, _ := unwrap(v).(map[string]any)
	if data == nil {
		return Album{}, nil, ErrNotFound
	}
	var album Album
	if _, ok := data["numberOfTracks"]; ok || data["title"] != nil {
		album = parseAlbum(data)
	}
	tracks := albumItems(data)
	if album.ID == 0 && len(tracks) > 0 {
		album = tracks[0].Album
	}
	if album.ID == 0 {
		return Album{}, nil, ErrNotFound
	}
	for album.Tracks > len(tracks) && len(tracks) < 2000 {
		q := url.Values{"id": {strconv.FormatInt(id, 10)}, "offset": {strconv.Itoa(len(tracks))}, "limit": {"500"}}
		v, err := c.get(ctx, "/album/?"+q.Encode())
		if err != nil {
			break
		}
		page, _ := unwrap(v).(map[string]any)
		more := albumItems(page)
		if len(more) == 0 {
			break
		}
		tracks = append(tracks, more...)
	}
	for i := range tracks {
		if tracks[i].Album.ID == 0 || tracks[i].Album.Title == "" {
			tracks[i].Album = album
		}
		if tracks[i].Artist.ID == 0 {
			tracks[i].Artist = album.Artist
		}
	}
	if album.Artist.ID == 0 && len(tracks) > 0 {
		album.Artist = tracks[0].Artist
	}
	if album.ReleaseDate == "" && len(tracks) > 0 {
		album.ReleaseDate = tracks[0].Album.ReleaseDate
	}
	if album.Tracks == 0 {
		album.Tracks = len(tracks)
	}
	if album.Duration == 0 {
		for _, t := range tracks {
			album.Duration += t.Duration
		}
	}
	return album, tracks, nil
}

func albumItems(data map[string]any) []Track {
	var out []Track
	for _, it := range objects(data["items"]) {
		if inner, ok := it["item"].(map[string]any); ok {
			it = inner
		}
		if t := parseTrack(it); t.ID != 0 {
			out = append(out, t)
		}
	}
	return out
}

// Artist renvoie un artiste et ses albums, du plus récent au plus ancien.
func (c *Client) Artist(ctx context.Context, id int64) (Artist, []Album, error) {
	v, err := c.get(ctx, "/artist/?id="+strconv.FormatInt(id, 10))
	if err != nil {
		return Artist{}, nil, err
	}
	data := unwrap(v)
	var raw map[string]any
	switch d := data.(type) {
	case map[string]any:
		if a, ok := d["artist"].(map[string]any); ok {
			raw = a
		} else {
			raw = d
		}
	case []any:
		if len(d) > 0 {
			raw, _ = d[0].(map[string]any)
		}
	}
	artist := parseArtist(raw)
	if artist.ID == 0 {
		artist.ID = id
	}
	if artist.Name == "" {
		return Artist{}, nil, ErrNotFound
	}
	albums := map[int64]Album{}
	scanAlbums(data, albums, 0)
	if len(albums) == 0 {
		q := url.Values{"f": {strconv.FormatInt(id, 10)}, "skip_tracks": {"true"}}
		if v, err := c.get(ctx, "/artist/?"+q.Encode()); err == nil {
			scanAlbums(v, albums, 0)
		}
	}
	out := make([]Album, 0, len(albums))
	for _, a := range albums {
		if a.Artist.ID != 0 && a.Artist.ID != id {
			continue
		}
		if a.Artist.ID == 0 {
			a.Artist = artist
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ReleaseDate != out[j].ReleaseDate {
			return out[i].ReleaseDate > out[j].ReleaseDate
		}
		return out[i].ID < out[j].ID
	})
	return artist, out, nil
}

func scanAlbums(v any, into map[int64]Album, depth int) {
	if depth > 10 {
		return
	}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			scanAlbums(e, into, depth+1)
		}
	case map[string]any:
		item := t
		if inner, ok := t["item"].(map[string]any); ok {
			item = inner
		}
		_, hasTracks := item["numberOfTracks"]
		typ := strings.ToLower(str(item["type"]))
		if (hasTracks || typ == "album") && num(item["id"]) != 0 {
			a := parseAlbum(item)
			into[a.ID] = a
		}
		for _, k := range sortedKeys(t) {
			scanAlbums(t[k], into, depth+1)
		}
	}
}

// Track renvoie les métadonnées d'un titre.
func (c *Client) Track(ctx context.Context, id int64) (Track, error) {
	v, err := c.get(ctx, "/info/?id="+strconv.FormatInt(id, 10))
	if err != nil {
		return Track{}, err
	}
	var t Track
	switch d := unwrap(v).(type) {
	case map[string]any:
		t = parseTrack(d)
	case []any:
		for _, m := range objects(d) {
			if _, ok := m["duration"]; ok {
				t = parseTrack(m)
				break
			}
		}
	}
	if t.ID == 0 {
		return Track{}, ErrNotFound
	}
	return t, nil
}

// Stream décrit un flux audio direct.
type Stream struct {
	Provider  string
	ExpiresAt int64
	URL       string
	Mime      string
	Quality   string
	// File : chemin d'un fichier du client torrent de Remix, lu sur le
	// disque sans requête HTTP ; URL n'est alors qu'un identifiant.
	File string
}

// qualityChain renvoie les qualités à essayer, de la demandée à la plus basse.
func qualityChain(q string) []string {
	all := []string{"HI_RES_LOSSLESS", "LOSSLESS", "HIGH", "LOW"}
	for i, v := range all {
		if v == q {
			return all[i:]
		}
	}
	return all[1:]
}

// Stream résout l'URL directe d'un titre. Les manifestes DASH sont
// ignorés au profit d'une qualité inférieure servie en fichier unique.
func (c *Client) Stream(ctx context.Context, id int64, quality string) (Stream, error) {
	var last error = ErrNotFound
	for _, q := range qualityChain(quality) {
		v, err := c.get(ctx, "/track/?"+url.Values{"id": {strconv.FormatInt(id, 10)}, "quality": {q}}.Encode())
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return Stream{}, err
			}
			last = err
			continue
		}
		s, err := streamFrom(v)
		if err == nil {
			s.Quality = q
			return s, nil
		}
		last = err
	}
	return Stream{}, last
}

func streamFrom(v any) (Stream, error) {
	if u := findString(v, "OriginalTrackUrl", 0); u != "" {
		return Stream{URL: u}, nil
	}
	if u := findString(v, "originalTrackUrl", 0); u != "" {
		return Stream{URL: u}, nil
	}
	m := findWith(v, "manifest", 0)
	if m == nil {
		return Stream{}, ErrNotFound
	}
	switch man := m["manifest"].(type) {
	case map[string]any:
		return manifestJSON(man)
	case string:
		raw := decodeB64(man)
		trimmed := bytes.TrimSpace(raw)
		if bytes.Contains(trimmed, []byte("<MPD")) {
			return Stream{}, errDASH
		}
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.UseNumber()
		var obj map[string]any
		if err := dec.Decode(&obj); err != nil {
			return Stream{}, fmt.Errorf("music: manifeste illisible")
		}
		return manifestJSON(obj)
	}
	return Stream{}, ErrNotFound
}

func manifestJSON(m map[string]any) (Stream, error) {
	urls, _ := m["urls"].([]any)
	var best string
	bestScore := 99
	for _, u := range urls {
		s := str(u)
		if s == "" {
			continue
		}
		score := 4
		low := strings.ToLower(s)
		for i, k := range []string{"flac", "lossless", "hi-res", "high"} {
			if strings.Contains(low, k) {
				score = i
				break
			}
		}
		if score < bestScore {
			best, bestScore = s, score
		}
	}
	if best == "" {
		return Stream{}, ErrNotFound
	}
	mime := str(m["mimeType"])
	if strings.HasPrefix(mime, "application/") {
		mime = ""
	}
	return Stream{URL: best, Mime: mime}, nil
}

func decodeB64(s string) []byte {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(strings.TrimSpace(s)); err == nil {
			return b
		}
	}
	return []byte(s)
}

// --- analyse permissive du JSON HiFi ---

func unwrap(v any) any {
	if m, ok := v.(map[string]any); ok {
		if d, ok := m["data"]; ok && d != nil {
			return d
		}
	}
	return v
}

func findSection(v any, key string, depth int) map[string]any {
	if depth > 8 {
		return nil
	}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			if f := findSection(e, key, depth+1); f != nil {
				return f
			}
		}
	case map[string]any:
		if _, ok := t["items"].([]any); ok {
			return t
		}
		if sub, ok := t[key]; ok {
			if f := findSection(sub, key, depth+1); f != nil {
				return f
			}
		}
		for _, k := range sortedKeys(t) {
			if f := findSection(t[k], key, depth+1); f != nil {
				return f
			}
		}
	}
	return nil
}

func findWith(v any, key string, depth int) map[string]any {
	if depth > 8 {
		return nil
	}
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			if f := findWith(e, key, depth+1); f != nil {
				return f
			}
		}
	case map[string]any:
		if _, ok := t[key]; ok {
			return t
		}
		for _, k := range sortedKeys(t) {
			if f := findWith(t[k], key, depth+1); f != nil {
				return f
			}
		}
	}
	return nil
}

func findString(v any, key string, depth int) string {
	if m := findWith(v, key, depth); m != nil {
		return str(m[key])
	}
	return ""
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func objects(v any) []map[string]any {
	arr, _ := v.([]any)
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func num(v any) int64 {
	switch t := v.(type) {
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n
		}
		f, _ := t.Float64()
		return int64(f)
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	}
	return 0
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	}
	return ""
}

func parseArtist(m map[string]any) Artist {
	if m == nil {
		return Artist{}
	}
	return Artist{ID: num(m["id"]), Name: str(m["name"]), Picture: str(m["picture"])}
}

func firstArtist(m map[string]any) Artist {
	if a, ok := m["artist"].(map[string]any); ok {
		if r := parseArtist(a); r.ID != 0 {
			return r
		}
	}
	for _, a := range objects(m["artists"]) {
		if r := parseArtist(a); r.ID != 0 {
			return r
		}
	}
	return Artist{}
}

func parseAlbum(m map[string]any) Album {
	if m == nil {
		return Album{}
	}
	date := str(m["releaseDate"])
	if date == "" {
		if s := str(m["streamStartDate"]); len(s) >= 10 {
			date = s[:10]
		}
	}
	return Album{
		ID:          num(m["id"]),
		Title:       str(m["title"]),
		Cover:       str(m["cover"]),
		Artist:      firstArtist(m),
		ReleaseDate: date,
		Tracks:      int(num(m["numberOfTracks"])),
		Duration:    int(num(m["duration"])),
	}
}

func parseTrack(m map[string]any) Track {
	if m == nil {
		return Track{}
	}
	t := Track{
		ID:       num(m["id"]),
		Title:    str(m["title"]),
		Duration: int(num(m["duration"])),
		Number:   int(num(m["trackNumber"])),
		Volume:   int(num(m["volumeNumber"])),
		Artist:   firstArtist(m),
		ISRC:     str(m["isrc"]),
	}
	if v := str(m["version"]); v != "" && !strings.Contains(t.Title, v) {
		t.Title += " (" + v + ")"
	}
	t.Explicit, _ = m["explicit"].(bool)
	if a, ok := m["album"].(map[string]any); ok {
		t.Album = parseAlbum(a)
	}
	if t.Album.Artist.ID == 0 {
		t.Album.Artist = t.Artist
	}
	return t
}
