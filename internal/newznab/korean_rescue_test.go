package newznab

import (
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestRescueAttemptsIncludeKoreanAlternativeBareEpisodeAndAirDate(t *testing.T) {
	cl := New(config.Indexer{Name: "test", URL: "http://example.invalid", Enabled: true}, nil, Options{})
	req := core.SearchRequest{
		Type:              core.TypeEpisode,
		IDs:               core.IDs{TMDB: 65282, IMDB: "tt7997010", TVDB: 298704},
		Title:             "Home Alone",
		OriginalTitle:     "나 혼자 산다",
		AlternativeTitles: []string{"I Live Alone"},
		Year:              2013,
		Season:            1,
		Episode:           674,
		Released:          time.Date(2024, 11, 15, 0, 0, 0, 0, time.UTC),
	}

	attempts := cl.rescueAttempts(req, &Caps{})
	var queries []string
	for _, a := range attempts {
		if q := a.Get("q"); q != "" {
			queries = append(queries, q)
		}
	}
	for _, want := range []string{
		"I Live Alone S01E674",
		"I Live Alone E674",
		"나 혼자 산다 E674",
		"I Live Alone 241115",
		"I Live Alone 2024.11.15",
	} {
		if !slices.Contains(queries, want) {
			t.Errorf("rescue queries missing %q; got %v", want, queries)
		}
	}
}
