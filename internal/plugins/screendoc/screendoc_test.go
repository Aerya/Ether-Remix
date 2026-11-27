package screendoc

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func sportsOptions(presentation string) Options {
	return Options{Presentation: presentation, Feeds: []string{"sports"}, Kinds: []string{"clips"}, Asset: func(string) bool { return true }}
}

// The documents Vela's renderer is built against compile, serve with their
// derived deps, and stay byte-stable across serves.
func TestVelaFixturesCompileAndServe(t *testing.T) {
	for _, name := range []string{"sports-index", "sports-game", "news-index"} {
		raw, err := os.ReadFile("testdata/" + name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Compile(name+".json", raw, sportsOptions(""))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		meta := ServeMeta{Screen: "sports", Page: "index", Version: "2.0.0", Title: "Sports", Presentation: PresentSection}
		first, err := doc.Serve(meta)
		if err != nil {
			t.Fatal(err)
		}
		second, _ := doc.Serve(meta)
		if string(first) != string(second) {
			t.Fatalf("%s: serving twice differs", name)
		}
		var served struct {
			Sources map[string]struct {
				Deps []string `json:"deps"`
			} `json:"sources"`
		}
		if err := json.Unmarshal(first, &served); err != nil {
			t.Fatal(err)
		}
		if name == "sports-index" {
			if got := served.Sources["schedule"].Deps; strings.Join(got, ",") != "date,league" {
				t.Fatalf("schedule deps = %v", got)
			}
			if got := served.Sources["viewer"].Deps; len(got) != 0 {
				t.Fatalf("viewer deps = %v", got)
			}
		}
	}
}

func TestCompileRefusesWhatADocumentMayNotSay(t *testing.T) {
	cases := map[string]string{
		"url":           `{"schema":1,"kind":"screen","renderer":19,"tree":{"type":"image","src":"https://evil.example/x.png"}}`,
		"css url":       `{"schema":1,"kind":"screen","renderer":19,"tree":{"type":"box","style":{"background-image":"url(//evil.example/x.png)"}}}`,
		"old renderer":  `{"schema":1,"kind":"screen","renderer":3,"tree":{"type":"box"}}`,
		"foreign feed":  `{"schema":1,"kind":"screen","renderer":19,"sources":{"a":{"from":"liveEvents","feed":"other"}},"tree":{"type":"box"}}`,
		"undeclared":    `{"schema":1,"kind":"screen","renderer":19,"sources":{"a":{"from":"liveEvents","feed":"sports","query":{"league":"{param.league}"}}},"tree":{"type":"box"}}`,
		"source chain":  `{"schema":1,"kind":"screen","renderer":19,"sources":{"a":{"from":"liveEvent","feed":"sports","id":"{src.b.id}"}},"tree":{"type":"box"}}`,
		"bad context":   `{"schema":1,"kind":"screen","renderer":19,"sources":{"a":{"from":"liveEvent","feed":"sports","id":"{context.event}"}},"tree":{"type":"box"}}`,
		"unknown field": `{"schema":1,"kind":"screen","renderer":19,"script":"alert(1)","tree":{"type":"box"}}`,
		"unknown type":  `{"schema":1,"kind":"screen","renderer":19,"sources":{"a":{"from":"sql","q":"select"}},"tree":{"type":"box"}}`,
	}
	for name, raw := range cases {
		_, err := Compile("doc.json", []byte(raw), sportsOptions(""))
		var docErr *Error
		if !errors.As(err, &docErr) || len(docErr.Fields) == 0 {
			t.Fatalf("%s: compiled, want a refusal", name)
		}
	}
	// A player overlay may read the event it is drawn over.
	overlay := `{"schema":1,"kind":"screen","renderer":19,"sources":{"game":{"from":"liveEvent","feed":"sports","id":"{context.event}"}},"tree":{"type":"box"}}`
	doc, err := Compile("scorebug.json", []byte(overlay), sportsOptions(PresentPlayerOverlay))
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Sources["game"].Deps; len(got) != 1 || got[0] != "context.event" {
		t.Fatalf("overlay deps = %v", got)
	}
}
