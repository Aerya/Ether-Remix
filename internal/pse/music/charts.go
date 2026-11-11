package music

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// Charter est implémenté par les sources qui publient des classements.
// La bibliothèque Musique des clients Jellyfin les affiche après les
// favoris et écoutes de chacun, pour qu'elle ne soit jamais vide.
type Charter interface {
	ChartAlbums(ctx context.Context, limit int) ([]Album, error)
	ChartArtists(ctx context.Context, limit int) ([]Artist, error)
	ChartTracks(ctx context.Context, limit int) ([]Track, error)
}

// TopTracker renvoie les titres les plus écoutés d'un artiste.
type TopTracker interface {
	ArtistTop(ctx context.Context, id int64, limit int) ([]Track, error)
}

func (d *DeezerSource) ChartAlbums(ctx context.Context, limit int) ([]Album, error) {
	var r struct{ Data []dzAlbum }
	if err := d.get(ctx, "/chart/0/albums", url.Values{"limit": {strconv.Itoa(limit)}}, &r); err != nil {
		return nil, err
	}
	out := make([]Album, 0, len(r.Data))
	for i := range r.Data {
		out = append(out, r.Data[i].conv())
	}
	return out, nil
}

func (d *DeezerSource) ChartArtists(ctx context.Context, limit int) ([]Artist, error) {
	var r struct{ Data []dzArtist }
	if err := d.get(ctx, "/chart/0/artists", url.Values{"limit": {strconv.Itoa(limit)}}, &r); err != nil {
		return nil, err
	}
	out := make([]Artist, 0, len(r.Data))
	for i := range r.Data {
		out = append(out, r.Data[i].conv())
	}
	return out, nil
}

func (d *DeezerSource) ChartTracks(ctx context.Context, limit int) ([]Track, error) {
	var r struct{ Data []dzTrack }
	if err := d.get(ctx, "/chart/0/tracks", url.Values{"limit": {strconv.Itoa(limit)}}, &r); err != nil {
		return nil, err
	}
	return readable(r.Data), nil
}

func (d *DeezerSource) ArtistTop(ctx context.Context, id int64, limit int) ([]Track, error) {
	var r struct{ Data []dzTrack }
	if err := d.get(ctx, "/artist/"+strconv.FormatInt(id, 10)+"/top", url.Values{"limit": {strconv.Itoa(limit)}}, &r); err != nil {
		return nil, err
	}
	return readable(r.Data), nil
}

func readable(in []dzTrack) []Track {
	out := make([]Track, 0, len(in))
	for i := range in {
		if in[i].Readable != nil && !*in[i].Readable {
			continue
		}
		out = append(out, in[i].conv())
	}
	return out
}

// memo garde quelques réponses du catalogue pour les clients Jellyfin, qui
// redemandent souvent les mêmes albums et artistes.
type memo struct {
	mu   sync.Mutex
	ttl  time.Duration
	max  int
	data map[string]memoEntry
}

type memoEntry struct {
	at time.Time
	v  any
}

func newMemo(ttl time.Duration, max int) *memo {
	return &memo{ttl: ttl, max: max, data: map[string]memoEntry{}}
}

func memoized[T any](m *memo, key string, now time.Time, fn func() (T, error)) (T, error) {
	m.mu.Lock()
	if e, ok := m.data[key]; ok && now.Sub(e.at) < m.ttl {
		m.mu.Unlock()
		return e.v.(T), nil
	}
	m.mu.Unlock()
	v, err := fn()
	if err != nil {
		return v, err
	}
	m.mu.Lock()
	if len(m.data) >= m.max {
		m.data = map[string]memoEntry{}
	}
	m.data[key] = memoEntry{at: now, v: v}
	m.mu.Unlock()
	return v, nil
}

func memoizedFor[T any](ctx context.Context, m *memo, key string, now time.Time, fn func() (T, error)) (T, error) {
	return memoized(m, strconv.FormatInt(core.CatalogViewerID(ctx), 10)+":"+strconv.FormatUint(mediaaccess.Revision(), 10)+":"+key, now, fn)
}
