package notify

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestViewerPushDeliverLiveEventAlertClaimsIsolationAndOptOut(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	testPushGrants(t, db)

	now := time.Unix(2000, 0).UTC()
	active := map[int64]bool{1: true, 2: true}
	prefs := &ViewerAlerts{
		DB:  db,
		Now: func() time.Time { return now },
		Active: func(_ context.Context, id int64) (*core.User, error) {
			if !active[id] {
				return nil, core.ErrUnauthorized
			}
			return &core.User{ID: id}, nil
		},
	}

	type sentMsg struct {
		endpoint string
		ev       Event
	}
	var sent []sentMsg
	push := &ViewerPush{
		Notifications: New(db, Options{}),
		Preferences:   prefs,
		Now:           func() time.Time { return now },
		Send: func(_ context.Context, sub PushSubscription, ev Event) error {
			sent = append(sent, sentMsg{endpoint: sub.Endpoint, ev: ev})
			return nil
		},
	}

	v1, v2 := &core.User{ID: 1}, &core.User{ID: 2}
	if err := prefs.SetEnabled(ctx, v1, true); err != nil {
		t.Fatal(err)
	}
	if err := prefs.SetEnabled(ctx, v2, false); err != nil {
		t.Fatal(err)
	}
	sub1 := testViewerPushSubscription(t, "https://push.test/v1")
	sub2 := testViewerPushSubscription(t, "https://push.test/v2")
	if err := push.Register(ctx, v1, sub1, testGrant(v1.ID)); err != nil {
		t.Fatal(err)
	}
	if err := push.Register(ctx, v2, sub2, testGrant(v2.ID)); err != nil {
		t.Fatal(err)
	}

	now = now.Add(10 * time.Second)
	alert := core.LiveEventAlert{
		ID:        "started:p1:f1:e1:p1:f1:team:t1",
		Kind:      core.LiveEventAlertStarted,
		EventID:   "p1:f1:e1",
		EntityKey: "p1:f1:team:t1",
		Title:     "North at South",
		Body:      "Live now",
		At:        now.UTC().Format(time.RFC3339),
	}

	// Viewer 1 (enabled) receives the alert once.
	if err := push.DeliverLiveEventAlert(ctx, v1.ID, alert); err != nil {
		t.Fatalf("DeliverLiveEventAlert v1: %v", err)
	}
	if len(sent) != 1 || sent[0].endpoint != sub1.Endpoint || sent[0].ev.Kind != "liveevent.started" || sent[0].ev.Title != "North at South" {
		t.Fatalf("unexpected sent pushes: %+v", sent)
	}

	// Duplicate delivery for same alert ID is deduplicated via claim table.
	if err := push.DeliverLiveEventAlert(ctx, v1.ID, alert); err != nil {
		t.Fatalf("DeliverLiveEventAlert duplicate: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("duplicate alert was sent again: %d pushes", len(sent))
	}

	// Viewer 2 (alerts disabled) receives nothing.
	if err := push.DeliverLiveEventAlert(ctx, v2.ID, alert); err != nil {
		t.Fatalf("DeliverLiveEventAlert v2: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("disabled viewer received push: %+v", sent)
	}

	// Stale alert (> 1 hour old) is ignored.
	stale := alert
	stale.ID = "final:p1:f1:e1:p1:f1:team:t1"
	stale.At = now.Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	if err := push.DeliverLiveEventAlert(ctx, v1.ID, stale); err != nil {
		t.Fatalf("DeliverLiveEventAlert stale: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("stale alert was delivered: %+v", sent)
	}
}
