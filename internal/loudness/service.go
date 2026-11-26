package loudness

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("loudness", 1, `
CREATE TABLE loudness_sources (
	rating_key        INTEGER NOT NULL,
	source            TEXT    NOT NULL,
	lufs              REAL    NOT NULL,
	peak_dbfs         REAL    NOT NULL,
	start_ms          INTEGER NOT NULL,
	end_ms            INTEGER NOT NULL,
	fade_in_end_ms    INTEGER NOT NULL,
	fade_out_start_ms INTEGER NOT NULL,
	duration_ms       INTEGER NOT NULL,
	measured_at       INTEGER NOT NULL,
	PRIMARY KEY (rating_key, source)
);
CREATE TABLE loudness_albums (
	parent_key  INTEGER PRIMARY KEY,
	lufs        REAL    NOT NULL,
	peak_dbfs   REAL    NOT NULL,
	tracks      INTEGER NOT NULL,
	measured_at INTEGER NOT NULL
);`)
}

// Opener opens an item's playable media for a viewer (the source playback
// uses, from the resolver's cache).
type Opener func(ctx context.Context, viewer *core.Viewer, key core.RatingKey) (*frontend.KindMedia, error)

// AlbumOf names an item's album (its parent) and every playable sibling in it,
// itself included; ok is false for items outside an album.
type AlbumOf func(ctx context.Context, key core.RatingKey) (parent core.RatingKey, tracks []core.RatingKey, ok bool)

// Options configure a Service.
type Options struct {
	DB      *sql.DB
	FFmpeg  func() string
	Open    Opener
	AlbumOf AlbumOf
	Logger  *slog.Logger
	Now     func() time.Time
	// Timeout bounds one analysis (default 3 minutes).
	Timeout time.Duration
}

// Service measures items after they are played through and answers the
// measurements in batches.
type Service struct {
	o       Options
	queue   chan job
	mu      sync.Mutex
	pending map[core.RatingKey]bool
}

type job struct {
	viewer int64
	key    core.RatingKey
}

// New builds a Service; Run starts its worker.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout <= 0 {
		o.Timeout = 3 * time.Minute
	}
	return &Service{o: o, queue: make(chan job, 256), pending: map[core.RatingKey]bool{}}
}

// Completed is told when a viewer has played an item through. A plugin item
// with no measurement yet is queued once; a full queue drops it (the next
// play through queues it again).
func (s *Service) Completed(viewerID int64, key core.RatingKey) {
	if s == nil || !key.IsPluginKey() {
		return
	}
	s.mu.Lock()
	if s.pending[key] {
		s.mu.Unlock()
		return
	}
	s.pending[key] = true
	s.mu.Unlock()
	select {
	case s.queue <- job{viewer: viewerID, key: key}:
	default:
		s.done(key)
	}
}

func (s *Service) done(key core.RatingKey) {
	s.mu.Lock()
	delete(s.pending, key)
	s.mu.Unlock()
}

// Run measures queued items one at a time until ctx ends.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.queue:
			if err := s.measure(ctx, j); err != nil && !errors.Is(err, context.Canceled) {
				s.o.Logger.Debug("loudness: not measured", "key", j.key, "err", err)
			}
			s.done(j.key)
		}
	}
}

func (s *Service) measured(ctx context.Context, key core.RatingKey) bool {
	var one int
	err := s.o.DB.QueryRowContext(ctx, `SELECT 1 FROM loudness_sources WHERE rating_key = ? LIMIT 1`, int64(key)).Scan(&one)
	return err == nil
}

func (s *Service) measure(ctx context.Context, j job) error {
	if s.o.DB == nil || s.o.Open == nil || s.o.FFmpeg == nil || s.measured(ctx, j.key) {
		return nil
	}
	media, err := s.o.Open(ctx, &core.Viewer{ID: j.viewer}, j.key)
	if err != nil {
		return err
	}
	defer media.Close()
	if !strings.HasPrefix(strings.ToLower(media.MIME), "audio/") || media.Size <= 0 {
		return nil
	}
	res, err := Analyze(ctx, s.o.FFmpeg(), io.NewSectionReader(media, 0, media.Size), s.o.Timeout)
	if err != nil {
		return err
	}
	if _, err := s.o.DB.ExecContext(ctx, `INSERT INTO loudness_sources(rating_key, source, lufs, peak_dbfs, start_ms, end_ms, fade_in_end_ms, fade_out_start_ms, duration_ms, measured_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(rating_key, source) DO UPDATE SET lufs = excluded.lufs, peak_dbfs = excluded.peak_dbfs, start_ms = excluded.start_ms, end_ms = excluded.end_ms,
		fade_in_end_ms = excluded.fade_in_end_ms, fade_out_start_ms = excluded.fade_out_start_ms, duration_ms = excluded.duration_ms, measured_at = excluded.measured_at`,
		int64(j.key), SourceID(media), res.LUFS, res.PeakDBFS, res.StartMs, res.EndMs, res.FadeInEndMs, res.FadeOutStartMs, res.DurationMs, s.o.Now().Unix()); err != nil {
		return err
	}
	s.o.Logger.Info("loudness: measured", "key", j.key, "lufs", res.LUFS, "gain_db", res.GainDB(), "peak_dbfs", res.PeakDBFS)
	return s.updateAlbum(ctx, j.key)
}

// updateAlbum stores the album's loudness once every track in it is measured.
func (s *Service) updateAlbum(ctx context.Context, key core.RatingKey) error {
	if s.o.AlbumOf == nil {
		return nil
	}
	parent, tracks, ok := s.o.AlbumOf(ctx, key)
	if !ok || parent == 0 || len(tracks) == 0 {
		return nil
	}
	latest, err := s.latest(ctx, tracks)
	if err != nil || len(latest) < len(tracks) {
		return err
	}
	lufs := make([]float64, 0, len(tracks))
	durs := make([]int64, 0, len(tracks))
	peak := math.Inf(-1)
	for _, k := range tracks {
		r := latest[k]
		lufs = append(lufs, r.LUFS)
		durs = append(durs, r.DurationMs)
		peak = math.Max(peak, r.PeakDBFS)
	}
	album := AlbumLUFS(lufs, durs)
	if math.IsInf(album, 0) {
		return nil
	}
	_, err = s.o.DB.ExecContext(ctx, `INSERT INTO loudness_albums(parent_key, lufs, peak_dbfs, tracks, measured_at) VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(parent_key) DO UPDATE SET lufs = excluded.lufs, peak_dbfs = excluded.peak_dbfs, tracks = excluded.tracks, measured_at = excluded.measured_at`,
		int64(parent), album, peak, len(tracks), s.o.Now().Unix())
	return err
}

// latest returns each key's most recent measurement (any source).
func (s *Service) latest(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]Result, error) {
	out := make(map[core.RatingKey]Result, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	args := make([]any, len(keys))
	ph := make([]string, len(keys))
	for i, k := range keys {
		args[i] = int64(k)
		ph[i] = "?"
	}
	rows, err := s.o.DB.QueryContext(ctx, `SELECT rating_key, lufs, peak_dbfs, start_ms, end_ms, fade_in_end_ms, fade_out_start_ms, duration_ms
		FROM loudness_sources WHERE rating_key IN (`+strings.Join(ph, ",")+`) ORDER BY measured_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k int64
		var r Result
		if err := rows.Scan(&k, &r.LUFS, &r.PeakDBFS, &r.StartMs, &r.EndMs, &r.FadeInEndMs, &r.FadeOutStartMs, &r.DurationMs); err != nil {
			return nil, err
		}
		out[core.RatingKey(k)] = r // later rows win: newest measurement
	}
	return out, rows.Err()
}

func (s *Service) albums(ctx context.Context, parents []core.RatingKey) (map[core.RatingKey][2]float64, error) {
	out := map[core.RatingKey][2]float64{}
	if len(parents) == 0 {
		return out, nil
	}
	args := make([]any, len(parents))
	ph := make([]string, len(parents))
	for i, k := range parents {
		args[i] = int64(k)
		ph[i] = "?"
	}
	rows, err := s.o.DB.QueryContext(ctx, `SELECT parent_key, lufs, peak_dbfs FROM loudness_albums WHERE parent_key IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k int64
		var v [2]float64
		if err := rows.Scan(&k, &v[0], &v[1]); err != nil {
			return nil, err
		}
		out[core.RatingKey(k)] = v
	}
	return out, rows.Err()
}

// PageLoudness answers one page of titles in two indexed queries; titles
// without a measurement are absent.
func (s *Service) PageLoudness(ctx context.Context, refs []frontend.LoudnessRef) map[core.RatingKey]core.Loudness {
	out := map[core.RatingKey]core.Loudness{}
	if s == nil || s.o.DB == nil || len(refs) == 0 {
		return out
	}
	keys := make([]core.RatingKey, 0, len(refs))
	var parents []core.RatingKey
	seenParent := map[core.RatingKey]bool{}
	for _, r := range refs {
		if !r.Key.IsPluginKey() {
			continue
		}
		keys = append(keys, r.Key)
		if r.Parent != 0 && !seenParent[r.Parent] {
			seenParent[r.Parent] = true
			parents = append(parents, r.Parent)
		}
	}
	latest, err := s.latest(ctx, keys)
	if err != nil || len(latest) == 0 {
		return out
	}
	albums, _ := s.albums(ctx, parents)
	for _, r := range refs {
		m, ok := latest[r.Key]
		if !ok {
			continue
		}
		l := toLoudness(m)
		if a, ok := albums[r.Parent]; ok && r.Parent != 0 {
			gain := round1(ReferenceLUFS - a[0])
			peak := Result{PeakDBFS: a[1]}.PeakLinear()
			l.AlbumGainDB, l.AlbumPeak = &gain, &peak
		}
		out[r.Key] = l
	}
	return out
}

// ForSource answers the measurement of the exact source playback opened.
func (s *Service) ForSource(ctx context.Context, key core.RatingKey, source string) (core.Loudness, bool) {
	if s == nil || s.o.DB == nil || source == "" {
		return core.Loudness{}, false
	}
	var r Result
	err := s.o.DB.QueryRowContext(ctx, `SELECT lufs, peak_dbfs, start_ms, end_ms, fade_in_end_ms, fade_out_start_ms, duration_ms
		FROM loudness_sources WHERE rating_key = ? AND source = ?`, int64(key), source).
		Scan(&r.LUFS, &r.PeakDBFS, &r.StartMs, &r.EndMs, &r.FadeInEndMs, &r.FadeOutStartMs, &r.DurationMs)
	if err != nil {
		return core.Loudness{}, false
	}
	return toLoudness(r), true
}

func toLoudness(r Result) core.Loudness {
	return core.Loudness{
		TrackGainDB: r.GainDB(),
		TrackPeak:   r.PeakLinear(),
		LUFS:        r.LUFS,
		Source:      core.LoudnessMeasured,
		Trim: &core.LoudnessTrim{
			StartMs: r.StartMs, EndMs: r.EndMs,
			FadeInEndMs: r.FadeInEndMs, FadeOutStartMs: r.FadeOutStartMs,
		},
	}
}

// SourceID names the exact file a measurement belongs to within its item:
// its byte size, which the opened media and every playback plan both know
// (two different files of one song practically never match to the byte).
func SourceID(media *frontend.KindMedia) string {
	if media == nil {
		return ""
	}
	return frontend.LoudnessSource(media.Size)
}
