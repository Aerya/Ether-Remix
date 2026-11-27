package core

import (
	"context"
	"errors"
	"testing"
)

func superseded(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestEnterSearchSupersedesOlderQueryFromSameClient(t *testing.T) {
	bg := context.Background()
	old, leaveOld := EnterSearch(bg, 7, "tv", "ja")
	defer leaveOld()
	sameQuery, leaveSame := EnterSearch(bg, 7, "tv", " JA ")
	defer leaveSame()
	otherClient, leaveOther := EnterSearch(bg, 7, "phone", "x")
	defer leaveOther()
	otherViewer, leaveViewer := EnterSearch(bg, 8, "tv", "x")
	defer leaveViewer()
	if old.Err() != nil || sameQuery.Err() != nil || superseded(SearchSuperseded(old)) {
		t.Fatal("the same query from the same client must share the turn, not cancel it")
	}

	newer, leaveNewer := EnterSearch(bg, 7, "tv", "jam")
	defer leaveNewer()
	for _, ctx := range []context.Context{old, sameQuery} {
		if !errors.Is(context.Cause(ctx), ErrSearchSuperseded) || !SearchWasSuperseded(ctx) || !superseded(SearchSuperseded(ctx)) {
			t.Fatal("a newer query must cancel every request running the older one")
		}
	}
	if newer.Err() != nil || SearchWasSuperseded(newer) || superseded(SearchSuperseded(newer)) {
		t.Fatal("the newest search must keep running")
	}
	if otherClient.Err() != nil || otherViewer.Err() != nil {
		t.Fatal("another client or viewer must not be superseded")
	}

	// A finished search stays the latest: its detached work is still wanted
	// until the client asks something else.
	leaveNewer()
	if superseded(SearchSuperseded(newer)) {
		t.Fatal("finishing a search must not supersede it")
	}
	_, leaveNext := EnterSearch(bg, 7, "tv", "jame")
	defer leaveNext()
	if !superseded(SearchSuperseded(newer)) {
		t.Fatal("the next query must supersede a finished search's detached work")
	}

	// Without a client nothing can be superseded; nested entry is a no-op.
	anon, leaveAnon := EnterSearch(bg, 7, "", "a")
	defer leaveAnon()
	if SearchSuperseded(anon) != nil {
		t.Fatal("a search without a client cannot be superseded")
	}
	nested, leaveNested := EnterSearch(newer, 7, "tv", "other")
	leaveNested()
	if nested != newer {
		t.Fatal("a ctx that already entered a search is returned as is")
	}
}

func TestIsShortSearch(t *testing.T) {
	for q, want := range map[string]bool{"": true, "j": true, " ja ": true, "a b": true, "jam": false, "007": false, "up": true} {
		if IsShortSearch(q) != want {
			t.Fatalf("IsShortSearch(%q) = %v", q, !want)
		}
	}
}

func TestEnterSearchGenerationLateKeystrokeDoesNotSupersedeNewer(t *testing.T) {
	bg := context.Background()
	newer, leaveNewer := EnterSearchGeneration(bg, 9, "tv", "jame", 4)
	defer leaveNewer()
	late, leaveLate := EnterSearchGeneration(bg, 9, "tv", "jam", 3)
	defer leaveLate()
	if !SearchWasSuperseded(late) || newer.Err() != nil {
		t.Fatal("an older generation arriving late must answer superseded, not cancel the newer search")
	}
	next, leaveNext := EnterSearchGeneration(bg, 9, "tv", "james", 5)
	defer leaveNext()
	if !SearchWasSuperseded(newer) || next.Err() != nil {
		t.Fatal("a newer generation must supersede the older search")
	}
}
