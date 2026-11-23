package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

// A request Ether cannot encode for the guest never reached the plugin:
// repeated ones must not close the runtime, record crash failures or
// disable the plugin (#645).
func TestRequestEncodeFailureIsNotAPluginCrash(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := env.installHello(t, "1.0.0")

	env.fake.mu.Lock()
	env.fake.routes["/ether/v1/frontend/feed/upload"] = func(Request) (Response, error) {
		return Response{}, &wasm.Error{
			Status:  wasm.StatusError,
			Message: "json: error calling MarshalJSON for type json.RawMessage: invalid character '-' in numeric literal",
			Err:     wasm.ErrRequestEncode,
		}
	}
	env.fake.mu.Unlock()

	// More than the five-failures-in-ten-minutes disable threshold.
	for i := 0; i < 6; i++ {
		_, err := env.m.Call(ctx, rec.ID, Request{
			Method: "POST",
			Path:   "/ether/v1/frontend/feed/upload",
			Body:   json.RawMessage(`--boundary`),
		})
		if !errors.Is(err, ErrUnsupportedBody) || !errors.Is(err, wasm.ErrRequestEncode) {
			t.Fatalf("Call err = %v, want ErrUnsupportedBody wrapping wasm.ErrRequestEncode", err)
		}
	}

	gotRec, err := env.m.Get(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !gotRec.Enabled {
		t.Fatal("plugin was disabled by host-side encoding failures")
	}
	health, err := env.m.HealthOf(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if health.Status == "error" || health.Status == "disabled" || health.Restarts > 0 || len(health.Failures) > 0 {
		t.Fatalf("encoding failures recorded crash/failure: %+v", health)
	}
}
