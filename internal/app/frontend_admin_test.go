package app

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"strings"
	"testing"
)

func TestFrontendControl(t *testing.T) {
	ctx := context.Background()
	c, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	closed := 0
	r := frontend.NewRegistry(func(frontend.Protocol) frontend.Host { return frontend.Host{} })
	if err := r.Register(listenerFrontend{"127.0.0.1:0", &closed}); err != nil {
		t.Fatal(err)
	}
	f := newFrontendControl(r, c)
	c.Subscribe(func(c config.Config) { f.Reconcile(ctx, c) })
	settings := frontend.Settings{"port": float64(8090), "discovery": true, "serverName": "Ether"}
	if err := f.PutSettings(ctx, "test", settings); err != nil {
		t.Fatal(err)
	}
	settings["discovery"] = false
	got, err := f.Settings(ctx, "test")
	if err != nil || got["discovery"] != true || got["serverName"] != nil {
		t.Fatal(got, err)
	}
	if err := f.PutSettings(ctx, "test", frontend.Settings{"port": float64(99999)}); err == nil {
		t.Fatal("invalid port accepted")
	}
	if err := f.Toggle(ctx, "test", true); err != nil {
		t.Fatal(err)
	}
	list, err := f.List(ctx)
	if err != nil || len(list) != 1 || !list[0].Enabled || len(list[0].Status.Addresses) != 1 {
		t.Fatal(list, err)
	}
	if err := f.PutSettings(ctx, "test", frontend.Settings{"port": float64(8091), "discovery": true, "serverName": "Ether"}); err != nil || closed != 1 {
		t.Fatal(err, closed)
	}
	if err := f.PutSettings(ctx, "test", frontend.Settings{"port": float64(8091), "discovery": true, "serverName": "Renamed"}); err != nil || closed != 1 {
		t.Fatal(err, closed)
	}
	if err := f.Toggle(ctx, "test", false); err != nil || closed != 2 {
		t.Fatal(err, closed)
	}
	if err := f.Toggle(ctx, "missing", true); err == nil {
		t.Fatal("unknown frontend accepted")
	}
}

func TestFrontendPortIsNodeLocal(t *testing.T) {
	c := config.Config{Frontends: map[string]config.Frontend{"silo": {Enabled: true}}, FrontendPorts: map[string]int{"silo": 8090}}
	shared := string(config.SharedJSON(c))
	local := string(config.LocalJSON(c))
	if strings.Contains(shared, "frontendPorts") || !strings.Contains(shared, `"frontends"`) || !strings.Contains(local, `"frontendPorts"`) || strings.Contains(local, `"frontends"`) {
		t.Fatal(shared, local)
	}
}

type customSettingsFrontend struct{ listenerFrontend }

func (customSettingsFrontend) ValidateSettings(s frontend.Settings) error {
	if v, ok := s["customPath"]; ok {
		if path, ok := v.(string); !ok || path == "" {
			return errors.New("invalid custom path")
		}
	}
	return nil
}
func TestFrontendSpecificSettings(t *testing.T) {
	ctx := context.Background()
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	closed := 0
	reg := frontend.NewRegistry(func(frontend.Protocol) frontend.Host { return frontend.Host{} })
	if err := reg.Register(customSettingsFrontend{listenerFrontend{"127.0.0.1:0", &closed}}); err != nil {
		t.Fatal(err)
	}
	control := newFrontendControl(reg, cfg)
	if err := control.PutSettings(ctx, "test", frontend.Settings{"customPath": "/addon"}); err != nil {
		t.Fatal(err)
	}
	if err := control.PutSettings(ctx, "test", frontend.Settings{"customPath": false}); err == nil {
		t.Fatal("invalid custom setting accepted")
	}
	got, err := control.Settings(ctx, "test")
	if err != nil || got["customPath"] != "/addon" {
		t.Fatal(got, err)
	}
}

func TestFrontendPublishedAddressesSettings(t *testing.T) {
	ctx := context.Background()
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	closed := 0
	reg := frontend.NewRegistry(func(frontend.Protocol) frontend.Host { return frontend.Host{} })
	if err := reg.Register(listenerFrontend{"127.0.0.1:0", &closed}); err != nil {
		t.Fatal(err)
	}
	control := newFrontendControl(reg, cfg)

	// Accepts both subdomain and path-prefix published addresses, deduplicating.
	err = control.PutSettings(ctx, "test", frontend.Settings{
		"publishedAddresses": []any{
			"https://jellyfin.example.com/",
			"https://example.com/jellyfin/",
			"https://JELLYFIN.example.com",
		},
	})
	if err != nil {
		t.Fatalf("PutSettings(publishedAddresses): %v", err)
	}
	got, err := control.Settings(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	list, _ := got["publishedAddresses"].([]string)
	if len(list) != 2 || list[0] != "https://jellyfin.example.com" || list[1] != "https://example.com/jellyfin" {
		t.Fatalf("Settings publishedAddresses = %#v", got["publishedAddresses"])
	}

	// Saving other settings without publishedAddresses preserves the list.
	if err := control.PutSettings(ctx, "test", frontend.Settings{"discovery": true}); err != nil {
		t.Fatal(err)
	}
	got, _ = control.Settings(ctx, "test")
	if list, _ := got["publishedAddresses"].([]string); len(list) != 2 {
		t.Fatalf("expected publishedAddresses preserved, got %#v", got["publishedAddresses"])
	}

	// Invalid published address is rejected.
	if err := control.PutSettings(ctx, "test", frontend.Settings{"publishedAddresses": []any{"jellyfin.example.com"}}); err == nil {
		t.Fatal("missing scheme accepted")
	}
	if err := control.PutSettings(ctx, "test", frontend.Settings{"publishedAddresses": []any{"https://example.com/jellyfin?token=1"}}); err == nil {
		t.Fatal("query string accepted")
	}

	// Explicit empty list clears stored addresses.
	if err := control.PutSettings(ctx, "test", frontend.Settings{"publishedAddresses": []any{}}); err != nil {
		t.Fatal(err)
	}
	got, _ = control.Settings(ctx, "test")
	if _, exists := got["publishedAddresses"]; exists {
		t.Fatalf("expected publishedAddresses cleared, got %#v", got["publishedAddresses"])
	}
}
