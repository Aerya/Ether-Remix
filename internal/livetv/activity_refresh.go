package livetv

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("livetv", 15, `CREATE TABLE livetv_refresh_leases(id TEXT PRIMARY KEY, holder TEXT NOT NULL, until_ms BIGINT NOT NULL);`)
}

const (
	clientIdleWindow    = 5 * time.Minute
	onDemandInUseWindow = 15 * time.Minute
)

func (s *Service) noteSourceUsedLocked(sourceID string) {
	if sourceID == "" {
		return
	}
	if s.sourceUsed == nil {
		s.sourceUsed = map[string]time.Time{}
	}
	s.sourceUsed[sourceID] = s.o.Now()
}

func (s *Service) sourceInUse(sourceID string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.sessions {
		if ss.info.Channel.SourceID == sourceID {
			return true
		}
	}
	if !s.lastClientActivity.IsZero() && now.Sub(s.lastClientActivity) < onDemandInUseWindow {
		return true
	}
	if at, ok := s.sourceUsed[sourceID]; ok && now.Sub(at) < onDemandInUseWindow {
		return true
	}
	return false
}

func (s *Service) onDemandInterval(src Source) time.Duration {
	if src.Disabled || src.RefreshHours < 0 {
		return 0
	}
	k, ok := s.kindRegistry().Lookup(src.Kind)
	if !ok {
		return 0
	}
	od, ok := k.(OnDemandRefreshKind)
	if !ok {
		return 0
	}
	d := od.OnDemandRefreshInterval()
	if d <= 0 {
		return 0
	}
	return d
}

func onDemandNextRefresh(src Source, interval time.Duration) time.Time {
	next := src.UpdatedAt.Add(interval)
	if src.LastErrorAt != nil && src.LastErrorAt.After(src.UpdatedAt) {
		backoff := maxRefreshBackoff
		if n := max(src.RefreshFailures, 1); n <= 7 {
			backoff = min(interval<<(n-1), maxRefreshBackoff)
		}
		if retry := src.LastErrorAt.Add(backoff); retry.After(next) {
			next = retry
		}
	}
	return next.UTC()
}

func (s *Service) activityRefreshDue(src Source, now time.Time) bool {
	if od := s.onDemandInterval(src); od > 0 {
		return !now.Before(onDemandNextRefresh(src, od))
	}
	return activityRefreshDue(src, now)
}

// NoteClientActivity is called only after client authentication. It does no
// database or provider work. The joined refresh loop coalesces wakeups and serves
// requests from the last imported snapshot while an update is in progress.
func (s *Service) NoteClientActivity() {
	now := s.o.Now()
	s.mu.Lock()
	idle := (s.lastClientActivity.IsZero() || now.Sub(s.lastClientActivity) >= clientIdleWindow) && len(s.sessions) == 0
	s.lastClientActivity = now
	s.mu.Unlock()
	if idle && s.serviceContext().Err() == nil {
		select {
		case s.activityWake <- struct{}{}:
		default:
		}
	}
}

func activityRefreshDue(src Source, now time.Time) bool {
	if _, enabled := refreshInterval(src); !enabled || src.ActivityRefreshMinutes <= 0 {
		return false
	}
	cooldown := time.Duration(max(30, src.ActivityRefreshMinutes)) * time.Minute
	next := src.UpdatedAt.Add(cooldown)
	if src.LastErrorAt != nil && src.LastErrorAt.After(src.UpdatedAt) {
		if retry, ok := nextRefresh(src, nil); ok && retry.After(next) {
			next = retry
		}
		if retry := src.LastErrorAt.Add(cooldown); retry.After(next) {
			next = retry
		}
	}
	return !now.Before(next)
}

// acquireRefresh admits one upstream refresh across nodes without holding a
// database transaction open during network I/O. Expiry recovers crashed owners.
func (s *Service) acquireRefresh(ctx context.Context, id string) (func(), error) {
	now, holder := s.o.Now(), randomID()
	result, err := s.o.DB.ExecContext(ctx, `INSERT INTO livetv_refresh_leases(id,holder,until_ms) VALUES(?,?,?)
ON CONFLICT(id) DO UPDATE SET holder=excluded.holder,until_ms=excluded.until_ms WHERE livetv_refresh_leases.until_ms<=?`, id, holder, now.Add(refreshBudget+time.Minute).UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, core.ErrNotAvailable
	}
	return func() {
		// A lease left behind blocks owner edits until it expires (31
		// minutes), so a busy database gets a few attempts, not one.
		for attempt := 0; attempt < 3; attempt++ {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := s.o.DB.ExecContext(cleanup, `DELETE FROM livetv_refresh_leases WHERE id=? AND holder=?`, id, holder)
			cancel()
			if err == nil {
				return
			}
			if attempt == 2 {
				s.o.Logger.Warn("Live TV source lease not released; it expires on its own", "sourceId", id, "error", err)
			}
		}
	}, nil
}

// errSourceBusy is what an owner edit gets while another import of the same
// source is still running after the edit waited for it.
var errSourceBusy = fmt.Errorf("this source is still updating; try again in a minute: %w", core.ErrNotAvailable)

// ownerEditLeaseWait is how long an owner edit waits for a running import
// of the same source.
var ownerEditLeaseWait = 90 * time.Second

// acquireRefreshWait is acquireRefresh for owner edits: a scheduled refresh
// of the same source is usually seconds from done, so the edit waits for it
// rather than failing at once.
func (s *Service) acquireRefreshWait(ctx context.Context, id string, wait time.Duration) (func(), error) {
	deadline := time.Now().Add(wait)
	for {
		release, err := s.acquireRefresh(ctx, id)
		if !errors.Is(err, core.ErrNotAvailable) {
			return release, err
		}
		if !time.Now().Before(deadline) {
			return nil, errSourceBusy
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
