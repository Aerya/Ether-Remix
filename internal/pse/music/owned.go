package music

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/pse/localmedia"
	"github.com/braedonsaunders/ether/internal/pse/mediaaccess"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("pse_music_owned", 1, `CREATE TABLE pse_music_owned(kind TEXT NOT NULL,item_id INTEGER NOT NULL,provider TEXT NOT NULL,name TEXT NOT NULL,data TEXT NOT NULL,added_at INTEGER NOT NULL,album_id INTEGER NOT NULL DEFAULT 0,artist_id INTEGER NOT NULL DEFAULT 0,PRIMARY KEY(kind,item_id)); CREATE INDEX pse_music_owned_provider ON pse_music_owned(provider); CREATE INDEX pse_music_owned_album ON pse_music_owned(kind,album_id); CREATE INDEX pse_music_owned_artist ON pse_music_owned(kind,artist_id);`)
}

// OwnedSource combines the configured online catalogue with explicitly shared
// local/iBroadcast libraries. Reserved ID ranges keep provider IDs separate.
type OwnedSource struct {
	Remote  Source
	DB      *sql.DB
	Local   *localmedia.Library
	IB      *IBroadcast
	Account func(context.Context, int64) (*IBroadcast, error)
}

func ownedID(id int64) bool { return id >= localmedia.AudioBase && id < 7_000_000_000_000 }
func ownedItems[T any](ctx context.Context, db *sql.DB, kind, q string, limit, offset int) ([]T, error) {
	if limit <= 0 {
		return []T{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := db.QueryContext(ctx, "SELECT data FROM pse_music_owned WHERE "+mediaaccess.Predicate("provider", core.CatalogViewerID(ctx))+" AND kind=? AND name LIKE ? ESCAPE '!' ORDER BY name,item_id LIMIT ? OFFSET ?", kind, "%"+strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(q))+"%", limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v T
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func ownedGet[T any](ctx context.Context, db *sql.DB, kind string, id int64) (T, error) {
	var v T
	var raw string
	err := db.QueryRowContext(ctx, "SELECT data FROM pse_music_owned WHERE "+mediaaccess.Predicate("provider", core.CatalogViewerID(ctx))+" AND kind=? AND item_id=?", kind, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(raw), &v)
	return v, err
}
func joinedSearch[T any](ctx context.Context, s *OwnedSource, kind, q string, limit, offset int, remote func(context.Context, string, int, int) ([]T, error)) ([]T, error) {
	if limit <= 0 {
		return []T{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	local, err := ownedItems[T](ctx, s.DB, kind, q, limit, offset)
	if err != nil {
		return nil, err
	}
	if len(local) == limit || remote == nil {
		return local, nil
	}
	var count int
	err = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM pse_music_owned WHERE "+mediaaccess.Predicate("provider", core.CatalogViewerID(ctx))+" AND kind=? AND name LIKE ? ESCAPE '!'", kind, "%"+strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(q))+"%").Scan(&count)
	if err != nil {
		return nil, err
	}
	skip := offset - count
	if skip < 0 {
		skip = 0
	}
	extra, err := remote(ctx, q, limit-len(local), skip)
	if len(local) > 0 {
		err = nil
	}
	return append(local, extra...), err
}
func (s *OwnedSource) SearchTracks(c context.Context, q string, n, o int) ([]Track, error) {
	var r func(context.Context, string, int, int) ([]Track, error)
	if s.Remote != nil {
		r = s.Remote.SearchTracks
	}
	return joinedSearch[Track](c, s, KindTrack, q, n, o, r)
}
func (s *OwnedSource) SearchAlbums(c context.Context, q string, n, o int) ([]Album, error) {
	var r func(context.Context, string, int, int) ([]Album, error)
	if s.Remote != nil {
		r = s.Remote.SearchAlbums
	}
	return joinedSearch[Album](c, s, KindAlbum, q, n, o, r)
}
func (s *OwnedSource) SearchArtists(c context.Context, q string, n, o int) ([]Artist, error) {
	var r func(context.Context, string, int, int) ([]Artist, error)
	if s.Remote != nil {
		r = s.Remote.SearchArtists
	}
	return joinedSearch[Artist](c, s, KindArtist, q, n, o, r)
}
func (s *OwnedSource) Track(c context.Context, id int64) (Track, error) {
	if ownedID(id) {
		return ownedGet[Track](c, s.DB, KindTrack, id)
	}
	if s.Remote != nil {
		return s.Remote.Track(c, id)
	}
	return Track{}, ErrNotFound
}
func (s *OwnedSource) Album(c context.Context, id int64) (Album, []Track, error) {
	if !ownedID(id) {
		if s.Remote != nil {
			return s.Remote.Album(c, id)
		}
		return Album{}, nil, ErrNotFound
	}
	a, err := ownedGet[Album](c, s.DB, KindAlbum, id)
	if err != nil {
		return a, nil, err
	}
	all, err := ownedRelated[Track](c, s.DB, KindTrack, "album_id", id)
	tt := []Track{}
	for _, t := range all {
		if t.Album.ID == id {
			tt = append(tt, t)
		}
	}
	sortTracks(tt)
	return a, tt, err
}
func (s *OwnedSource) Artist(c context.Context, id int64) (Artist, []Album, error) {
	if !ownedID(id) {
		if s.Remote != nil {
			return s.Remote.Artist(c, id)
		}
		return Artist{}, nil, ErrNotFound
	}
	a, err := ownedGet[Artist](c, s.DB, KindArtist, id)
	if err != nil {
		return a, nil, err
	}
	all, err := ownedRelated[Album](c, s.DB, KindAlbum, "artist_id", id)
	aa := []Album{}
	for _, al := range all {
		if al.Artist.ID == id {
			aa = append(aa, al)
		}
	}
	return a, aa, err
}
func (s *OwnedSource) Stream(c context.Context, id int64, q string) (Stream, error) {
	if ownedID(id) && !ownedAllowed(c, s.DB, core.CatalogViewerID(c), KindTrack, id) {
		return Stream{}, ErrNotFound
	}
	if id >= ibBase && id < ibBase+localmedia.Span {
		if s.Account != nil {
			var account int64
			if s.DB.QueryRowContext(c, "SELECT account_id FROM pse_ibroadcast_tracks WHERE id=?", id).Scan(&account) != nil {
				return Stream{}, ErrNotFound
			}
			ib, err := s.Account(c, account)
			if err != nil {
				return Stream{}, ErrNotFound
			}
			return ib.Stream(c, id, q)
		}
		if s.IB != nil {
			return s.IB.Stream(c, id, q)
		}
	}
	if id >= localmedia.AudioBase && id < localmedia.AudioBase+localmedia.Span && s.Local != nil {
		f, meta, err := s.Local.Open(c, id)
		if err != nil {
			return Stream{}, ErrNotFound
		}
		f.Close()
		return Stream{Provider: "local", Mime: meta.MIME, Quality: "ORIGINAL", Open: func() (io.ReadSeekCloser, time.Time, error) {
			f, m, e := s.Local.Open(c, id)
			return f, time.Unix(0, m.Modified), e
		}}, nil
	}
	if s.Remote != nil && !ownedID(id) {
		return s.Remote.Stream(c, id, q)
	}
	return Stream{}, ErrNotFound
}
func (s *OwnedSource) ImageURL(k byte, ref string, size int) string {
	if strings.HasPrefix(ref, "ib-") && s.IB != nil {
		return s.IB.ImageURL(ref, size)
	}
	if s.Remote != nil {
		return s.Remote.ImageURL(k, ref, size)
	}
	return ""
}
func (s *OwnedSource) ChartAlbums(c context.Context, n int) ([]Album, error) {
	var remote func(context.Context, string, int, int) ([]Album, error)
	if ch, ok := s.Remote.(Charter); ok {
		remote = func(c context.Context, _ string, n, o int) ([]Album, error) {
			v, e := ch.ChartAlbums(c, n+o)
			if o >= len(v) {
				return nil, e
			}
			return v[o:], e
		}
	}
	return joinedSearch[Album](c, s, KindAlbum, "", n, 0, remote)
}
func (s *OwnedSource) ChartArtists(c context.Context, n int) ([]Artist, error) {
	var remote func(context.Context, string, int, int) ([]Artist, error)
	if ch, ok := s.Remote.(Charter); ok {
		remote = func(c context.Context, _ string, n, o int) ([]Artist, error) {
			v, e := ch.ChartArtists(c, n+o)
			if o >= len(v) {
				return nil, e
			}
			return v[o:], e
		}
	}
	return joinedSearch[Artist](c, s, KindArtist, "", n, 0, remote)
}
func (s *OwnedSource) ChartTracks(c context.Context, n int) ([]Track, error) {
	var remote func(context.Context, string, int, int) ([]Track, error)
	if ch, ok := s.Remote.(Charter); ok {
		remote = func(c context.Context, _ string, n, o int) ([]Track, error) {
			v, e := ch.ChartTracks(c, n+o)
			if o >= len(v) {
				return nil, e
			}
			return v[o:], e
		}
	}
	return joinedSearch[Track](c, s, KindTrack, "", n, 0, remote)
}
func (s *OwnedSource) ArtistTop(c context.Context, id int64, n int) ([]Track, error) {
	if !ownedID(id) {
		if t, ok := s.Remote.(TopTracker); ok {
			return t.ArtistTop(c, id, n)
		}
		return nil, nil
	}
	_, aa, err := s.Artist(c, id)
	tt := []Track{}
	for _, a := range aa {
		_, t, e := s.Album(c, a.ID)
		if e != nil {
			return nil, e
		}
		tt = append(tt, t...)
	}
	if len(tt) > n {
		tt = tt[:n]
	}
	return tt, err
}

// ReplaceOwned publishes a complete library snapshot transactionally. It never
// rewrites personal favourites, ratings or listening history.
func ReplaceOwned(ctx context.Context, db *sql.DB, provider string, tracks []Track) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "DELETE FROM pse_music_owned WHERE provider=?", provider); err != nil {
		return err
	}
	artists := map[int64]Artist{}
	albums := map[int64]Album{}
	put := func(kind string, id int64, name string, v any) error {
		raw, e := json.Marshal(v)
		if e != nil {
			return e
		}
		var album, artist int64
		switch x := v.(type) {
		case Track:
			album, artist = x.Album.ID, x.Artist.ID
		case Album:
			artist = x.Artist.ID
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO pse_music_owned(kind,item_id,provider,name,data,added_at,album_id,artist_id) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(kind,item_id) DO UPDATE SET name=excluded.name,data=excluded.data,album_id=excluded.album_id,artist_id=excluded.artist_id", kind, id, provider, strings.ToLower(name), string(raw), time.Now().Unix(), album, artist)
		return e
	}
	for _, t := range tracks {
		if !ownedID(t.ID) {
			return errors.New("invalid owned music ID")
		}
		artists[t.Artist.ID] = t.Artist
		artists[t.Album.Artist.ID] = t.Album.Artist
		a := albums[t.Album.ID]
		if a.ID == 0 {
			a = t.Album
			a.Tracks = 0
			a.Duration = 0
		}
		a.Tracks++
		a.Duration += t.Duration
		albums[a.ID] = a
		if err = put(KindTrack, t.ID, t.Title+" "+t.Artist.Name+" "+t.Album.Title, t); err != nil {
			return err
		}
	}
	for _, a := range artists {
		if err = put(KindArtist, a.ID, a.Name, a); err != nil {
			return err
		}
	}
	for _, a := range albums {
		if err = put(KindAlbum, a.ID, a.Title+" "+a.Artist.Name, a); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func ImportLocal(ctx context.Context, db *sql.DB, r localmedia.Root, files []localmedia.File) error {
	tt := make([]Track, 0, len(files))
	for _, f := range files {
		artist := Artist{ID: localmedia.ID("local-artist:"+r.ID, f.Artist, localmedia.AudioBase), Name: f.Artist}
		album := Album{ID: localmedia.ID("local-album:"+r.ID, f.Artist+"\x00"+f.Album, localmedia.AudioBase), Title: f.Album, Artist: artist}
		tt = append(tt, Track{MIME: f.MIME, Container: f.Container, Codec: f.Codec, Channels: f.Channels, SampleRate: f.SampleRate, ID: f.ID, Title: f.Name, Duration: f.Duration, Number: f.Number, Volume: f.Disc, Artist: artist, Album: album})
	}
	return ReplaceOwned(ctx, db, "local:"+r.ID, tt)
}
func sortTracks(t []Track) {
	sort.SliceStable(t, func(i, j int) bool {
		if t[i].Volume != t[j].Volume {
			return t[i].Volume < t[j].Volume
		}
		if t[i].Number != t[j].Number {
			return t[i].Number < t[j].Number
		}
		return t[i].Title < t[j].Title
	})
}

// Personal state wins, while current imported metadata stays fresh. Owned rows
// are visible in the regular library without synthesising plays or favourites.
func libraryItemsSQL(viewer int64) string {
	return `(SELECT p.viewer_id,p.kind,p.item_id,p.name,COALESCE(o.data,p.data) AS data,p.added_at,p.starred_at,p.play_count,p.played_at FROM pse_music_items p LEFT JOIN pse_music_owned o ON o.kind=p.kind AND o.item_id=p.item_id WHERE (p.item_id<4000000000000 OR p.item_id>=7000000000000 OR ` + mediaaccess.Predicate("o.provider", viewer) + `)
UNION ALL SELECT ?,o.kind,o.item_id,o.name,o.data,o.added_at,0,0,0 FROM pse_music_owned o WHERE ` + mediaaccess.Predicate("o.provider", viewer) + ` AND NOT EXISTS(SELECT 1 FROM pse_music_items p WHERE p.viewer_id=? AND p.kind=o.kind AND p.item_id=o.item_id))`
}

func (s *OwnedSource) Genres(c context.Context) ([]Genre, error) {
	if d, ok := s.Remote.(Discoverer); ok {
		return d.Genres(c)
	}
	return nil, nil
}
func (s *OwnedSource) GenreArtists(c context.Context, g int64, n int) ([]Artist, error) {
	if d, ok := s.Remote.(Discoverer); ok {
		return d.GenreArtists(c, g, n)
	}
	return nil, nil
}
func (s *OwnedSource) GenreAlbums(c context.Context, g int64, n int) ([]Album, error) {
	if d, ok := s.Remote.(Discoverer); ok {
		return d.GenreAlbums(c, g, n)
	}
	return nil, nil
}
func (s *OwnedSource) GenreTracks(c context.Context, g int64, n int) ([]Track, error) {
	if d, ok := s.Remote.(Discoverer); ok {
		return d.GenreTracks(c, g, n)
	}
	return nil, nil
}
func (s *OwnedSource) Related(c context.Context, g int64, n int) ([]Artist, error) {
	if !ownedID(g) {
		if d, ok := s.Remote.(Discoverer); ok {
			return d.Related(c, g, n)
		}
	}
	return nil, nil
}
func (s *OwnedSource) ArtistRadio(c context.Context, g int64, n int) ([]Track, error) {
	if ownedID(g) {
		return s.ArtistTop(c, g, n)
	}
	if d, ok := s.Remote.(Discoverer); ok {
		return d.ArtistRadio(c, g, n)
	}
	return nil, nil
}

func ownedRelated[T any](ctx context.Context, db *sql.DB, kind, column string, id int64) ([]T, error) {
	rows, err := db.QueryContext(ctx, "SELECT data FROM pse_music_owned WHERE "+mediaaccess.Predicate("provider", core.CatalogViewerID(ctx))+" AND kind=? AND "+column+"=? ORDER BY name,item_id", kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var v T
		if err = json.Unmarshal([]byte(raw), &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Server) trackContentType(t Track) (string, string) {
	if t.Container != "" {
		return t.MIME, t.Container
	}
	return s.contentType()
}
func trackCodec(t Track, suffix string) string {
	if t.Codec != "" {
		return t.Codec
	}
	if suffix == "m4a" {
		return "aac"
	}
	return suffix
}
func trackChannels(t Track) int {
	if t.Channels > 0 {
		return t.Channels
	}
	return 2
}
func trackSampleRate(t Track) int {
	if t.SampleRate > 0 {
		return t.SampleRate
	}
	return 44100
}

func withMusicViewer(ctx context.Context, viewer int64) context.Context {
	return core.WithCatalogViewer(ctx, &core.User{ID: viewer})
}
func ownedAllowed(ctx context.Context, db *sql.DB, viewer int64, kind string, id int64) bool {
	if !ownedID(id) {
		return true
	}
	var n int
	return db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pse_music_owned WHERE kind=? AND item_id=? AND "+mediaaccess.Predicate("provider", viewer), kind, id).Scan(&n) == nil && n > 0
}

func (s *OwnedSource) artAllowed(ctx context.Context, ref string) bool {
	var n int
	return s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM pse_music_owned WHERE kind='album' AND data LIKE ? AND "+mediaaccess.Predicate("provider", core.CatalogViewerID(ctx)), `%"cover":"`+ref+`"%`).Scan(&n) == nil && n > 0
}
