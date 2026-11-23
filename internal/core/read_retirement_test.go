package core

import "testing"

// A retiring session interrupts each in-flight request once; a request that
// finished cleared its hook and is never touched.
func TestReadRetirementInterrupt(t *testing.T) {
	var tracker ReadRetirement
	calls := 0
	tracker.OnInterrupt(func() { calls++ })
	tracker.Interrupt()
	tracker.Interrupt()
	if calls != 1 {
		t.Fatalf("interrupt ran %d times, want once", calls)
	}
	tracker.OnInterrupt(func() { calls++ })
	tracker.OnInterrupt(nil)
	tracker.Interrupt()
	if calls != 1 {
		t.Fatalf("a cleared hook ran")
	}
}
