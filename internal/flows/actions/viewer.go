package actions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/events"
	"github.com/braedonsaunders/ether/internal/flows"
	"github.com/braedonsaunders/ether/internal/store"
)

const maxRestrictionRevertAfter = 30 * 24 * time.Hour

func init() {
	store.Register("flows_actions", 1, `
CREATE TABLE flows_actions_viewer_restrictions (
	viewer_id TEXT PRIMARY KEY,
	previous_json TEXT NOT NULL,
	applied_json TEXT NOT NULL,
	reason TEXT NOT NULL DEFAULT '',
	applied_at_ms INTEGER NOT NULL,
	revert_at_ms INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX flows_actions_viewer_restrictions_revert ON flows_actions_viewer_restrictions(revert_at_ms) WHERE revert_at_ms > 0;
`)
	// Restriction snapshots use text keys so string viewer IDs and numeric IDs work uniformly.
	drop := func(ctx context.Context, tx *sql.Tx, viewerID int64) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM flows_actions_viewer_restrictions WHERE viewer_id=?`, strconv.FormatInt(viewerID, 10))
		return err
	}
	store.RegisterViewerPurge("flows_actions", []string{"flows_actions_viewer_restrictions"}, drop)
	store.RegisterViewerMerge("flows_actions", []string{"flows_actions_viewer_restrictions"}, func(ctx context.Context, tx *sql.Tx, fromID, _ int64) error {
		return drop(ctx, tx, fromID)
	})
}

// ViewerRestrictionRecord stores the pre-restriction snapshot for a viewer so
// viewer.unrestrict or automatic revertAfter expiry restores prior settings
// instead of clobbering permanent owner configuration.
type ViewerRestrictionRecord struct {
	ViewerID  string                 `json:"viewerId"`
	Previous  ViewerPolicySnapshot   `json:"previous"`
	Applied   ViewerRestrictionPatch `json:"applied"`
	Reason    string                 `json:"reason,omitempty"`
	AppliedAt time.Time              `json:"appliedAt"`
	RevertAt  time.Time              `json:"revertAt,omitempty"`
}

// RestrictionStore persists temporary viewer restrictions (DB-backed when db is
// non-nil, or in-memory for lightweight tests).
type RestrictionStore struct {
	db  *sql.DB
	now func() time.Time

	mu  sync.Mutex
	mem map[string]ViewerRestrictionRecord
}

// NewRestrictionStore creates a DB-backed RestrictionStore.
func NewRestrictionStore(db *sql.DB, now func() time.Time) *RestrictionStore {
	if now == nil {
		now = time.Now
	}
	return &RestrictionStore{
		db:  db,
		now: now,
		mem: make(map[string]ViewerRestrictionRecord),
	}
}

// NewMemoryRestrictionStore creates an in-memory RestrictionStore.
func NewMemoryRestrictionStore(now func() time.Time) *RestrictionStore {
	return NewRestrictionStore(nil, now)
}

// SaveIfAbsentOrUpdate preserves the original pre-restriction baseline when a
// viewer already has an active restriction record, while updating the applied
// patch, reason, and revert deadline.
func (s *RestrictionStore) SaveIfAbsentOrUpdate(ctx context.Context, rec ViewerRestrictionRecord) error {
	if s == nil {
		return nil
	}
	if rec.AppliedAt.IsZero() {
		rec.AppliedAt = s.now().UTC()
	}
	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if existing, ok := s.mem[rec.ViewerID]; ok {
			rec.Previous = existing.Previous
		}
		s.mem[rec.ViewerID] = rec
		return nil
	}
	if existing, ok, err := s.Get(ctx, rec.ViewerID); err != nil {
		return err
	} else if ok {
		rec.Previous = existing.Previous
	}
	prevJSON, err := json.Marshal(rec.Previous)
	if err != nil {
		return err
	}
	appliedJSON, err := json.Marshal(rec.Applied)
	if err != nil {
		return err
	}
	var revertMS int64
	if !rec.RevertAt.IsZero() {
		revertMS = rec.RevertAt.UnixMilli()
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO flows_actions_viewer_restrictions(viewer_id, previous_json, applied_json, reason, applied_at_ms, revert_at_ms)
VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(viewer_id) DO UPDATE SET
	applied_json = excluded.applied_json,
	reason = excluded.reason,
	applied_at_ms = excluded.applied_at_ms,
	revert_at_ms = excluded.revert_at_ms`,
		rec.ViewerID, string(prevJSON), string(appliedJSON), rec.Reason, rec.AppliedAt.UnixMilli(), revertMS)
	return err
}

// Get returns the active restriction record for viewerID, if any.
func (s *RestrictionStore) Get(ctx context.Context, viewerID string) (ViewerRestrictionRecord, bool, error) {
	if s == nil {
		return ViewerRestrictionRecord{}, false, nil
	}
	if s.db == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		rec, ok := s.mem[viewerID]
		return rec, ok, nil
	}
	var (
		prevRaw    string
		appliedRaw string
		reason     string
		appliedMS  int64
		revertMS   int64
	)
	err := s.db.QueryRowContext(ctx, `
SELECT previous_json, applied_json, reason, applied_at_ms, revert_at_ms
FROM flows_actions_viewer_restrictions
WHERE viewer_id = ?`, viewerID).Scan(&prevRaw, &appliedRaw, &reason, &appliedMS, &revertMS)
	if errors.Is(err, sql.ErrNoRows) {
		return ViewerRestrictionRecord{}, false, nil
	}
	if err != nil {
		return ViewerRestrictionRecord{}, false, err
	}
	rec := ViewerRestrictionRecord{
		ViewerID:  viewerID,
		Reason:    reason,
		AppliedAt: time.UnixMilli(appliedMS).UTC(),
	}
	if revertMS > 0 {
		rec.RevertAt = time.UnixMilli(revertMS).UTC()
	}
	if err := json.Unmarshal([]byte(prevRaw), &rec.Previous); err != nil {
		return ViewerRestrictionRecord{}, false, err
	}
	_ = json.Unmarshal([]byte(appliedRaw), &rec.Applied)
	return rec, true, nil
}

// Delete removes any active restriction record for viewerID.
func (s *RestrictionStore) Delete(ctx context.Context, viewerID string) error {
	if s == nil {
		return nil
	}
	if s.db == nil {
		s.mu.Lock()
		delete(s.mem, viewerID)
		s.mu.Unlock()
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM flows_actions_viewer_restrictions WHERE viewer_id = ?`, viewerID)
	return err
}

// RevertDue restores every temporary viewer restriction whose RevertAt has
// elapsed and removes the stored record. Bounded to 100 viewers per pass.
func (s *RestrictionStore) RevertDue(ctx context.Context, viewers ViewerController) (int, error) {
	if s == nil || viewers == nil {
		return 0, nil
	}
	now := s.now().UTC()
	var due []ViewerRestrictionRecord

	if s.db == nil {
		s.mu.Lock()
		for _, rec := range s.mem {
			if !rec.RevertAt.IsZero() && !rec.RevertAt.After(now) {
				due = append(due, rec)
				if len(due) >= 100 {
					break
				}
			}
		}
		s.mu.Unlock()
	} else {
		rows, err := s.db.QueryContext(ctx, `
SELECT viewer_id, previous_json, applied_json, reason, applied_at_ms, revert_at_ms
FROM flows_actions_viewer_restrictions
WHERE revert_at_ms > 0 AND revert_at_ms <= ?
ORDER BY revert_at_ms ASC
LIMIT 100`, now.UnixMilli())
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var (
				vid, prevRaw, appliedRaw, reason string
				appliedMS, revertMS              int64
			)
			if err := rows.Scan(&vid, &prevRaw, &appliedRaw, &reason, &appliedMS, &revertMS); err != nil {
				rows.Close()
				return 0, err
			}
			rec := ViewerRestrictionRecord{
				ViewerID:  vid,
				Reason:    reason,
				AppliedAt: time.UnixMilli(appliedMS).UTC(),
				RevertAt:  time.UnixMilli(revertMS).UTC(),
			}
			if json.Unmarshal([]byte(prevRaw), &rec.Previous) == nil {
				_ = json.Unmarshal([]byte(appliedRaw), &rec.Applied)
				due = append(due, rec)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return 0, err
		}
		rows.Close()
	}

	reverted := 0
	var firstErr error
	for _, rec := range due {
		patch := snapshotToPatch(rec.Previous)
		if err := viewers.ApplyViewerPolicy(ctx, rec.ViewerID, patch); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := s.Delete(ctx, rec.ViewerID); err != nil && firstErr == nil {
			firstErr = err
		}
		reverted++
	}
	return reverted, firstErr
}

// RunReverter periodically calls RevertDue until ctx is cancelled.
func (s *RestrictionStore) RunReverter(ctx context.Context, viewers ViewerController, interval time.Duration) {
	if s == nil || viewers == nil {
		return
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = s.RevertDue(ctx, viewers)
		}
	}
}

func snapshotToPatch(snap ViewerPolicySnapshot) ViewerRestrictionPatch {
	rd := snap.RemoteDisabled
	mb := snap.MaxBitrateKbps
	sl := snap.StreamLimit
	en := snap.AccountEnabled
	return ViewerRestrictionPatch{
		RemoteDisabled: &rd,
		MaxBitrateKbps: &mb,
		StreamLimit:    &sl,
		AccountEnabled: &en,
	}
}

// NewViewerRestrict constructs the built-in viewer.restrict action handler.
func NewViewerRestrict(deps Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "viewer.restrict",
		Owner:       events.OwnerBuiltin,
		Group:       "viewers",
		Label:       "Restrict viewer",
		Description: "Apply or tighten playback or account restrictions for a viewer, with an optional duration after which prior settings automatically revert.",
		Icon:        "shield-alert",
		Idempotent:  true,
		SideEffect:  true,
		Params: []flows.ParamSchema{
			{Key: "viewerId", Type: events.FieldString, Label: "Viewer", Description: "Viewer ID to restrict.", Required: true, Template: true, Picker: flows.PickerViewer},
			{Key: "remoteDisabled", Type: events.FieldBool, Label: "Disable remote playback", Description: "Prevent playback from outside the local network."},
			{Key: "maxBitrateKbps", Type: events.FieldInt, Label: "Max bitrate", Description: "Cap streaming bitrate in kbps (0 removes cap).", Unit: "kbps"},
			{Key: "streamLimit", Type: events.FieldInt, Label: "Concurrent stream limit", Description: "Cap concurrent streams for this viewer."},
			{Key: "disableAccount", Type: events.FieldBool, Label: "Pause viewer account", Description: "Temporarily disable sign-in and playback for this viewer."},
			{Key: "revertAfter", Type: events.FieldString, Label: "Revert after", Description: "Optional duration after which restrictions revert automatically (for example 30m, 2h, 1d; max 30d).", Template: true},
			{Key: "reason", Type: events.FieldString, Label: "Reason", Description: "Optional note describing why the restriction was applied.", Template: true},
		},
		Outputs: []events.FieldSchema{
			{Name: "viewerId", Type: events.FieldString, Label: "Viewer ID"},
			{Name: "restricted", Type: events.FieldBool, Label: "Restriction applied"},
			{Name: "revertAt", Type: events.FieldTimestamp, Label: "Auto-revert time"},
			{Name: "changes", Type: events.FieldStringList, Label: "Applied fields"},
			{Name: "dryRun", Type: events.FieldBool, Label: "Dry run"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		viewerID := strings.TrimSpace(paramString(params, "viewerId", correlationViewerID(actx)))
		if viewerID == "" {
			return flows.ActionResult{}, errors.New("viewer.restrict: viewerId is required")
		}

		var (
			patch   ViewerRestrictionPatch
			changes []string
		)
		if v, present, err := paramBool(params, "remoteDisabled", false); err != nil {
			return flows.ActionResult{}, fmt.Errorf("viewer.restrict: %w", err)
		} else if present {
			patch.RemoteDisabled = &v
			changes = append(changes, "remoteDisabled")
		}
		if v, present, err := paramInt(params, "maxBitrateKbps", 0); err != nil {
			return flows.ActionResult{}, fmt.Errorf("viewer.restrict: %w", err)
		} else if present {
			if v < 0 || v > 500000 {
				return flows.ActionResult{}, errors.New("viewer.restrict: maxBitrateKbps must be between 0 and 500000")
			}
			patch.MaxBitrateKbps = &v
			changes = append(changes, "maxBitrateKbps")
		}
		if v, present, err := paramInt(params, "streamLimit", 0); err != nil {
			return flows.ActionResult{}, fmt.Errorf("viewer.restrict: %w", err)
		} else if present {
			if v < 0 || v > 100 {
				return flows.ActionResult{}, errors.New("viewer.restrict: streamLimit must be between 0 and 100")
			}
			patch.StreamLimit = &v
			changes = append(changes, "streamLimit")
		}
		if v, present, err := paramBool(params, "disableAccount", false); err != nil {
			return flows.ActionResult{}, fmt.Errorf("viewer.restrict: %w", err)
		} else if present {
			enabled := !v
			patch.AccountEnabled = &enabled
			changes = append(changes, "disableAccount")
		}
		if len(changes) == 0 {
			return flows.ActionResult{}, errors.New("viewer.restrict: specify at least one restriction (remoteDisabled, maxBitrateKbps, streamLimit, or disableAccount)")
		}

		var revertAt time.Time
		if rawRevert := strings.TrimSpace(paramString(params, "revertAfter", "")); rawRevert != "" {
			d, err := parseHumanDuration(rawRevert)
			if err != nil {
				return flows.ActionResult{}, fmt.Errorf("viewer.restrict: invalid revertAfter: %w", err)
			}
			if d > maxRestrictionRevertAfter {
				return flows.ActionResult{}, errors.New("viewer.restrict: revertAfter cannot exceed 30d")
			}
			revertAt = deps.now().UTC().Add(d)
		}
		reason := strings.TrimSpace(paramString(params, "reason", ""))

		revertAtStr := ""
		if !revertAt.IsZero() {
			revertAtStr = revertAt.Format(time.RFC3339)
		}

		if actx.DryRun {
			return flows.ActionResult{
				Output: map[string]any{
					"dryRun":     true,
					"viewerId":   viewerID,
					"restricted": false,
					"revertAt":   revertAtStr,
					"changes":    changes,
				},
			}, nil
		}
		if deps.Viewers == nil {
			return flows.ActionResult{}, errors.New("viewer.restrict: viewer controller unavailable")
		}

		prev, err := deps.Viewers.GetViewerPolicy(ctx, viewerID)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("viewer.restrict: read viewer policy: %w", err)
		}
		if deps.Restrictions != nil {
			if err := deps.Restrictions.SaveIfAbsentOrUpdate(ctx, ViewerRestrictionRecord{
				ViewerID:  viewerID,
				Previous:  prev,
				Applied:   patch,
				Reason:    reason,
				AppliedAt: deps.now().UTC(),
				RevertAt:  revertAt,
			}); err != nil {
				return flows.ActionResult{}, fmt.Errorf("viewer.restrict: persist baseline: %w", err)
			}
		}
		if err := deps.Viewers.ApplyViewerPolicy(ctx, viewerID, patch); err != nil {
			return flows.ActionResult{}, fmt.Errorf("viewer.restrict: apply policy: %w", err)
		}
		return flows.ActionResult{
			Output: map[string]any{
				"dryRun":     false,
				"viewerId":   viewerID,
				"restricted": true,
				"revertAt":   revertAtStr,
				"changes":    changes,
			},
		}, nil
	}}
}

// NewViewerUnrestrict constructs the built-in viewer.unrestrict action handler.
func NewViewerUnrestrict(deps Deps) flows.ActionHandler {
	spec := flows.ActionSpec{
		Type:        "viewer.unrestrict",
		Owner:       events.OwnerBuiltin,
		Group:       "viewers",
		Label:       "Lift viewer restrictions",
		Description: "Restore a viewer's previous settings if restricted by a flow, or clear active playback and account restrictions.",
		Icon:        "shield-check",
		Idempotent:  true,
		SideEffect:  true,
		Params: []flows.ParamSchema{
			{Key: "viewerId", Type: events.FieldString, Label: "Viewer", Description: "Viewer ID to unrestrict.", Required: true, Template: true, Picker: flows.PickerViewer},
			{Key: "resetStreamLimit", Type: events.FieldBool, Label: "Reset stream limit", Description: "Also reset the viewer's stream limit to server default when no saved baseline exists.", Default: false},
		},
		Outputs: []events.FieldSchema{
			{Name: "viewerId", Type: events.FieldString, Label: "Viewer ID"},
			{Name: "unrestricted", Type: events.FieldBool, Label: "Restrictions lifted"},
			{Name: "restoredFromSnapshot", Type: events.FieldBool, Label: "Restored saved baseline"},
			{Name: "dryRun", Type: events.FieldBool, Label: "Dry run"},
		},
	}

	return flows.ActionFunc{ActionSpec: spec, Run: func(ctx context.Context, actx flows.ActionContext, params map[string]any) (flows.ActionResult, error) {
		viewerID := strings.TrimSpace(paramString(params, "viewerId", correlationViewerID(actx)))
		if viewerID == "" {
			return flows.ActionResult{}, errors.New("viewer.unrestrict: viewerId is required")
		}
		resetStreamLimit, _, err := paramBool(params, "resetStreamLimit", false)
		if err != nil {
			return flows.ActionResult{}, fmt.Errorf("viewer.unrestrict: %w", err)
		}

		if actx.DryRun {
			hasSnap := false
			if deps.Restrictions != nil {
				_, hasSnap, _ = deps.Restrictions.Get(ctx, viewerID)
			}
			return flows.ActionResult{
				Output: map[string]any{
					"dryRun":               true,
					"viewerId":             viewerID,
					"unrestricted":         false,
					"restoredFromSnapshot": hasSnap,
				},
			}, nil
		}
		if deps.Viewers == nil {
			return flows.ActionResult{}, errors.New("viewer.unrestrict: viewer controller unavailable")
		}

		var (
			patch                ViewerRestrictionPatch
			restoredFromSnapshot bool
		)
		if deps.Restrictions != nil {
			if rec, ok, err := deps.Restrictions.Get(ctx, viewerID); err != nil {
				return flows.ActionResult{}, fmt.Errorf("viewer.unrestrict: read baseline: %w", err)
			} else if ok {
				patch = snapshotToPatch(rec.Previous)
				restoredFromSnapshot = true
			}
		}
		if !restoredFromSnapshot {
			f := false
			zero := 0
			t := true
			patch = ViewerRestrictionPatch{
				RemoteDisabled: &f,
				MaxBitrateKbps: &zero,
				AccountEnabled: &t,
			}
			if resetStreamLimit {
				patch.StreamLimit = &zero
			}
		}
		if err := deps.Viewers.ApplyViewerPolicy(ctx, viewerID, patch); err != nil {
			return flows.ActionResult{}, fmt.Errorf("viewer.unrestrict: apply policy: %w", err)
		}
		if deps.Restrictions != nil {
			_ = deps.Restrictions.Delete(ctx, viewerID)
		}
		return flows.ActionResult{
			Output: map[string]any{
				"dryRun":               false,
				"unrestricted":         true,
				"viewerId":             viewerID,
				"restoredFromSnapshot": restoredFromSnapshot,
			},
		}, nil
	}}
}
