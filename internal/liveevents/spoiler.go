package liveevents

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

const (
	spoilerPrefSurface = "vela"
	spoilerPrefClient  = "live"
	spoilerPrefKey     = "spoilerFree"

	// MaxRevealsPerViewer bounds persisted and cached event reveal overrides per viewer.
	MaxRevealsPerViewer = 512
	// MaxCachedViewerSpoilers bounds the in-memory viewer spoiler cache.
	MaxCachedViewerSpoilers = 4096
)

func init() {
	store.RegisterAfter("live_event_reveals", "auth")
	store.Register("live_event_reveals", 1, `
CREATE TABLE live_event_reveals (
	viewer_id INTEGER NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
	event_id TEXT NOT NULL,
	revealed_at BIGINT NOT NULL,
	PRIMARY KEY(viewer_id, event_id)
);
CREATE INDEX live_event_reveals_viewer_time ON live_event_reveals(viewer_id, revealed_at);
`)
	store.RegisterViewerTable("liveevents", "live_event_reveals", "viewer_id", store.ViewerMergeMove, "event_id")
}

// SpoilerStore manages per-viewer spoilerFree preferences (stored in
// frontend_preferences) and per-viewer event reveal overrides (stored in
// live_event_reveals). When DB is nil, it operates purely in memory with the
// same bounds (useful in unit tests).
type SpoilerStore struct {
	db       *sql.DB
	clock    Clock
	onMutate func(viewerID int64)

	mu          sync.Mutex
	spoilerFree map[int64]bool
	reveals     map[int64]map[string]int64 // viewerID -> eventID -> revealedAtUnixNano
	viewerTouch map[int64]int64            // viewerID -> touch seq for bounded LRU eviction
	seq         int64
}

// NewSpoilerStore constructs a SpoilerStore. db may be nil for in-memory operation.
func NewSpoilerStore(db *sql.DB, clock Clock, onMutate func(viewerID int64)) *SpoilerStore {
	if clock == nil {
		clock = realClock{}
	}
	return &SpoilerStore{
		db:          db,
		clock:       clock,
		onMutate:    onMutate,
		spoilerFree: make(map[int64]bool),
		reveals:     make(map[int64]map[string]int64),
		viewerTouch: make(map[int64]int64),
	}
}

// SpoilerFree reports whether spoiler-free mode is enabled for viewerID.
// Defaults to false when unset or viewerID <= 0.
func (s *SpoilerStore) SpoilerFree(ctx context.Context, viewerID int64) (bool, error) {
	if s == nil || viewerID <= 0 {
		return false, nil
	}
	s.mu.Lock()
	if val, ok := s.spoilerFree[viewerID]; ok {
		s.touchLocked(viewerID)
		s.mu.Unlock()
		return val, nil
	}
	s.mu.Unlock()

	if s.db == nil {
		return false, nil
	}
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT p.value FROM frontend_preferences p JOIN auth_users u ON u.id=p.viewer_id WHERE p.viewer_id=? AND p.surface=? AND p.client=? AND p.pref_key=? AND u.active=1`,
		viewerID, spoilerPrefSurface, spoilerPrefClient, spoilerPrefKey,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		s.mu.Lock()
		s.setSpoilerCacheLocked(viewerID, false)
		s.mu.Unlock()
		return false, nil
	}
	if err != nil {
		return false, err
	}
	val := strings.EqualFold(strings.TrimSpace(raw), "true")
	s.mu.Lock()
	s.setSpoilerCacheLocked(viewerID, val)
	s.mu.Unlock()
	return val, nil
}

// SetSpoilerFree updates viewerID's spoilerFree preference.
func (s *SpoilerStore) SetSpoilerFree(ctx context.Context, viewerID int64, enabled bool) error {
	if s == nil || viewerID <= 0 {
		return core.ErrUnauthorized
	}
	if s.db != nil {
		val := "false"
		if enabled {
			val = "true"
		}
		now := s.clock.Now().UnixMilli()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM auth_users WHERE id=? AND active=1`, viewerID).Scan(&active); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return core.ErrUnauthorized
			}
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO frontend_preferences(viewer_id,surface,client,pref_key,value,revision,updated_at) VALUES(?,?,?,?,?,1,?)
			 ON CONFLICT(viewer_id,surface,client,pref_key) DO UPDATE SET value=excluded.value,revision=frontend_preferences.revision+1,updated_at=excluded.updated_at`,
			viewerID, spoilerPrefSurface, spoilerPrefClient, spoilerPrefKey, val, now,
		); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.setSpoilerCacheLocked(viewerID, enabled)
	s.mu.Unlock()
	if s.onMutate != nil {
		s.onMutate(viewerID)
	}
	return nil
}

// IsRevealed reports whether viewerID has explicitly revealed scores for eventID.
func (s *SpoilerStore) IsRevealed(ctx context.Context, viewerID int64, eventID string) (bool, error) {
	if s == nil || viewerID <= 0 || eventID == "" {
		return false, nil
	}
	s.mu.Lock()
	if set, ok := s.reveals[viewerID]; ok {
		s.touchLocked(viewerID)
		_, revealed := set[eventID]
		s.mu.Unlock()
		return revealed, nil
	}
	s.mu.Unlock()

	if s.db == nil {
		return false, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT r.event_id, r.revealed_at FROM live_event_reveals r JOIN auth_users u ON u.id=r.viewer_id WHERE r.viewer_id=? AND u.active=1 ORDER BY r.revealed_at DESC LIMIT ?`,
		viewerID, MaxRevealsPerViewer,
	)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	loaded := make(map[string]int64)
	for rows.Next() {
		var id string
		var at int64
		if err := rows.Scan(&id, &at); err != nil {
			return false, err
		}
		loaded[id] = at
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	s.reveals[viewerID] = loaded
	s.touchLocked(viewerID)
	s.evictViewersLocked()
	_, revealed := loaded[eventID]
	s.mu.Unlock()
	return revealed, nil
}

// Reveal records an explicit per-viewer override revealing scores for eventID.
func (s *SpoilerStore) Reveal(ctx context.Context, viewerID int64, eventID string) error {
	if s == nil || viewerID <= 0 {
		return core.ErrUnauthorized
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return ErrInvalidRequest
	}
	now := s.clock.Now().UnixNano()
	if s.db != nil {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM auth_users WHERE id=? AND active=1`, viewerID).Scan(&active); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return core.ErrUnauthorized
			}
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO live_event_reveals(viewer_id,event_id,revealed_at) VALUES(?,?,?)
			 ON CONFLICT(viewer_id,event_id) DO UPDATE SET revealed_at=excluded.revealed_at`,
			viewerID, eventID, now,
		); err != nil {
			return err
		}
		// Keep persisted reveals bounded per viewer.
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM live_event_reveals WHERE viewer_id=?`, viewerID).Scan(&count); err != nil {
			return err
		}
		if count > MaxRevealsPerViewer {
			excess := count - MaxRevealsPerViewer
			rows, err := tx.QueryContext(ctx,
				`SELECT event_id FROM live_event_reveals WHERE viewer_id=? ORDER BY revealed_at ASC, event_id ASC LIMIT ?`,
				viewerID, excess,
			)
			if err != nil {
				return err
			}
			var drop []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				drop = append(drop, id)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, id := range drop {
				if _, err := tx.ExecContext(ctx, `DELETE FROM live_event_reveals WHERE viewer_id=? AND event_id=?`, viewerID, id); err != nil {
					return err
				}
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}

	s.mu.Lock()
	set := s.reveals[viewerID]
	if set == nil {
		set = make(map[string]int64)
		s.reveals[viewerID] = set
	}
	set[eventID] = now
	if len(set) > MaxRevealsPerViewer {
		evictOldestReveal(set)
	}
	s.touchLocked(viewerID)
	s.evictViewersLocked()
	s.mu.Unlock()

	if s.onMutate != nil {
		s.onMutate(viewerID)
	}
	return nil
}

// InvalidateViewer drops cached spoiler state for one viewer (e.g. on cluster update).
func (s *SpoilerStore) InvalidateViewer(viewerID int64) {
	if s == nil || viewerID <= 0 {
		return
	}
	s.mu.Lock()
	delete(s.spoilerFree, viewerID)
	delete(s.reveals, viewerID)
	delete(s.viewerTouch, viewerID)
	s.mu.Unlock()
}

// InvalidateAll drops all in-memory spoiler cache entries (e.g. on clusterbus resync).
func (s *SpoilerStore) InvalidateAll() {
	if s == nil || s.db == nil {
		return
	}
	s.mu.Lock()
	s.spoilerFree = make(map[int64]bool)
	s.reveals = make(map[int64]map[string]int64)
	s.viewerTouch = make(map[int64]int64)
	s.mu.Unlock()
}

func (s *SpoilerStore) setSpoilerCacheLocked(viewerID int64, val bool) {
	s.spoilerFree[viewerID] = val
	s.touchLocked(viewerID)
	s.evictViewersLocked()
}

func (s *SpoilerStore) touchLocked(viewerID int64) {
	s.seq++
	s.viewerTouch[viewerID] = s.seq
}

func (s *SpoilerStore) evictViewersLocked() {
	for len(s.viewerTouch) > MaxCachedViewerSpoilers {
		var oldestID int64
		var oldestSeq int64
		first := true
		for id, seq := range s.viewerTouch {
			if first || seq < oldestSeq {
				oldestID = id
				oldestSeq = seq
				first = false
			}
		}
		if first {
			break
		}
		delete(s.viewerTouch, oldestID)
		delete(s.spoilerFree, oldestID)
		delete(s.reveals, oldestID)
	}
}

func evictOldestReveal(set map[string]int64) {
	var oldestKey string
	var oldestAt int64
	first := true
	for k, at := range set {
		if first || at < oldestAt {
			oldestKey = k
			oldestAt = at
			first = false
		}
	}
	if !first {
		delete(set, oldestKey)
	}
}

func eventHasSpoilerContent(ev core.LiveEvent) bool {
	switch ev.Status.State {
	case core.LiveEventStateIn, core.LiveEventStatePost, core.LiveEventStateDelayed:
		return true
	}
	if ev.Probability != nil || len(ev.Ranking) > 0 || len(ev.Situation) > 0 || ev.Status.Close {
		return true
	}
	for _, c := range ev.Competitors {
		if c.Score != "" || c.Winner {
			return true
		}
	}
	return false
}

// MaskEvent returns a spoiler-safe copy of ev for a viewer with spoilerFree
// enabled who has not revealed ev.
//
// In-progress, delayed, finished, or score-bearing events have scores, winners,
// ranking, situation, probability, and live status details stripped and
// Spoiler set to true. Upcoming (pre), postponed, or canceled events without
// spoiler data keep their schedule metadata with Spoiler=false.
func MaskEvent(ev core.LiveEvent) core.LiveEvent {
	out := ev.Clone()
	if !eventHasSpoilerContent(out) {
		out.Spoiler = false
		out.Probability = nil
		return out
	}
	out.Spoiler = true
	for i := range out.Competitors {
		out.Competitors[i].Score = ""
		out.Competitors[i].Winner = false
	}
	out.Ranking = nil
	out.Situation = nil
	out.Probability = nil
	out.Status.Detail = ""
	out.Status.Period = 0
	out.Status.Clock = ""
	out.Status.Close = false
	return out
}

// MaskDetail returns a spoiler-safe copy of d for a viewer with spoilerFree
// enabled who has not revealed d.
func MaskDetail(d core.LiveEventDetail) core.LiveEventDetail {
	out := d.Clone()
	out.LiveEvent = MaskEvent(out.LiveEvent)
	if out.Spoiler || len(out.Plays) > 0 || len(out.ProbabilitySeries) > 0 || len(out.Tables) > 0 {
		if out.Spoiler || len(out.Plays) > 0 || len(out.ProbabilitySeries) > 0 {
			out.Spoiler = true
		}
		if out.Spoiler {
			out.Plays = nil
			out.ProbabilitySeries = nil
			out.Tables = nil
		}
	}
	return out
}

// AllowAlertWhenSpoilerFree reports whether an alert of the given kind may be
// delivered to a viewer with spoilerFree enabled. Per spec A7, spoiler-free
// viewers receive start alerts only.
func AllowAlertWhenSpoilerFree(kind string) bool {
	return kind == core.LiveEventAlertStarted
}

// bumpUpdatedAt returns an RFC3339 timestamp strictly greater than prevUpdatedAt
// (both chronologically and lexicographically) so clients merging updates by
// updatedAt never ignore a reveal or content change whose timestamp would tie.
func bumpUpdatedAt(prevUpdatedAt string, now time.Time) string {
	nowUTC := now.UTC().Truncate(time.Second)
	if prevUpdatedAt == "" {
		return nowUTC.Format(time.RFC3339)
	}
	prev, err := time.Parse(time.RFC3339Nano, prevUpdatedAt)
	if err != nil {
		return nowUTC.Format(time.RFC3339)
	}
	prevSec := prev.UTC().Truncate(time.Second)
	if !nowUTC.After(prevSec) {
		return prevSec.Add(time.Second).Format(time.RFC3339)
	}
	return nowUTC.Format(time.RFC3339)
}
