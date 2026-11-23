package flight

import (
	"context"
	"testing"
)

func TestGroupDoAndDetachedRecoverFromPanic(t *testing.T) {
	t.Parallel()
	var g Group[string, string]

	if _, err := g.Do(context.Background(), "k1", func(context.Context) (string, error) {
		panic("boom")
	}); err == nil {
		t.Fatal("expected error from panicking Do")
	}
	if g.Has("k1") {
		t.Fatal("panicking Do left key stuck in flight map")
	}
	v, err := g.Do(context.Background(), "k1", func(context.Context) (string, error) {
		return "ok", nil
	})
	if err != nil || v != "ok" {
		t.Fatalf("post-panic Do = (%q, %v), want (ok, nil)", v, err)
	}

	if _, err := g.DoDetached(context.Background(), "k2", func(context.Context) (string, error) {
		panic("detached boom")
	}); err == nil {
		t.Fatal("expected error from panicking DoDetached")
	}
	if g.Has("k2") {
		t.Fatal("panicking DoDetached left key stuck in flight map")
	}
	v, err = g.DoDetached(context.Background(), "k2", func(context.Context) (string, error) {
		return "ok2", nil
	})
	if err != nil || v != "ok2" {
		t.Fatalf("post-panic DoDetached = (%q, %v), want (ok2, nil)", v, err)
	}
}
