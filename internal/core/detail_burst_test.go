package core

import (
	"testing"
	"time"
)

func TestDetailBurstHoldsLibrarySyncsNotPeople(t *testing.T) {
	var b detailBursts
	now := time.Unix(1_800_000_000, 0)
	// A person reopening the same few pages is never held.
	for i := 0; i < 50; i++ {
		if b.note(now, 1, "tv", RatingKey(i%3+1)) {
			t.Fatal("repeat reads of three titles held")
		}
	}
	// A sync reading many distinct titles is held after the limit...
	held := false
	for i := 0; i < 40; i++ {
		held = b.note(now.Add(time.Duration(i)*time.Second), 2, "tracearr", RatingKey(1000+i))
	}
	if !held {
		t.Fatal("sync of 40 distinct titles in a minute not held")
	}
	// ...stays held through the next window, and other clients are unaffected.
	if !b.note(now.Add(90*time.Second), 2, "tracearr", RatingKey(5000)) {
		t.Fatal("sync released at the next window")
	}
	if b.note(now.Add(90*time.Second), 2, "phone", RatingKey(5000)) {
		t.Fatal("another client of the same viewer held")
	}
	// A quiet window releases it.
	if b.note(now.Add(5*time.Minute), 2, "tracearr", RatingKey(6000)) {
		t.Fatal("sync still held after going quiet")
	}
}
