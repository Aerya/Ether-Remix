package resolver

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/events"
)

func TestResolverEmitsAllEvents(t *testing.T) {
	ctx := context.Background()
	bus, rec := events.NewTestBus(t)
	reg := events.NewBuiltinRegistry()

	idx := &fakeIndexer{releases: movieReleases()}
	health := fakeHealth{unhealthy: map[string]bool{"sd720": true}}
	svc := New(idx, health, nil, testCfg(), Options{Events: bus})

	title := matrixTitle()

	// 1. Successful resolve -> resolver.search.completed + resolver.release.selected.
	res, err := svc.Resolve(ctx, core.ResolveRequest{Title: title, Client: webCaps()})
	if err != nil || res == nil {
		t.Fatalf("Resolve: res=%+v err=%v", res, err)
	}
	evDone := rec.WaitFor(t, events.EventResolverSearchCompleted, time.Second)
	if err := reg.ValidatePayload(evDone.Type, evDone.Payload); err != nil {
		t.Fatalf("search completed invalid: %v", err)
	}
	done, err := events.DecodePayload[events.ResolverSearchCompletedPayload](evDone)
	if err != nil {
		t.Fatal(err)
	}
	if done.WinnerReleaseID != res.Release.ID || done.MediaType != "movie" {
		t.Fatalf("unexpected search completed: %+v", done)
	}

	evSel := rec.WaitFor(t, events.EventResolverReleaseSelected, time.Second)
	if err := reg.ValidatePayload(evSel.Type, evSel.Payload); err != nil {
		t.Fatalf("release selected invalid: %v", err)
	}
	sel, err := events.DecodePayload[events.ResolverReleaseSelectedPayload](evSel)
	if err != nil {
		t.Fatal(err)
	}
	if sel.ReleaseID != res.Release.ID || sel.TitleKey != title.Key.String() {
		t.Fatalf("unexpected release selected: %+v", sel)
	}

	// 2. Empty search -> resolver.search.empty.
	emptySvc := New(&fakeIndexer{}, fakeHealth{}, nil, testCfg(), Options{Events: bus})
	if _, err := emptySvc.Resolve(ctx, core.ResolveRequest{Title: title, Client: webCaps()}); err == nil {
		t.Fatal("expected empty search error")
	}
	evEmpty := rec.WaitFor(t, events.EventResolverSearchEmpty, time.Second)
	if err := reg.ValidatePayload(evEmpty.Type, evEmpty.Payload); err != nil {
		t.Fatalf("search empty invalid: %v", err)
	}

	// 3. Pin created -> resolver.pin.created.
	r720, ok := svc.LookupRelease("sd720")
	if !ok {
		t.Fatal("expected sd720 in recent releases")
	}
	if _, err := svc.Pin(ctx, title.Key, title.Title, r720, "owner choice"); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	evPinCreate := rec.WaitFor(t, events.EventResolverPinCreated, time.Second)
	if err := reg.ValidatePayload(evPinCreate.Type, evPinCreate.Payload); err != nil {
		t.Fatalf("pin created invalid: %v", err)
	}
	pinCreate, err := events.DecodePayload[events.ResolverPinCreatedPayload](evPinCreate)
	if err != nil {
		t.Fatal(err)
	}
	if pinCreate.ReleaseID != "sd720" || pinCreate.Note != "owner choice" {
		t.Fatalf("unexpected pin created: %+v", pinCreate)
	}

	// 4. Pin rejected (excluded) -> resolver.pin.rejected.
	if _, err := svc.Resolve(ctx, core.ResolveRequest{
		Title:   title,
		Client:  core.ClientCaps{Product: "Vela iOS", MaxHeight: 1080},
		Exclude: []string{"sd720"},
	}); err == nil {
		t.Fatal("expected excluded pin to fail")
	}
	evPinRej := rec.WaitFor(t, events.EventResolverPinRejected, time.Second)
	if err := reg.ValidatePayload(evPinRej.Type, evPinRej.Payload); err != nil {
		t.Fatalf("pin rejected invalid: %v", err)
	}
	pinRej, err := events.DecodePayload[events.ResolverPinRejectedPayload](evPinRej)
	if err != nil {
		t.Fatal(err)
	}
	if pinRej.ReleaseID != "sd720" || pinRej.Client != "Vela iOS" {
		t.Fatalf("unexpected pin rejected: %+v", pinRej)
	}

	// 5. Pin unhealthy -> resolver.pin.unhealthy.
	if _, err := svc.Resolve(ctx, core.ResolveRequest{Title: title, Client: webCaps()}); err == nil {
		t.Fatal("expected unhealthy pin to fail")
	}
	evPinBad := rec.WaitFor(t, events.EventResolverPinUnhealthy, time.Second)
	if err := reg.ValidatePayload(evPinBad.Type, evPinBad.Payload); err != nil {
		t.Fatalf("pin unhealthy invalid: %v", err)
	}
	pinBad, err := events.DecodePayload[events.ResolverPinUnhealthyPayload](evPinBad)
	if err != nil {
		t.Fatal(err)
	}
	if pinBad.ReleaseID != "sd720" || pinBad.Status != string(PinUnhealthy) {
		t.Fatalf("unexpected pin unhealthy: %+v", pinBad)
	}

	// 6. Block release -> resolver.release.blocklisted.
	if _, err := svc.Block(ctx, BlockRequest{ReleaseID: res.Release.ID, TitleKey: title.Key, TitleName: title.Title}, "corrupt audio"); err != nil {
		t.Fatalf("Block: %v", err)
	}
	evBlock := rec.WaitFor(t, events.EventResolverReleaseBlocklisted, time.Second)
	if err := reg.ValidatePayload(evBlock.Type, evBlock.Payload); err != nil {
		t.Fatalf("blocklisted invalid: %v", err)
	}
	blocked, err := events.DecodePayload[events.ResolverReleaseBlocklistedPayload](evBlock)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.ReleaseID != res.Release.ID || blocked.Actor != BlockOwner || blocked.Reason != "corrupt audio" {
		t.Fatalf("unexpected blocklisted: %+v", blocked)
	}

	// 7. Pin removed -> resolver.pin.removed.
	if had, err := svc.Unpin(ctx, title.Key); !had || err != nil {
		t.Fatalf("Unpin: had=%v err=%v", had, err)
	}
	evPinRem := rec.WaitFor(t, events.EventResolverPinRemoved, time.Second)
	if err := reg.ValidatePayload(evPinRem.Type, evPinRem.Payload); err != nil {
		t.Fatalf("pin removed invalid: %v", err)
	}
	pinRem, err := events.DecodePayload[events.ResolverPinRemovedPayload](evPinRem)
	if err != nil {
		t.Fatal(err)
	}
	if pinRem.ReleaseID != "sd720" || pinRem.TitleKey != title.Key.String() {
		t.Fatalf("unexpected pin removed: %+v", pinRem)
	}
}
