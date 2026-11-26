package resolver

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// pseTitleWithNumber applique releaseparse.PSETitleWithNumber aux titres de
// la requête (titre, original, anglais, alternatifs).
func pseTitleWithNumber(rel core.Release, sreq core.SearchRequest) core.Release {
	wants := append([]string{sreq.Title, sreq.OriginalTitle, sreq.EnglishTitle}, sreq.AlternativeTitles...)
	rel.Info = releaseparse.PSETitleWithNumber(rel.Info, wants...)
	return rel
}
