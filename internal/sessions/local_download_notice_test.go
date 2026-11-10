package sessions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// A play waiting for the owner's torrent client shows a message on the
// Jellyfin or Emby screen, once per short while; Plex has its own message.
func TestLocalDownloadNoticeReachesDevice(t *testing.T) {
	viewer := &core.Viewer{ID: 7}
	bus := NewEventBus(nil, func(context.Context, int64) (*core.Viewer, error) { return viewer, nil })
	b := &Broker{deps: BrokerDeps{Events: bus}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dev := core.Device{ID: "tv-1"}
	events, err := bus.Subscribe(ctx, frontend.EventScope{Viewer: viewer, Protocol: "jellyfin", Device: dev})
	if err != nil {
		t.Fatal(err)
	}
	waiting := fmt.Errorf("release: %w", &core.LocalDownloadError{Progress: 0.5, Lang: "fr"})
	b.localDownloadNotice(ctx, viewer, "jellyfin", dev, waiting)
	b.localDownloadNotice(ctx, viewer, "jellyfin", dev, waiting) // répété : ignoré
	b.localDownloadNotice(ctx, viewer, "plex", core.Device{ID: "plex-1"}, waiting)
	b.localDownloadNotice(ctx, viewer, "jellyfin", core.Device{ID: "tv-2"}, errors.New("autre"))
	select {
	case ev := <-events:
		if ev.Type != "SessionCommand" || ev.Command == nil || ev.Command.Kind != frontend.CommandMessage ||
			ev.TargetDeviceID != "tv-1" || ev.ActorViewerID != 7 || !strings.Contains(ev.Command.Message, "50 %") {
			t.Fatalf("événement %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("aucun message")
	}
	select {
	case ev := <-events:
		t.Fatalf("message en trop %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}
