package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/pse/music"
	"github.com/braedonsaunders/ether/internal/store"
)

type activityMusicSource struct{ music.Source }

func (activityMusicSource) Track(context.Context, int64) (music.Track, error) {
	return music.Track{ID: 1, Title: "Activity track", Duration: 180}, nil
}

func TestActivitySessionsIncludesReportedMusic(t *testing.T) {
	a := &App{}
	t.Cleanup(func() { pseRuntimes.Delete(a) })
	srv := music.NewServer(music.Options{Source: activityMusicSource{}, Library: music.NewLibrary(store.OpenTest(t)), Auth: func(context.Context, string, string) (int64, error) { return 7, nil }})
	pseRuntimeOf(a).music.Store(srv)
	r := httptest.NewRequest("GET", "/rest/scrobble?u=alice&p=secret&v=1.16.1&c=test&f=json&id=tr-1&submission=false", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	data, err := json.Marshal(a.activitySessions(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"title":"Activity track"`) || !strings.Contains(string(data), `"isMusic":true`) || !strings.Contains(string(data), `"username":"alice"`) {
		t.Fatalf("sessions: %s; réponse: %s", data, w.Body.String())
	}
	if strings.Contains(string(data), "secret") {
		t.Fatal("secret exposé")
	}
	pseRuntimeOf(a).music.Store(nil)
	data, _ = json.Marshal(a.activitySessions(context.Background()))
	if string(data) != "[]" {
		t.Fatal(string(data))
	}
}
