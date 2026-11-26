package core

import (
	"context"
	"testing"
)

func TestProviderMissHint(t *testing.T) {
	var h ProviderMissHint
	for range ProviderMissDemote - 1 {
		h.Miss("a")
	}
	if h.Demoted("a") {
		t.Fatal("demoted before the threshold")
	}
	h.Miss("a")
	if !h.Demoted("a") || h.Demoted("b") {
		t.Fatal("demotion should follow misses per provider")
	}
	h.Hit("a")
	if h.Demoted("a") {
		t.Fatal("a delivery should lift the demotion")
	}
	if ProviderMissHintFrom(context.Background()) != nil {
		t.Fatal("hint on a bare context")
	}
	if ProviderMissHintFrom(WithProviderMissHint(context.Background(), &h)) != &h {
		t.Fatal("hint not carried by the context")
	}
}
