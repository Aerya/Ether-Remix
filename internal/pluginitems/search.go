package pluginitems

import (
	"context"
	"strings"
	"unicode"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
	"github.com/braedonsaunders/ether/internal/store"
)

// SearchQuery scopes a full-text search to a single library (spec §2.3, §3.4).
type SearchQuery struct {
	// NoTotal skips exact full-text COUNT(*) scans when total is not needed.
	NoTotal bool
	// CheapTotal, paired with NoTotal, still populates TotalExact when the
	// result fits on one page or is already cached for the current generation.
	CheapTotal bool
	Policy     *libraryfilter.Filter
	TabPolicy  *libraryfilter.Filter
	ViewerID   int64
	Hidden     bool
	LibraryID  core.LibraryID
	Query      string
	Level      string
	Levels     []string
	PersonID   int64
	Facets     map[string][]string
	// AllFacets requires every listed value per field; ExcludeFacets
	// drops items having any listed value; AnyGroups adds one OR-group
	// per entry (single field or cross-field {any: [...]}); Empty tests
	// field presence/absence; ExcludeQuery drops items matching the text
	// query (see BrowseQuery, #767, #807).
	AllFacets     map[string][]string
	ExcludeFacets map[string][]string
	AnyGroups     []core.KindBrowseFacetGroup
	Empty         map[string]bool
	ExcludeQuery  string
	Limit         int
}

// Search executes a library-scoped full-text search over plugin items
// (SQLite FTS5 or PostgreSQL tsvector, spec §2.3, §3.4, §10).
func (s *Service) Search(ctx context.Context, q SearchQuery) (BrowsePage, error) {
	if q.LibraryID <= 0 {
		return BrowsePage{}, nil
	}
	if len(q.Empty) > 0 {
		rec, err := s.GetKindByLibrary(ctx, q.LibraryID)
		if err != nil {
			return BrowsePage{}, err
		}
		if _, err := validateBrowseEmptyFields(rec.Decl, q.Empty); err != nil {
			return BrowsePage{}, err
		}
	}
	tokens := tokenizeSearchQuery(q.Query)
	if len(tokens) == 0 {
		return BrowsePage{}, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultBrowseLimit
	}
	if limit > MaxBrowseLimit {
		limit = MaxBrowseLimit
	}
	if store.DialectOf(s.db) == store.Postgres {
		return s.searchPostgres(ctx, q, tokens, limit)
	}
	return s.searchSQLite(ctx, q, tokens, limit)
}

// searchTokenPrefix reports whether token i of n is matched as a prefix.
// The last word is the one being typed; a short prefix ("s", "th") or a
// short earlier word ("the") expanded to every word it begins makes the
// full-text match read most of a library, so those match whole words only.
func searchTokenPrefix(tok string, i, n int) bool {
	runes := len([]rune(tok))
	if i == n-1 {
		return runes >= 3
	}
	return runes >= 4
}

func tokenizeSearchQuery(raw string) []string {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range strings.ToLower(raw) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	if len(tokens) > 16 {
		tokens = tokens[:16]
	}
	return tokens
}

func escapeLike(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch r {
		case '\\', '%', '_':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func searchTitleTierArgs(rawQuery string) (exactTitle, exactSort, prefixTitle, prefixSort string) {
	exactTitle = strings.ToLower(strings.TrimSpace(rawQuery))
	exactSort = SortTitle(rawQuery)
	if exactSort == "" {
		exactSort = exactTitle
	}
	prefixTitle = escapeLike(exactTitle) + "%"
	prefixSort = escapeLike(exactSort) + "%"
	return exactTitle, exactSort, prefixTitle, prefixSort
}

func (s *Service) resolveSearchTotal(
	ctx context.Context,
	q SearchQuery,
	countSQL string,
	whereKey string,
	args []any,
	hasMore bool,
	itemCount int,
) (int, bool, error) {
	if q.NoTotal && !q.CheapTotal {
		return itemCount, false, nil
	}
	if !hasMore {
		return itemCount, true, nil
	}

	cacheable := !strings.Contains(whereKey, "state_watch")
	var (
		ckey   string
		gen    uint64
		minute int64
	)
	if cacheable {
		gen = s.writeGen.Load()
		minute = s.countMinuteBucket()
		ckey = browseCountCacheKey("search", whereKey, args)
		if cached, ok := s.countCacheGet(ckey, gen, minute); ok {
			return cached, true, nil
		}
	}
	if q.NoTotal && q.CheapTotal {
		return itemCount, false, nil
	}

	var total int
	if err := s.db.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return 0, false, err
	}
	if cacheable {
		s.countCachePut(ckey, total, gen, minute)
	}
	return total, true, nil
}
