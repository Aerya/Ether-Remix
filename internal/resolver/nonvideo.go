package resolver

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func nonVideoRelease(rel core.Release, t *core.Title, sreq core.SearchRequest) bool {
	titles := nonVideoExpectedTitles(t, sreq)
	if releaseparse.IsNonVideoRelease(rel.Name, rel.FileName, titles...) {
		return true
	}
	if rel.Target != nil && rel.Target.ExactFile != "" && releaseparse.IsNonVideoRelease("", rel.Target.ExactFile, titles...) {
		return true
	}
	return false
}

func nonVideoExpectedTitles(t *core.Title, sreq core.SearchRequest) []string {
	var out []string
	add := func(s string) {
		if s != "" {
			out = append(out, s)
		}
	}
	add(sreq.Title)
	add(sreq.OriginalTitle)
	add(sreq.EnglishTitle)
	add(sreq.EpisodeTitle)
	for _, alt := range sreq.AlternativeTitles {
		add(alt)
	}
	if t != nil {
		add(t.Title)
		add(t.OriginalTitle)
		add(t.EnglishTitle)
		add(t.ShowTitle)
		add(t.ShowOriginalTitle)
		add(t.ShowEnglishTitle)
		for _, alt := range t.AlternativeTitles {
			add(alt)
		}
		for _, alt := range t.ShowAlternativeTitles {
			add(alt)
		}
	}
	return out
}
