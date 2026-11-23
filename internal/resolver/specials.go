package resolver

import "github.com/braedonsaunders/ether/internal/core"

func specialShowTitles(req core.SearchRequest) []string {
	if req.EnglishTitle != "" {
		return append([]string{req.Title, req.OriginalTitle, req.EnglishTitle}, req.AlternativeTitles...)
	}
	return append([]string{req.Title, req.OriginalTitle}, req.AlternativeTitles...)
}
