package usenet

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
)

func TestUsenetPoolEmitsProviderEvents(t *testing.T) {
	bus, rec := events.NewTestBus(t)
	reg := events.NewBuiltinRegistry()

	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	o := &Options{
		BackoffMin:    2 * time.Second,
		BackoffMax:    time.Minute,
		FailThreshold: 2,
		AuthRetryMin:  10 * time.Second,
		AuthRetryMax:  time.Minute,
		Events:        bus,
	}
	o.withDefaults()

	p := newPool("usenet-1", config.UsenetProvider{
		ID:          "usenet-1",
		Name:        "Newshosting",
		Host:        "news.example.com",
		Port:        563,
		Connections: 2,
		Enabled:     true,
	}, o, o.Logger, 0)
	defer p.close()
	p.brk.now = clock

	// 1. Trigger backoff outage -> provider.service.outage.
	p.brk.commandFailure(errors.New("502 unavailable"))
	p.brk.commandFailure(errors.New("502 unavailable"))
	evOutage := rec.WaitFor(t, events.EventProviderServiceOutage, time.Second)
	if err := reg.ValidatePayload(evOutage.Type, evOutage.Payload); err != nil {
		t.Fatalf("outage payload invalid: %v", err)
	}
	outage, err := events.DecodePayload[events.ProviderServiceOutagePayload](evOutage)
	if err != nil {
		t.Fatal(err)
	}
	if outage.ProviderID != "usenet-1" || outage.Kind != "usenet" || outage.BackoffSec < 1 {
		t.Fatalf("unexpected outage payload: %+v", outage)
	}

	// 2. Recover from outage -> provider.service.recovered.
	now = now.Add(5 * time.Second)
	p.brk.success()
	evRec := rec.WaitFor(t, events.EventProviderServiceRecovered, time.Second)
	if err := reg.ValidatePayload(evRec.Type, evRec.Payload); err != nil {
		t.Fatalf("recovered payload invalid: %v", err)
	}
	recovered, err := events.DecodePayload[events.ProviderServiceRecoveredPayload](evRec)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.ProviderID != "usenet-1" || recovered.OutageDurationMS < 5000 {
		t.Fatalf("unexpected recovered payload: %+v", recovered)
	}

	// 3. Auth rejected -> provider.auth.rejected.
	p.brk.authFailure(&ProtocolError{Cmd: "AUTHINFO", Code: 481, Msg: "authentication failed"})
	evAuth := rec.WaitFor(t, events.EventProviderAuthRejected, time.Second)
	if err := reg.ValidatePayload(evAuth.Type, evAuth.Payload); err != nil {
		t.Fatalf("auth rejected payload invalid: %v", err)
	}
	authEv, err := events.DecodePayload[events.ProviderAuthRejectedPayload](evAuth)
	if err != nil {
		t.Fatal(err)
	}
	if authEv.ProviderID != "usenet-1" || authEv.StatusCode != 481 {
		t.Fatalf("unexpected auth rejected payload: %+v", authEv)
	}

	// 4. Connection saturation -> provider.connections.saturated.
	ctx := context.Background()
	if err := p.sem.Acquire(ctx, core.PriorityLive); err != nil {
		t.Fatal(err)
	}
	if err := p.sem.Acquire(ctx, core.PriorityLive); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithCancel(ctx)
	waitDone := make(chan error, 1)
	go func() {
		waitDone <- p.sem.Acquire(waitCtx, core.PriorityLive)
	}()

	evSat := rec.WaitFor(t, events.EventProviderConnectionsSaturated, time.Second)
	cancel()
	<-waitDone
	p.sem.Release(core.PriorityLive)
	p.sem.Release(core.PriorityLive)

	if err := reg.ValidatePayload(evSat.Type, evSat.Payload); err != nil {
		t.Fatalf("saturated payload invalid: %v", err)
	}
	sat, err := events.DecodePayload[events.ProviderConnectionsSaturatedPayload](evSat)
	if err != nil {
		t.Fatal(err)
	}
	if sat.ProviderID != "usenet-1" || sat.ActiveConnections != 2 || sat.MaxConnections != 2 || sat.WaitingReaders < 1 {
		t.Fatalf("unexpected saturated payload: %+v", sat)
	}
}
