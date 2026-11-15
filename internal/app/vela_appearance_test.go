package app

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/vela"
)

// The revision and owner choice must commit together and survive restarts.
func TestVelaAppearanceRevision(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	registry := frontend.NewRegistry(func(frontend.Protocol) frontend.Host { return frontend.Host{} })
	if err := registry.Register(vela.Frontend{}); err != nil {
		t.Fatal(err)
	}
	control := newFrontendControl(registry, cfg)
	for _, tc := range []struct {
		skin     string
		revision uint64
	}{{"cinematic", 0}, {"lounge", 1}, {"lounge", 1}, {"cinematic", 2}} {
		if err := control.PutSettings(context.Background(), "vela", frontend.Settings{"defaultSkinId": tc.skin}); err != nil {
			t.Fatal(err)
		}
		reopened, err := config.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		got := reopened.Read().Frontends["vela"]
		if got.AppearanceRevision != tc.revision || got.Settings["defaultSkinId"] != tc.skin {
			t.Fatalf("persisted: %#v", got)
		}
	}
	if err := control.PutSettings(context.Background(), "vela", frontend.Settings{"defaultSkinId": "../secret"}); err == nil {
		t.Fatal("invalid skin accepted")
	}
}
