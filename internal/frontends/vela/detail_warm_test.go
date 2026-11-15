package vela

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

type embeddedWarmSessions = frontendtest.Sessions

type detailWarmSessions struct {
	embeddedWarmSessions
	started chan frontend.PlanRequest
	release chan struct{}
}

func (s detailWarmSessions) WarmDetail(ctx context.Context, req frontend.PlanRequest) bool {
	s.started <- req
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	return true
}

// Negotiation, detail and Play must share a declaration, while a blocked
// warmer cannot delay detail or create unbounded duplicate workers.
func TestVelaDetailWarmContract(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	broker := detailWarmSessions{started: make(chan frontend.PlanRequest, 8), release: make(chan struct{})}
	defer close(broker.release)
	built := make(chan frontend.CapabilityRequest, 8)
	i := &instance{ctx: ctx, host: frontend.Host{Sessions: broker, Capabilities: frontendtest.Capabilities{BuildFunc: func(_ context.Context, req frontend.CapabilityRequest) (frontend.CapabilityResult, error) {
		built <- req
		return frontend.CapabilityResult{Client: core.ClientCaps{ClientID: req.Device.ClientID}}, nil
	}}}}
	r := httptest.NewRequest("GET", "/vela/v0/titles/movie", nil)
	r.Header.Set("X-Vela-Device-Id", "tv")
	c := client{Device: device{ID: "tv", Platform: "tvOS"}, MSE: true, VideoCodecs: []string{"hevc", "h264"}, AudioCodecs: []string{"aac"}, Containers: []string{"mp4"}, SubtitleFormats: []string{"webvtt"}, MaxHeight: 2160}
	i.rememberNegotiatedClient(r, c)
	v := &frontend.Viewer{ID: 1}
	d := frontend.ItemDetail{Title: core.Title{Key: core.MovieKey(42)}}
	i.warmDetail(r, v, d)
	select {
	case req := <-broker.started:
		if !req.Intent.DeclaredCaps || req.Item != d.Title.Key || req.Protocol != "vela" {
			t.Fatalf("warm request lost declaration: %+v", req)
		}
	case <-time.After(time.Second):
		t.Fatal("detail did not schedule")
	}
	warmCaps := <-built
	if _, err := i.clientCapabilities(r, v, c); err != nil {
		t.Fatal(err)
	}
	if playCaps := <-built; !reflect.DeepEqual(warmCaps, playCaps) {
		t.Fatalf("detail and Play caps differ: %+v / %+v", warmCaps, playCaps)
	}
	i.warmDetail(r, v, d) // Same device still preparing: no extra goroutine.
	d.Title.Key = core.ShowKey(42)
	i.warmDetail(r, v, d)
	select {
	case <-broker.started:
		t.Fatal("duplicate detail or show scheduled extra work")
	default:
	}
	i.warmMu.Lock()
	defer i.warmMu.Unlock()
	if len(i.warmPending) != 1 {
		t.Fatalf("pending admission = %d", len(i.warmPending))
	}
}
