package resolver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// Owner pins
//
// A pin selects the source for playback. Resolve validates only that release;
// rejection, proven failed health, blocklisting and failover exclusion are errors,
// never permission to substitute another release. Pins override owner quality
// and identity heuristics, but retain source safety gates below. Client codec
// and HDR support are delivery decisions: playback can remux, transcode or
// tone-map the pinned source, or explain why no delivery path exists.
// Sampling timeouts alone allow an unverified start; real source reads remain
// authoritative and the pin never permits automatic substitution.
//
// An explicit viewer version choice overrides a pin. The logical Auto slot
// still honours it. Offline downloads retain their own profile and ignore pins.

// PinStatus is the outcome of the last time a pin was tried or checked.
type PinStatus string

const (
	PinHealthy     PinStatus = "healthy"     // selected for playback; LastReason notes unverified starts
	PinUnhealthy   PinStatus = "unhealthy"   // failed a health check or the NZB is gone
	PinRejected    PinStatus = "rejected"    // a hard gate excluded it for a client
	PinUnavailable PinStatus = "unavailable" // a transient failure (provider outage)
)

// Pin is an owner's release pin for a title.
type Pin struct {
	TitleKey      core.RatingKey `json:"titleKey,string"`
	TitleName     string         `json:"titleName"`
	Release       core.Release   `json:"release"`
	Note          string         `json:"note,omitempty"`
	PinnedAt      time.Time      `json:"pinnedAt"`
	LastStatus    PinStatus      `json:"lastStatus,omitempty"`
	LastReason    string         `json:"lastReason,omitempty"`
	LastCheckedAt time.Time      `json:"lastCheckedAt"`
}

func pinnableKey(k core.RatingKey) bool {
	return k.Valid() && (k.Kind() == core.KindMovie || k.Kind() == core.KindEpisode)
}

// Pin pins rel to a movie or episode, replacing any earlier pin. The full
// release is stored, so the pin works after the release drops out of
// cached search results.
func (s *Service) Pin(ctx context.Context, key core.RatingKey, titleName string, rel core.Release, note string) (Pin, error) {
	if !pinnableKey(key) {
		return Pin{}, ErrNotPinnable
	}
	if rel.ID == "" {
		return Pin{}, fmt.Errorf("resolver: empty release id")
	}
	if s.isBadRelease(rel) {
		return Pin{}, ErrPinBlocked
	}
	if titleName == "" {
		s.con.mu.Lock()
		titleName = s.con.recent[rel.ID].titleName
		s.con.mu.Unlock()
	}
	note = strings.TrimSpace(note)
	p := Pin{TitleKey: key, TitleName: titleName, Release: rel, Note: note, PinnedAt: s.now()}
	if s.db != nil {
		raw, err := json.Marshal(rel)
		if err != nil {
			return Pin{}, err
		}
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO resolver_pins(rating_key, release_id, release_json, title_name, note, pinned_at, last_status, last_reason, last_checked_at)
			 VALUES (?,?,?,?,?,?,'','',0)
			 ON CONFLICT(rating_key) DO UPDATE SET release_id=excluded.release_id, release_json=excluded.release_json,
			   title_name=excluded.title_name, note=excluded.note, pinned_at=excluded.pinned_at,
			   last_status='', last_reason='', last_checked_at=0`,
			int64(key), rel.ID, string(raw), titleName, note, p.PinnedAt.Unix())
		if err != nil {
			return Pin{}, err
		}
	}
	s.con.mu.Lock()
	s.con.pins[key] = p
	s.con.mu.Unlock()
	s.InvalidateDetailVariants()
	s.notifyPin(key)
	s.emitPinCreated(p, "owner")
	return p, nil
}

// Unpin removes a title's pin; it reports whether there was one.
func (s *Service) Unpin(ctx context.Context, key core.RatingKey) (bool, error) {
	s.con.mu.Lock()
	prev, had := s.con.pins[key]
	delete(s.con.pins, key)
	s.con.mu.Unlock()
	if had {
		s.InvalidateDetailVariants()
	}
	if s.db == nil {
		if had {
			s.emitPinRemoved(key, prev, true, "owner")
		}
		return had, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM resolver_pins WHERE rating_key=?`, int64(key))
	if err != nil {
		return had, err
	}
	s.InvalidateDetailVariants()
	s.notifyPin(key)
	n, _ := res.RowsAffected()
	removed := had || n > 0
	if removed {
		s.emitPinRemoved(key, prev, had, "owner")
	}
	return removed, nil
}

// Pins lists every pin, newest first.
func (s *Service) Pins() []Pin {
	s.con.mu.Lock()
	out := make([]Pin, 0, len(s.con.pins))
	for _, p := range s.con.pins {
		out = append(out, p)
	}
	s.con.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].PinnedAt.Equal(out[j].PinnedAt) {
			return out[i].PinnedAt.After(out[j].PinnedAt)
		}
		return out[i].TitleKey < out[j].TitleKey
	})
	return out
}

// PinFor returns a title's pin.
func (s *Service) PinFor(key core.RatingKey) (Pin, bool) {
	s.con.mu.Lock()
	defer s.con.mu.Unlock()
	p, ok := s.con.pins[key]
	return p, ok
}

// pinColumns are the resolver_pins columns scanPin reads.
const pinColumns = `rating_key, release_json, title_name, note, pinned_at, last_status, last_reason, last_checked_at`

// scanPin reads one resolver_pins row (pinColumns).
func scanPin(row interface{ Scan(...any) error }) (Pin, error) {
	var key, pinnedAt, checkedAt int64
	var raw, status string
	var p Pin
	if err := row.Scan(&key, &raw, &p.TitleName, &p.Note, &pinnedAt, &status, &p.LastReason, &checkedAt); err != nil {
		return p, err
	}
	p.TitleKey = core.RatingKey(key)
	if err := json.Unmarshal([]byte(raw), &p.Release); err != nil || p.Release.ID == "" {
		return p, fmt.Errorf("resolver pin %d unreadable: %v", key, err)
	}
	p.PinnedAt = time.Unix(pinnedAt, 0)
	p.LastStatus = PinStatus(status)
	if checkedAt > 0 {
		p.LastCheckedAt = time.Unix(checkedAt, 0)
	}
	return p, nil
}

// loadPins replaces the in-memory pins with the stored ones.
func (s *Service) loadPins(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pinColumns+` FROM resolver_pins`)
	if err != nil {
		s.log.Warn("resolver pins unavailable", "err", err)
		return
	}
	defer rows.Close()
	pins := map[core.RatingKey]Pin{}
	for rows.Next() {
		p, err := scanPin(rows)
		if err != nil {
			s.log.Warn("resolver pin unreadable", "err", err)
			continue
		}
		pins[p.TitleKey] = p
	}
	if rows.Err() != nil {
		return
	}
	s.con.mu.Lock()
	s.con.pins = pins
	s.con.mu.Unlock()
}

// reloadPin re-reads one title's pin after another node changed it.
func (s *Service) reloadPin(ctx context.Context, key core.RatingKey) {
	if s.db == nil || key == 0 {
		return
	}
	p, err := scanPin(s.db.QueryRowContext(ctx, `SELECT `+pinColumns+` FROM resolver_pins WHERE rating_key=?`, int64(key)))
	s.con.mu.Lock()
	switch {
	case err == nil:
		s.con.pins[key] = p
	case errors.Is(err, sql.ErrNoRows):
		delete(s.con.pins, key) // unpinned on another node
	}
	s.con.mu.Unlock()
	s.InvalidateDetailVariants()
}

// notePin records the outcome of trying a title's pin, when releaseID is
// still the pinned release.
func (s *Service) notePin(ctx context.Context, key core.RatingKey, releaseID string, status PinStatus, reason string) {
	s.notePinForClient(ctx, key, releaseID, status, reason, "")
}

func (s *Service) notePinForClient(ctx context.Context, key core.RatingKey, releaseID string, status PinStatus, reason, client string) {
	now := s.now()
	s.con.mu.Lock()
	p, ok := s.con.pins[key]
	matched := ok && p.Release.ID == releaseID
	if matched {
		p.LastStatus, p.LastReason, p.LastCheckedAt = status, reason, now
		s.con.pins[key] = p
	}
	s.con.mu.Unlock()
	if matched {
		s.emitPinStatusEvent(p, status, reason, client)
	}
	if !ok || s.db == nil {
		return
	}
	s.persistBookkeeping(ctx, func(ctx context.Context) {
		_, err := s.db.ExecContext(ctx,
			`UPDATE resolver_pins SET last_status=?, last_reason=?, last_checked_at=? WHERE rating_key=? AND release_id=?`,
			string(status), reason, now.Unix(), int64(key), releaseID)
		if err != nil {
			s.log.Debug("resolver pin status not saved", "title", key, "err", err)
			return
		}
		s.notifyPin(key)
	})
}

// notePinsBlocked marks pins of a newly blocklisted release as failing, so
// the Pinned list shows why the pin is being skipped.
func (s *Service) notePinsBlocked(ctx context.Context, releaseID, reason string) {
	type hit struct {
		key core.RatingKey
		id  string
	}
	s.con.mu.Lock()
	var hits []hit
	for k, p := range s.con.pins {
		if p.Release.ID == releaseID {
			hits = append(hits, hit{key: k, id: p.Release.ID})
		}
	}
	s.con.mu.Unlock()
	for _, h := range hits {
		s.notePin(ctx, h.key, h.id, PinUnhealthy, "blocklisted: "+reason)
	}
}

// pinForResolve returns the title's pin when it applies to req, else nil
// and, when there is a pin, why it does not apply.
func (s *Service) pinForResolve(req core.ResolveRequest) (*Pin, string) {
	if req.Title == nil {
		return nil, ""
	}
	p, ok := s.PinFor(req.Title.Key)
	if !ok {
		return nil, ""
	}
	switch {
	case req.Prefer != "" && req.Prefer != p.Release.ID && !autoVersion(req.Prefer):
		return nil, "skipped: viewer picked another version"
	case req.Client.Download:
		return nil, "skipped: downloads use their own profile"
	}
	return &p, ""
}

// PinnedReleaseID returns the authoritative playback source, if this request
// uses an owner pin. Playback uses it to recheck warm/resume and failover paths
// that can otherwise reuse a release without calling Resolve. An active viewer
// remembered choice overrides the owner pin in the Auto slot.
func (s *Service) PinnedReleaseID(req core.ResolveRequest) string {
	if s.viewerTitleChoiceForResolve(req, nil) != nil || s.viewerShowChoiceForResolve(req) != nil {
		return ""
	}
	if pin, _ := s.pinForResolve(req); pin != nil {
		return pin.Release.ID
	}
	return ""
}

func autoVersion(prefer string) bool {
	slot, ok := parseVersionSlot(prefer)
	return ok && slot.Kind == "auto"
}

// pinRejectReason is the gate set for an owner pin (see the package
// comment above): "" to accept.
func (s *Service) pinRejectReason(rel core.Release, sreq core.SearchRequest, _ core.ClientCaps, exclude map[string]bool) string {
	switch {
	case rel.Password && rel.Protocol == core.ProtocolTorrent:
		return "passworded"
	case exclude[rel.ID]:
		return "excluded (already tried)"
	case s.isBadRelease(rel):
		return "marked bad"
	case nonVideoRelease(rel, nil, sreq):
		return ReasonNotAVideoFile
	case sreq.Type == core.TypeEpisode && rel.Info.FullSeason && rel.Protocol != core.ProtocolTorrent && rel.Protocol != core.ProtocolUsenet:
		return "season pack (skipped in v1)"
	case sreq.Type == core.TypeEpisode && rel.Info.FullSeason && !packCoversSeasonCour(rel.Info, sreq):
		return "season/episode mismatch"
	}
	return ""
}

// withRelease returns results with rel appended when it is not already
// among them.
func withRelease(results []core.Release, rel core.Release) []core.Release {
	for _, r := range results {
		if r.ID == rel.ID {
			return results
		}
	}
	if rel.Info.Title == "" && rel.Info.Height == 0 && rel.Info.Source == core.SourceUnknown {
		rel.Info = releaseparse.Parse(rel.Name)
	}
	return append(results, rel)
}

// pinFirst moves the pinned candidate to the front: Resolve tries it
// before the ranking.
func pinFirst(cands []candidate, pinID string) []candidate {
	if pinID == "" {
		return cands
	}
	for i, c := range cands {
		if c.rel.ID == pinID {
			out := make([]candidate, 0, len(cands))
			out = append(out, c)
			out = append(out, cands[:i]...)
			return append(out, cands[i+1:]...)
		}
	}
	return cands
}

// candidateReason is a candidate's trace reason, for pin status notes.
func candidateReason(tr *Trace, id string) string {
	for _, c := range tr.Candidates {
		if c.ReleaseID == id {
			if c.Reason != "" {
				return c.Reason
			}
			if c.HealthChecked && !c.Healthy {
				return "health: unhealthy"
			}
		}
	}
	return "not playable for this client"
}
