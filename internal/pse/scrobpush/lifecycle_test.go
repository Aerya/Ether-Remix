package scrobpush

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
	"testing"
	"time"
)

func TestLifecycleStopKeepsIdentityAndNeverRestartsImportedSessions(t *testing.T) {
	p := &Pusher{}
	key := core.MovieKey(101)
	origins := []state.Origin{{Surface: "plex", SessionID: "plex-play", ClientID: "tv"}, {Surface: "jellyfin", SessionID: "jellyfin-play", ClientID: "phone"}, {Surface: "jellyfin", SessionID: "moonfin-play", ClientID: "moonfin"}}
	for i, origin := range origins {
		viewer := int64(i + 1)
		ev := state.Event{Kind: state.EventProgress, UserID: viewer, Key: key, Origin: origin, State: "playing", Offset: time.Minute, Duration: time.Hour}
		p.Handle(ev)
		start := <-p.queue
		sid := start.payload["Session"].(map[string]any)["Id"]
		ev.State = "paused"
		ev.Offset = 2 * time.Minute
		p.Handle(ev)
		pause := <-p.queue
		if pause.payload["Session"].(map[string]any)["Id"] != sid {
			t.Fatal("pause changed session")
		}
		p.Handle(state.Event{Kind: state.EventScrobble, UserID: viewer, Key: key, Origin: origin})
		<-p.queue
		p.Stop(viewer, key, origin)
		stop := <-p.queue
		session := stop.payload["Session"].(map[string]any)
		if stop.payload["NotificationType"] != "PlaybackStop" || session["Id"] != sid || session["PlayState"].(map[string]any)["PositionTicks"] != int64(2*time.Minute/100) {
			t.Fatalf("stop=%+v", stop)
		}
		p.Stop(viewer, key, origin)
		ev.State = "stopped"
		p.Handle(ev)
		ev.State = "playing"
		p.Handle(ev)
		ev.Imported = true
		p.Handle(ev)
		if len(p.queue) != 0 {
			t.Fatal("duplicate stop or resurrected session")
		}
	}
}
func TestSameViewerConcurrentSessionsAreIndependent(t *testing.T) {
	p := &Pusher{}
	key := core.MovieKey(101)
	a := state.Origin{Surface: "jellyfin", SessionID: "a"}
	b := state.Origin{Surface: "jellyfin", SessionID: "b"}
	for _, o := range []state.Origin{a, b} {
		p.Handle(state.Event{Kind: state.EventProgress, UserID: 1, Key: key, Origin: o, State: "playing"})
	}
	first, second := <-p.queue, <-p.queue
	if first.payload["Session"].(map[string]any)["Id"] == second.payload["Session"].(map[string]any)["Id"] {
		t.Fatal("session IDs collide")
	}
	p.Stop(1, key, a)
	<-p.queue
	p.Handle(state.Event{Kind: state.EventProgress, UserID: 1, Key: key, Origin: b, State: "paused"})
	if event := <-p.queue; event.payload["NotificationType"] != "PlaybackProgress" {
		t.Fatal("other session was removed")
	}
}
