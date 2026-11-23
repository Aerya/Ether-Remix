package stremio

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

type descFacts struct {
	frontendtest.Experience
	facts frontend.DetailFacts
}

func (d descFacts) DetailFacts(context.Context, *frontend.Viewer, frontend.Title) frontend.DetailFacts {
	return d.facts
}

func (d descFacts) PageRatings(context.Context, *frontend.Viewer, []frontend.Title) map[frontend.RatingKey]frontend.DetailFacts {
	return nil
}

func TestStremioDescriptionPrefixesHeaderAndNamesRatings(t *testing.T) {
	exp := descFacts{facts: frontend.DetailFacts{
		Header: "96% Match", RatingsKnown: true,
		Sources: []frontend.ExternalRating{{Source: "imdb", Name: "IMDb", Display: "8.2", Score: 82}},
	}}
	got := stremioDescription(httptest.NewRequest("GET", "/meta", nil), exp, &frontend.Viewer{ID: 1}, frontend.Title{Summary: "A linguist."})
	want := "96% Match\n\nA linguist.\n\nIMDb 8.2"
	if got != want {
		t.Fatalf("description %q", got)
	}
	exp.facts = frontend.DetailFacts{}
	plain := stremioDescription(httptest.NewRequest("GET", "/meta", nil), exp, &frontend.Viewer{ID: 1}, frontend.Title{Summary: "A linguist."})
	if plain != "A linguist." {
		t.Fatalf("thin model %q", plain)
	}
}

func TestStremioTrailers(t *testing.T) {
	trailers, streams := stremioTrailers([]frontend.Extra{
		{Title: "Official Trailer", Kind: "trailer", Source: "YouTube", SourceID: "abc123"},
		{Title: "Teaser", Kind: "teaser", Source: "YouTube", SourceID: "def456"},
		{Title: "Featurette", Kind: "featurette", Source: "YouTube", SourceID: "ghi789"},
		{Title: "Discover MP4", Kind: "trailer"},
	})
	if len(trailers) != 3 || len(streams) != 3 {
		t.Fatalf("got %d trailers, %d streams", len(trailers), len(streams))
	}
	if trailers[0]["source"] != "abc123" || trailers[0]["type"] != "Trailer" ||
		trailers[1]["type"] != "Trailer" || trailers[2]["type"] != "Clip" {
		t.Fatalf("unexpected trailers %#v", trailers)
	}
	if streams[0]["ytId"] != "abc123" || streams[0]["title"] != "Official Trailer" {
		t.Fatalf("unexpected trailerStreams %#v", streams)
	}
}
