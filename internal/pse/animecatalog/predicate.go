// Package animecatalog shares the catalogue counterpart of core.Title.IsAnime.
package animecatalog

// Predicate expects catalog_titles to be aliased as c. Animation alone is not anime.
const Predicate = `(EXISTS (SELECT 1 FROM catalog_keywords ak WHERE ak.key=c.key AND LOWER(ak.keyword)='anime') OR (LOWER(c.language)='ja' AND EXISTS (SELECT 1 FROM catalog_genres ag WHERE ag.key=c.key AND LOWER(ag.genre)='animation')))`
