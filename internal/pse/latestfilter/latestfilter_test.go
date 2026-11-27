package latestfilter

import (
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestDefaultsCompileAndHide(t *testing.T) {
	raw, _ := json.Marshal(Defaults())
	c, err := Parse(string(raw))
	if err != nil || c.Empty() {
		t.Fatalf("parse: %v %v", c, err)
	}
	anime := &core.Title{Type: core.TypeMovie, Genres: []string{"Animation", "Comedy"}}
	indian := &core.Title{Type: core.TypeMovie, Countries: []string{"India"}}
	hindi := &core.Title{Type: core.TypeShow, Language: "hi"}
	ok := &core.Title{Type: core.TypeShow, Genres: []string{"Drama"}, Countries: []string{"United States of America"}, Language: "en"}
	if !c.Movie.Hidden(anime) || !c.Movie.Hidden(indian) || !c.Show.Hidden(hindi) || c.Show.Hidden(ok) {
		t.Fatal("exclusions inattendues")
	}
}

func TestParseEmptyAndInvalid(t *testing.T) {
	if c, err := Parse(""); err != nil || !c.Empty() {
		t.Fatalf("vide : %v %v", c, err)
	}
	for _, bad := range []string{`{`, `{"x":1}`, `{"movie":{"rules":[{"field":"genre","op":"in","value":["a"]}]}}`,
		`{"movie":{"exclude":[{"field":"nope","op":"in","value":["a"]}]}}`} {
		if _, err := Parse(bad); err == nil {
			t.Fatalf("%s accepté", bad)
		}
	}
}

func TestNormalize(t *testing.T) {
	if out, err := Normalize(`{"movie":{},"show":{"match":"any"}}`); err != nil || out != "" {
		t.Fatalf("%q %v", out, err)
	}
	out, err := Normalize(`{"show":{"exclude":[{"field":"genre","op":"in","value":["News"]}]}}`)
	if err != nil || out == "" {
		t.Fatalf("%q %v", out, err)
	}
}
