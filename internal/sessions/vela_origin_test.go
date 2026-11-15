package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontends/vela"
)

type velaOriginSession struct {
	fakeMediaSession
	origin time.Duration
}

func (s *velaOriginSession) TimelineOrigin() time.Duration { return s.origin }

type velaOriginProducer struct {
	fakeBrokerTranscoder
	mu     sync.Mutex
	output *velaOriginSession
	copy   bool
}

func (p *velaOriginProducer) StartHLS(_ context.Context, req core.HLSRequest) (core.HLSSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	origin := req.StartOffset
	if p.copy {
		origin -= 9950 * time.Millisecond
	}
	p.output = &velaOriginSession{fakeMediaSession: fakeMediaSession{id: req.SessionID}, origin: origin}
	return p.output, nil
}
func (p *velaOriginProducer) Session(id string) (core.HLSSession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.output == nil || p.output.id != id {
		return nil, false
	}
	return p.output, true
}

type velaOriginIdentity struct{ frontend.SurfaceIdentity }

func (velaOriginIdentity) ResolveDevice(context.Context, string, frontend.Device) (*core.Viewer, time.Time, error) {
	return &core.Viewer{ID: 1}, time.Now().Add(time.Hour), nil
}
func (velaOriginIdentity) GrantScope(context.Context, string, frontend.Device) (string, error) {
	return "family", nil
}
func (velaOriginIdentity) GrantScopeCurrent(context.Context, string, frontend.Device) (*core.Viewer, error) {
	return &core.Viewer{ID: 1}, nil
}

// Exercise the real broker output lookup through the mounted Vela protocol.
// The public encoder ID intentionally differs from the playback source ID.
func TestVelaCopySeekPublishesMeasuredOrigin(t *testing.T) {
	for _, copyVideo := range []bool{true, false} {
		name := "copy"
		if !copyVideo {
			name = "encode"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			if _, err := db.Exec(`CREATE TABLE vela_replays(key TEXT PRIMARY KEY,digest TEXT NOT NULL,response BLOB,expires INTEGER NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			producer := &velaOriginProducer{copy: copyVideo}
			b := &Broker{deps: BrokerDeps{Transcoder: producer}, bindings: NewBindingTable[frontend.PlayIntent]()}
			b.bindings.Put(&Binding[frontend.PlayIntent]{ID: "public-session", PlayID: "source-session", Started: true})
			decision := core.Decision{Kind: core.DecisionRemux, VideoCopy: copyVideo, SubtitleStream: -1}
			if !copyVideo {
				decision.Kind = core.DecisionTranscode
			}
			surface := frontendtest.Sessions{
				PlanFunc: func(context.Context, frontend.PlanRequest) (frontend.PlayPlan, error) {
					return frontend.PlayPlan{ID: "plan", HLS: true, Decision: decision}, nil
				},
				OpenFunc: func(context.Context, frontend.OpenRequest) (frontend.Session, error) {
					return frontend.Session{ID: "public-session"}, nil
				},
				StartStreamFunc: func(ctx context.Context, _ frontend.SessionAccess, id string, req frontend.StreamRequest) (string, error) {
					if req.Format != frontend.StreamHLS || !req.Continuous {
						t.Fatal("not continuous HLS", req)
					}
					_, err := producer.StartHLS(ctx, core.HLSRequest{SessionID: id, StartOffset: req.Position})
					return "/sessions/media/" + id + "/grant/master.m3u8", err
				},
				TranscodeForFunc: b.TranscodeFor,
			}
			inst, err := (vela.Frontend{DB: db, ReplayKey: make([]byte, 32)}).Mount(ctx, frontend.Host{Identity: velaOriginIdentity{}, Experience: frontendtest.Experience{}, Capabilities: frontendtest.Capabilities{}, Sessions: surface})
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Close(ctx)
			call := func(path, body string) map[string]any {
				t.Helper()
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer token")
				r.Header.Set("X-Vela-Device-Id", "device")
				w := httptest.NewRecorder()
				inst.Handler().ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
				}
				var plan map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
					t.Fatal(err)
				}
				return plan
			}
			check := func(plan map[string]any, position, revision float64) {
				t.Helper()
				origin := position
				if copyVideo {
					origin -= 9.95
				}
				path := plan["paths"].([]any)[0].(map[string]any)
				if path["timelineOriginSeconds"] != origin || plan["positionSeconds"] != position || plan["revision"] != revision {
					t.Fatalf("expected position %v origin %v revision %v, got %+v", position, origin, revision, plan)
				}
			}
			plan := call("/vela/v0/playback", `{"idempotencyKey":"start","titleId":"`+ids.RatingKeyGUID(core.MovieKey(1))+`","positionSeconds":30,"client":{"device":{"id":"device"},"hls":true}}`)
			check(plan, 30, 1)
			plan = call("/vela/v0/sessions/public-session/commands", `{"idempotencyKey":"seek","kind":"seek","positionSeconds":60,"sourceGeneration":1,"seekGeneration":1}`)
			check(plan, 60, 2)
			if plan["seekGeneration"] != float64(2) {
				t.Fatal("seek generation did not advance", plan)
			}
			if hs, ok := b.TranscodeFor(ctx, "source-session"); !ok || hs == nil {
				t.Fatal("source ID lookup lost")
			}
		})
	}
}
