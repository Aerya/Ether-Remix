package livetv

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// TimeshiftMinutes is the owner default used by new IPTV outputs.
func (s *Service) TimeshiftMinutes() int {
	if s.o.TimeshiftMinutes != nil {
		return min(max(s.o.TimeshiftMinutes(), 0), config.MaxTimeshiftMinutes)
	}
	return config.DefaultTimeshiftMinutes
}

func (s *Service) timeshiftWindow(c Channel) time.Duration {
	minutes := s.TimeshiftMinutes()
	if c.DVRMinutes > 0 {
		minutes = min(c.DVRMinutes, config.MaxTimeshiftMinutes)
	}
	return time.Duration(minutes) * time.Minute
}

func (s *Service) liveDVRCache() *transcode.LiveDVRCache {
	s.dvrOnce.Do(func() {
		budget := s.o.TimeshiftBudgetBytes
		if budget == nil {
			budget = func() int64 { return config.DefaultCacheBudgetBytes }
		}
		s.dvr = transcode.NewLiveDVRCache(s.o.TimeshiftDir, budget)
	})
	return s.dvr
}

func (s *Service) enableOwnedTimeshift(t *ownedTimeline, c Channel) {
	if c.Kind == "iptv" {
		t.dvrWindow = s.timeshiftWindow(c)
		if t.dvrWindow > 0 {
			t.dvr = s.liveDVRCache().Open()
		}
	}
}

// LiveTimeshiftMinutes returns the configured limit for an already published
// IPTV window, or zero when only its live suffix is currently playable.
func (s *Service) LiveTimeshiftMinutes(ctx context.Context, id string) int {
	if ctx.Err() != nil {
		return 0
	}
	s.mu.Lock()
	ss := s.sessions[id]
	s.mu.Unlock()
	if ss == nil {
		return 0
	}
	ss.mu.Lock()
	broadcast, owned, closed := ss.broadcast, ss.owned, ss.closed
	ss.mu.Unlock()
	if closed {
		return 0
	}
	if owned != nil {
		t := owned.tl
		t.mu.Lock()
		defer t.mu.Unlock()
		t.trimWindowLocked()
		if len(t.window) > t.liveSegmentCountLocked() {
			return int(t.dvrWindow / time.Minute)
		}
		return 0
	}
	if limit := s.broadcastTimeshift(broadcast); limit > 0 {
		index, err := broadcast.output()
		if err == nil {
			if latest, ok := index.Latest(); ok {
				end := latest.Start.Add(latest.Duration)
				first := index.Since(-1, 1)
				if len(first) != 0 && !first[0].Start.Add(first[0].Duration).After(end.Add(-broadcast.broadcast.dvrLiveWindow)) {
					return int(limit / time.Minute)
				}
			}
		}
	}
	return 0
}

func (s *Service) broadcastTimeshift(lease *broadcastLease) time.Duration {
	if lease != nil && lease.broadcast.dvrLiveWindow > 0 {
		return lease.broadcast.dvr
	}
	return 0
}

func (s *Service) broadcastWindowStart(lease *broadcastLease, end time.Time) time.Time {
	if s.broadcastTimeshift(lease) > 0 {
		return time.Time{}
	}
	return end.Add(-s.latencyPolicy().PlaylistWindow())
}
