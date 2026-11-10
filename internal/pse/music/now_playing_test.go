package music

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestMusicNowPlayingSubsonicDoesNotCreateHistory(t *testing.T) {
	h := newHarness(t, "")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	h.srv.clock = func() time.Time { return now }
	okResp(t, h.call("alice", "scrobble", "id", "tr-100", "submission", "false"))
	rows := h.srv.NowPlaying()
	if len(rows) != 1 || rows[0].Title != "One More Time" || rows[0].Username != "alice" || rows[0].Client.Protocol != "subsonic" || rows[0].ViewOffset != nil || rows[0].CanStop {
		t.Fatalf("session: %+v", rows)
	}
	page, err := h.srv.o.Library.History(context.Background(), now.Add(-24*time.Hour), now.Add(24*time.Hour), 0, 0)
	if err != nil || page.Total != 0 {
		t.Fatalf("le début crée une écoute: %+v %v", page, err)
	}
	now = now.Add(232 * time.Second)
	if len(h.srv.NowPlaying()) != 0 {
		t.Fatal("session fantôme")
	}
	okResp(t, h.call("alice", "scrobble", "id", "tr-100", "submission", "true"))
	var n int
	_ = h.srv.o.Library.db.QueryRow("SELECT COUNT(*) FROM pse_music_plays").Scan(&n)
	if n != 1 {
		t.Fatalf("historique: %d", n)
	}
	if len(h.srv.NowPlaying()) != 0 {
		t.Fatal("une soumission différée crée une session")
	}
}

func TestMusicNowPlayingDeviceIsolationPauseStopAndExpiry(t *testing.T) {
	h := newHarness(t, "")
	now := time.Now()
	h.srv.clock = func() time.Time { return now }
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Plex-Client-Identifier", "phone")
	r.Header.Set("X-Plex-Product", "Plexamp")
	ms := int64(45000)
	h.srv.observeMusic(r, 7, "plex", 100, "playing", &ms, "")
	h.srv.observeMusic(r, 8, "plex", 100, "playing", &ms, "")
	r2 := r.Clone(context.Background())
	r2.Header = r.Header.Clone()
	r2.Header.Set("X-Plex-Client-Identifier", "desktop")
	h.srv.observeMusic(r2, 7, "plex", 101, "playing", &ms, "")
	if len(h.srv.NowPlaying()) != 3 {
		t.Fatal("appareils ou spectateurs confondus")
	}
	h.srv.observeMusic(r, 7, "plex", 101, "paused", &ms, "")
	h.srv.observeMusic(r, 7, "plex", 100, "stopped", nil, "")
	rows := h.srv.NowPlaying()
	if len(rows) != 3 {
		t.Fatal("arrêt tardif ferme le nouveau morceau")
	}
	found := false
	for _, row := range rows {
		if row.ViewerID == 7 && row.Client.Device == "" && row.track == 101 && row.ViewerState == "paused" {
			found = true
			if *row.ViewOffset != 45000 {
				t.Fatal("position")
			}
		}
	}
	if !found {
		t.Fatal("pause absente")
	}
	h.srv.observeMusic(r, 7, "plex", 101, "stopped", nil, "")
	if len(h.srv.NowPlaying()) != 2 {
		t.Fatal("arrêt ignoré")
	}
	now = now.Add(121 * time.Second)
	if len(h.srv.NowPlaying()) != 0 {
		t.Fatal("expiration ignorée")
	}
}

func TestMusicNowPlayingJellyfinAndEmbyReports(t *testing.T) {
	for _, protocol := range []string{"jellyfin", "emby"} {
		t.Run(protocol, func(t *testing.T) {
			h := newHarness(t, "")
			report := func(path string, paused bool) {
				body, _ := json.Marshal(map[string]any{"ItemId": JellyfinGUID(guidTrack, 100), "PositionTicks": 120000000, "IsPaused": paused})
				r := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
				r.Header.Set("Authorization", `MediaBrowser Token="NEVER-EXPORT", DeviceId="phone", Client="Music client", Device="Phone"`)
				w := httptest.NewRecorder()
				if !h.srv.ServeJellyfin(w, r, 7, frontend.PSEMusicJellyfin{Protocol: protocol}) || w.Code != 204 {
					t.Fatalf("report: %d", w.Code)
				}
			}
			report("/Sessions/Playing", false)
			report("/Sessions/Playing/Progress", true)
			rows := h.srv.NowPlaying()
			if len(rows) != 1 || rows[0].ViewerState != "paused" || rows[0].Client.Protocol != protocol || rows[0].Client.Device != "Phone" || *rows[0].ViewOffset != 12000 {
				t.Fatalf("session: %+v", rows)
			}
			raw, _ := json.Marshal(rows)
			if strings.Contains(string(raw), "NEVER-EXPORT") {
				t.Fatal("jeton exposé")
			}
			report("/Sessions/Playing/Stopped", false)
			if len(h.srv.NowPlaying()) != 0 {
				t.Fatal("arrêt ignoré")
			}
		})
	}
}

func TestMusicNowPlayingPlexTimeline(t *testing.T) {
	h := newHarness(t, "")
	for _, state := range []string{"playing", "paused", "stopped"} {
		r := httptest.NewRequest("GET", "/:/timeline?ratingKey="+psemKey(psemTrack, 100)+"&state="+state+"&time=12000&X-Plex-Client-Identifier=phone", nil)
		w := httptest.NewRecorder()
		if !h.srv.ServePlex(w, r, 7, frontend.PSEMusicPlex{Write: func(w http.ResponseWriter, r *http.Request, v any) { w.WriteHeader(200) }}) {
			t.Fatal("timeline non traitée")
		}
		rows := h.srv.NowPlaying()
		if state == "stopped" {
			if len(rows) != 0 {
				t.Fatal("arrêt")
			}
		} else if len(rows) != 1 || rows[0].ViewerState != state {
			t.Fatalf("session: %+v", rows)
		}
	}
}

type delayedActivitySource struct {
	Source
	entered chan struct{}
	release chan struct{}
}

func (s delayedActivitySource) Track(_ context.Context, id int64) (Track, error) {
	if id == 100 {
		close(s.entered)
		<-s.release
	}
	return Track{ID: id, Title: "Track", Duration: 200}, nil
}

func TestMusicNowPlayingSlowMetadataCannotResurrectStoppedOrOldTrack(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			h := newHarness(t, "")
			source := delayedActivitySource{entered: make(chan struct{}), release: make(chan struct{})}
			h.srv.o.Source = source
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("X-Plex-Client-Identifier", "phone")
			done := make(chan struct{})
			go func() { defer close(done); h.srv.observeMusic(r, 7, "plex", 100, "playing", nil, "") }()
			<-source.entered
			if replace {
				h.srv.observeMusic(r, 7, "plex", 101, "playing", nil, "")
			} else {
				h.srv.observeMusic(r, 7, "plex", 100, "stopped", nil, "")
			}
			close(source.release)
			<-done
			rows := h.srv.NowPlaying()
			if replace {
				if len(rows) != 1 || rows[0].track != 101 {
					t.Fatalf("ancien morceau revenu: %+v", rows)
				}
			} else if len(rows) != 0 {
				t.Fatal("lecture arrêtée réapparue")
			}
		})
	}
}
