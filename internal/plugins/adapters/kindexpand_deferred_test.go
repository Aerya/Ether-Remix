package adapters

import "testing"

func TestSearchNameFolding(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"Dave Matthews Band", "dave matthews band"},
		{"The Beatles", "beatles"},
		{"Simon & Garfunkel", "Simon and Garfunkel"},
		{"AC/DC", "acdc"},
	} {
		if searchName(c.a) != searchName(c.b) {
			t.Fatalf("%q and %q should fold alike: %q vs %q", c.a, c.b, searchName(c.a), searchName(c.b))
		}
	}
	if searchName("Dave Matthews") == searchName("Dave Matthews Band") {
		t.Fatal("a shorter name matched")
	}
}

func TestLyricsLinesFromWire(t *testing.T) {
	lines := linesFromWire([]wireLyricsLine{
		{StartMs: 5000, EndMs: 7000, Words: []wireLyricWord{{StartMs: 5000, EndMs: 5600, Text: "Hello"}, {StartMs: 5600, EndMs: 7000, Text: "world"}}, Translation: "Hola mundo", Romanization: "harō wārudo"},
		{StartMs: 1000, Text: "First"},
		{StartMs: 9000},
	})
	if len(lines) != 2 || lines[0].Text != "First" || lines[1].Text != "Hello world" || len(lines[1].Words) != 2 {
		t.Fatalf("lines %+v", lines)
	}
	if lines[1].Translation != "Hola mundo" || lines[1].Romanization != "harō wārudo" || lines[1].Words[1].End.Milliseconds() != 7000 {
		t.Fatalf("line %+v", lines[1])
	}
}
