package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
	"github.com/braedonsaunders/ether/internal/frontends/plex"
	"github.com/braedonsaunders/ether/internal/resolver"
)

func TestRX2ReleasedBaseline(t *testing.T) {
	root := os.Getenv("ETHER_RX2_DIR")
	var fixtures []rx2Fixture
	rx2Read(t, os.Getenv("ETHER_RX2_CASES"), &fixtures)
	defaults := config.Defaults(t.TempDir())
	for i, fixture := range fixtures {
		var document struct {
			Quality   map[string]json.RawMessage
			Transcode json.RawMessage
		}
		if err := json.Unmarshal(fixture.Config, &document); err != nil {
			t.Fatal(err)
		}
		var quality map[string]json.RawMessage
		if err := json.Unmarshal(rx2JSON(t, defaults.Quality), &quality); err != nil {
			t.Fatal(err)
		}
		for k, v := range document.Quality {
			quality[k] = v
		}
		fixtures[i].Config = rx2JSON(t, map[string]any{"setupComplete": true, "quality": quality, "transcode": document.Transcode})
	}
	rx2Write(t, filepath.Join(root, "fixtures.json"), rx2JSON(t, fixtures))
	if prod := os.Getenv("ETHER_RX2_PRODUCTION"); prod != "" {
		var fixture rx2Fixture
		rx2Read(t, prod, &fixture)
		fixtures = append(fixtures, fixture)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			a, _ := rx2Boot(t, fixture)
			defer a.Close()
			got := rx2Matrix(t, a, fixture)
			rx2Write(t, filepath.Join(root, fixture.Name+".json"), rx2JSON(t, got))
		})
	}
}

func rx2Resolve(t *testing.T, a *App, v rx2Viewer, device rx2Device, title *core.Title, remote bool) rx2Choice {
	t.Helper()
	ctx := context.Background()
	cfg := a.cfg.Read()
	policy := a.safety.ForUser(&core.User{ID: v.ID})
	u := &core.User{ID: v.ID}
	pr := &plex.Request{Product: device.Product, Platform: device.Platform, Device: device.Device, DeviceName: device.Device, Remote: remote, User: u}
	declared := pr.ClientCapsWith(cfg.Transcode.RemoteMaxBitrateKbps, cfg.Clients.Overrides)
	if frontendHostPolicy(device.Protocol).ProfileQualityEvaluation {
		info := caps.DeviceInfo{Protocol: device.Protocol, Product: device.Product, Platform: device.Platform, Device: device.Device, Remote: remote}
		neutral, err := (caps.Builder{}).Build(ctx, info, caps.DeviceProfile{}, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		declared = playbackFloor(neutral, cfg.Clients.Overrides)
	} else if device.Product == "" {
		declared = core.ClientCaps{Remote: remote, HDR10: true, DolbyVision: true}
	}
	// The legacy playback path clamps the declared bitrate with viewer safety.
	if remote && cfg.Transcode.RemoteMaxBitrateKbps > 0 && (declared.MaxBitrateKbps == 0 || cfg.Transcode.RemoteMaxBitrateKbps < declared.MaxBitrateKbps) {
		declared.MaxBitrateKbps = cfg.Transcode.RemoteMaxBitrateKbps
		declared.DefaultRemoteBitrate = false
	}
	if policy.MaxBitrateKbps > 0 && (declared.MaxBitrateKbps == 0 || policy.MaxBitrateKbps < declared.MaxBitrateKbps) {
		declared.MaxBitrateKbps = policy.MaxBitrateKbps
		declared.DefaultRemoteBitrate = false
	}
	lib := newLibraryProfiles(a).QualityProfileForTitle(ctx, title)
	profile, applied := resolver.RX2LegacyDecision(cfg.Quality, declared, u, title, lib)
	return rx2Choice{Profile: rx2Profile(t, profile, title), Bitrate: applied.MaxBitrateKbps, Stereo: applied.ConvertSurroundAudio, Blocked: remote && policy.RemoteDisabled}
}
