package livetv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type admissionTranscoder struct{ budgetTranscoder }

func (admissionTranscoder) ProbeLive(context.Context, string) (*core.MediaInfo, error) {
	return &core.MediaInfo{Container: "mpegts", Streams: []core.StreamInfo{{Type: "video", Codec: "h264", Width: 1280, Height: 720}, {Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000}}}, nil
}

// Admission must exhaust failed inputs before returning a typed failure, while
// a working backup remains playable on the same tune and provider reservation.
func TestLiveStartupAdmission(t *testing.T) {
	for _, backupWorks := range []bool{false, true} {
		name := "all_refused"
		if backupWorks {
			name = "backup_plays"
		}
		t.Run(name, func(t *testing.T) {
			var attempted []string
			var attemptMu sync.Mutex
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attemptMu.Lock()
				attempted = append(attempted, r.URL.Path)
				attemptMu.Unlock()
				if backupWorks && r.URL.Path == "/backup" {
					w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
					w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nseg.ts\n"))
					return
				}
				w.WriteHeader(502)
			}))
			defer up.Close()
			var logs diagnosticBuffer
			s := testService(t, Options{Transcoder: admissionTranscoder{}, HTTPClient: up.Client(), Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
			c, err := s.SaveChannel(t.Context(), Channel{Name: "Startup", Number: "1", Kind: "iptv", Enabled: true, StreamSources: []StreamSource{{ID: "primary", URL: up.URL + "/primary", Enabled: true}, {ID: "backup", URL: up.URL + "/backup", Enabled: true, Priority: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			var admitted *steadyAdmission
			var code string
			if backupWorks {
				admitted, code, _, err = s.admitSteady(t.Context(), c)
			} else {
				_, err = s.Tune(t.Context(), &core.User{ID: 1}, c.ID, core.ClientCaps{})
			}
			t.Logf("admission elapsed=%s", time.Since(started))
			// Concurrent admission preserves membership, not request order.
			attemptMu.Lock()
			seen := map[string]bool{}
			for _, path := range attempted {
				seen[path] = true
			}
			attemptMu.Unlock()
			if !seen["/backup"] || !backupWorks && !seen["/primary"] {
				t.Fatalf("inputs attempted: %v", seen)
			}
			if backupWorks {
				if err != nil || code != "" || admitted == nil || admitted.link.src.ID != "backup" {
					t.Fatalf("backup lost: %v %s", err, code)
				}
				admitted.link.close(s)
			} else {
				if strings.Count(logs.text(), `"msg":"live tune failed"`) != 1 || !strings.Contains(logs.text(), `"level":"INFO"`) || len(s.ViewingSessions()) != 0 {
					t.Fatal("failed tune not summarized or released")
				}
				var detail *TuneError
				if !errors.As(err, &detail) || detail.UpstreamStatus != 502 || len(detail.Inputs) != 2 || !strings.Contains(detail.Error(), "HTTP 502") || admitted != nil || code != "" {
					t.Fatalf("failure evidence: %#v %s", err, code)
				}
			}
			s.mu.Lock()
			for _, reservations := range s.broadcastProviders {
				if len(reservations) != 0 {
					t.Error("provider reservation leaked")
				}
			}
			s.mu.Unlock()
		})
	}
}

// Many bad candidates must not shrink a healthy late input's first-picture
// window. Capped accounts stay serial; uncapped ones stay within three probes.
func TestLiveStartupAdmissionManyInputs(t *testing.T) {
	for _, cap := range []int{0, 1} {
		t.Run(fmt.Sprintf("provider_cap_%d", cap), func(t *testing.T) {
			var active, peak atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
				}
				if r.URL.Path != "/healthy" {
					w.WriteHeader(502)
					return
				}
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				_, _ = w.Write([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nseg.ts\n"))
			}))
			defer up.Close()
			tc := delayedAdmissionTranscoder{}
			s := testService(t, Options{Transcoder: tc, HTTPClient: up.Client()})
			if _, err := s.ImportSource(t.Context(), Source{ID: "account", Name: "Account", Playlist: "#EXTM3U\n#EXTINF:-1,Healthy\n" + up.URL + "/healthy\n"}); err != nil {
				t.Fatal(err)
			}
			entry, err := s.sourceEntry(t.Context(), "account")
			if err != nil {
				t.Fatal(err)
			}
			entry.limit = cap
			s.setSourceEntry("account", entry)
			c := Channel{ID: "many", Name: "Many", Number: "1", Kind: "iptv", Enabled: true, SourceID: "account"}
			for n := 0; n < 15; n++ {
				c.StreamSources = append(c.StreamSources, StreamSource{ID: fmt.Sprint(n), AuthSourceID: "account", URL: fmt.Sprintf("%s/bad/%d", up.URL, n), Enabled: true, Priority: n})
			}
			c.StreamSources = append(c.StreamSources, StreamSource{ID: "healthy", AuthSourceID: "account", URL: up.URL + "/healthy", Enabled: true, Priority: 15})
			started := time.Now()
			admitted, code, _, err := s.admitSteady(t.Context(), c)
			elapsed := time.Since(started)
			t.Logf("late healthy first picture: %s", elapsed)
			if err != nil || code != "" || admitted == nil {
				t.Fatalf("admission: %s %v", code, err)
			}
			if admitted.link.src.ID != "healthy" || admitted.link.primedDecoder == nil {
				t.Fatal("healthy decoder/frame was not retained")
			}
			admitted.link.close(s)
			if peak.Load() > 3 || cap == 1 && peak.Load() > 1 {
				t.Fatalf("provider cap exceeded: %d", peak.Load())
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, reservations := range s.broadcastProviders {
				if len(reservations) > 0 {
					t.Fatal("reservation leaked")
				}
			}
		})
	}
}

type delayedAdmissionTranscoder struct{ admissionTranscoder }

func (delayedAdmissionTranscoder) ProbeLive(ctx context.Context, input string) (*core.MediaInfo, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(650 * time.Millisecond):
	}
	return (admissionTranscoder{}).ProbeLive(ctx, input)
}
