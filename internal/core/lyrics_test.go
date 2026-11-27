package core

import (
	"testing"
	"time"
)

func TestParseLRC(t *testing.T) {
	got := ParseLRC("[ar:Daft Punk]\n[00:12.50] There are so many things\n[00:20.1][01:05.25]Chorus\nno stamp\n[00:15.00]\n")
	want := []LyricLine{
		{Start: 12*time.Second + 500*time.Millisecond, Text: "There are so many things"},
		{Start: 15 * time.Second, Text: ""},
		{Start: 20*time.Second + 100*time.Millisecond, Text: "Chorus"},
		{Start: 65*time.Second + 250*time.Millisecond, Text: "Chorus"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i].Start != want[i].Start || got[i].Text != want[i].Text {
			t.Fatalf("line %d: got %+v want %+v", i, got[i], want[i])
		}
	}
	// Each line ends where the next begins; the last end is unknown.
	if got[0].End != 15*time.Second || got[3].End != 0 {
		t.Fatalf("line ends: %v %v", got[0].End, got[3].End)
	}
}

func TestParseEnhancedLRCWords(t *testing.T) {
	got := ParseLRC("[00:10.00]<00:10.00>One <00:10.40>more <00:10.90>time\n[00:13.00]<00:13.00>Celebrate\n")
	if len(got) != 2 || got[0].Text != "One more time" || len(got[0].Words) != 3 {
		t.Fatalf("got %+v", got)
	}
	w := got[0].Words
	if w[0].Start != 10*time.Second || w[0].End != 10400*time.Millisecond || w[2].Text != "time" || w[2].End != 13*time.Second {
		t.Fatalf("words %+v", w)
	}
}
