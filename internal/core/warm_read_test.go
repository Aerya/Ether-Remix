package core

import (
	"context"
	"testing"
)

func TestWarmReadWindowRetiresOnlyBehindPlayhead(t *testing.T) {
	var window WarmReadWindow
	parent := WithWarmReadWindow(t.Context(), &window)
	behind, doneBehind := WarmReadContext(parent, 100)
	defer doneBehind()
	ahead, doneAhead := WarmReadContext(parent, 200)
	defer doneAhead()
	window.Observe(100)
	if behind.Err() != context.Canceled {
		t.Fatal("behind read survived")
	}
	if ahead.Err() != nil {
		t.Fatal("ahead read cancelled")
	}
	stale, doneStale := WarmReadContext(parent, 50)
	defer doneStale()
	if stale.Err() != context.Canceled {
		t.Fatal("late stale run admitted")
	}
	window.Observe(25) // a backward seek admits new warming at that position
	backward, doneBackward := WarmReadContext(parent, 50)
	defer doneBackward()
	if backward.Err() != nil {
		t.Fatal("backward seek warm was rejected")
	}
	window.Observe(300)
	if ahead.Err() != context.Canceled {
		t.Fatal("forward seek did not retire old warm")
	}
	var other WarmReadWindow
	independent, doneIndependent := WarmReadContext(WithWarmReadWindow(parent, &other), 50)
	defer doneIndependent()
	if independent.Err() != nil {
		t.Fatal("another generation was cancelled")
	}
}
