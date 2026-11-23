package notify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.Register("viewer_push", 3, `CREATE TABLE viewer_push_liveevent_claims (viewer_id INTEGER NOT NULL, alert_key TEXT NOT NULL, endpoint_hash TEXT NOT NULL, claimed_at INTEGER NOT NULL, PRIMARY KEY(viewer_id,alert_key,endpoint_hash));
CREATE INDEX viewer_push_liveevent_claims_at ON viewer_push_liveevent_claims(claimed_at);`)
	store.RegisterViewerTable("viewer_alerts", "viewer_push_liveevent_claims", "viewer_id", store.ViewerMergeMove, "alert_key", "endpoint_hash")
}

// DeliverLiveEventAlert sends an at-most-once browser push notification for a
// live event alert (A7) to every valid, subscribed endpoint belonging to
// viewerID when the viewer has alert notifications enabled.
//
// Like Drain, claims are intentionally at-most-once across cluster nodes,
// stale or pre-subscription alerts are skipped, and viewer status, opt-in, and
// grant validity are rechecked immediately before delivery.
func (p *ViewerPush) DeliverLiveEventAlert(ctx context.Context, viewerID int64, alert core.LiveEventAlert) error {
	if !p.ready() || viewerID <= 0 || strings.TrimSpace(alert.ID) == "" {
		return nil
	}
	now := p.now()
	alertAt := now
	if raw := strings.TrimSpace(alert.At); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			alertAt = parsed
		} else if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			alertAt = parsed
		}
	}
	// Never deliver stale alerts older than an hour.
	if now.Sub(alertAt) > time.Hour {
		return nil
	}

	viewer, err := p.Preferences.Active(ctx, viewerID)
	if err != nil && !errors.Is(err, core.ErrUnauthorized) && !errors.Is(err, core.ErrNotFound) {
		return err
	}
	if err != nil || viewer == nil || viewer.ID != viewerID {
		return p.PurgeViewer(ctx, viewerID)
	}
	enabled, err := p.Preferences.Enabled(ctx, viewer)
	if err != nil || !enabled {
		return err
	}

	rows, err := p.Preferences.DB.QueryContext(ctx,
		`SELECT s.endpoint_hash, s.subscription
		 FROM viewer_push_subscriptions s
		 JOIN viewer_alert_preferences pr ON pr.viewer_id=s.viewer_id
		 JOIN identity_grants g ON g.hash=s.grant_hash AND g.viewer_id=s.viewer_id AND g.surface='viewer-portal' AND g.revoked=0 AND g.expires>?
		 LEFT JOIN viewer_push_liveevent_claims c ON c.viewer_id=s.viewer_id AND c.alert_key=? AND c.endpoint_hash=s.endpoint_hash
		 WHERE s.viewer_id=? AND c.viewer_id IS NULL AND pr.enabled=1 AND s.created_at<=?`,
		now.UnixMilli(), alert.ID, viewerID, alertAt.Unix(),
	)
	if err != nil {
		return err
	}
	type dest struct {
		hash, sealed string
	}
	var dests []dest
	for rows.Next() {
		var d dest
		if err = rows.Scan(&d.hash, &d.sealed); err != nil {
			rows.Close()
			return err
		}
		dests = append(dests, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	for _, d := range dests {
		plain, _, err := config.OpenWith(p.Notifications.opts.Sealer, d.sealed, viewerPushContext(viewerID, d.hash)...)
		if err != nil {
			return err
		}
		var subscription PushSubscription
		if json.Unmarshal([]byte(plain), &subscription) != nil || validatePush(&subscription) != nil || pushHash(subscription.Endpoint) != d.hash {
			continue
		}
		res, err := p.Preferences.DB.ExecContext(ctx,
			`INSERT INTO viewer_push_liveevent_claims(viewer_id,alert_key,endpoint_hash,claimed_at) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`,
			viewerID, alert.ID, d.hash, now.Unix(),
		)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}

		// Revalidate active viewer, opt-in preference, and live grant after claim.
		viewer, err = p.Preferences.Active(ctx, viewerID)
		if err != nil && !errors.Is(err, core.ErrUnauthorized) && !errors.Is(err, core.ErrNotFound) {
			return err
		}
		if err != nil || viewer == nil || viewer.ID != viewerID {
			if e := p.PurgeViewer(ctx, viewerID); e != nil {
				return e
			}
			return nil
		}
		enabled, err = p.Preferences.Enabled(ctx, viewer)
		if err != nil || !enabled {
			return err
		}
		var current bool
		err = p.Preferences.DB.QueryRowContext(ctx,
			`SELECT EXISTS(
				SELECT 1 FROM viewer_push_subscriptions s
				JOIN viewer_alert_preferences pr ON pr.viewer_id=s.viewer_id
				JOIN identity_grants g ON g.hash=s.grant_hash AND g.viewer_id=s.viewer_id AND g.surface='viewer-portal' AND g.revoked=0 AND g.expires>?
				WHERE s.viewer_id=? AND s.endpoint_hash=? AND s.subscription=? AND pr.enabled=1 AND s.created_at<=?
			)`,
			now.UnixMilli(), viewerID, d.hash, d.sealed, alertAt.Unix(),
		).Scan(&current)
		if err != nil {
			return err
		}
		if !current {
			continue
		}

		kind := "liveevent.alert"
		if alert.Kind != "" {
			kind = "liveevent." + alert.Kind
		}
		ev := Event{
			Title:   alert.Title,
			Message: alert.Body,
			Kind:    kind,
			At:      alertAt,
		}
		if p.Send != nil {
			_ = p.Send(ctx, subscription, ev)
		} else {
			_ = p.send(ctx, subscription, ev)
		}
	}

	cutoff := now.Add(-48 * time.Hour).Unix()
	var hasExpired bool
	if err = p.Preferences.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM viewer_push_liveevent_claims WHERE claimed_at<?)`, cutoff).Scan(&hasExpired); err != nil {
		return err
	}
	if hasExpired {
		_, err = p.Preferences.DB.ExecContext(ctx, `DELETE FROM viewer_push_liveevent_claims WHERE claimed_at<?`, cutoff)
		return err
	}
	return nil
}
