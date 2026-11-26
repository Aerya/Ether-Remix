package core

import (
	"context"
	"sync"
)

// maxSearchQueries bounds one resolution's query log. Fan-out searches ask
// a handful of queries per indexer; a log that is full drops later queries
// and counts them.
const maxSearchQueries = 64

// SearchQuery is one indexer request a resolution made: which indexer, the
// query text and numbering it asked for, and how many releases came back.
// Indexer is the owner's indexer name. Consumers that export queries must
// replace it; the support capture does.
type SearchQuery struct {
	Indexer string `json:"indexer"`
	// IndexerKind is how the indexer is searched: newznab, torznab,
	// stremio add-on, built-in site or plugin source.
	IndexerKind string `json:"indexerKind,omitempty"`
	// Mode is the API search type (search, tvsearch, movie) or "cache"
	// when the indexer answered from its own result cache.
	Mode    string `json:"mode,omitempty"`
	Query   string `json:"query,omitempty"`
	Season  string `json:"season,omitempty"`
	Episode string `json:"episode,omitempty"`
	// IDs lists the identifier parameters sent, e.g. "tvdbid=81189".
	IDs     string `json:"ids,omitempty"`
	Results int    `json:"results"`
	Err     string `json:"err,omitempty"`
	Rescue  bool   `json:"rescue,omitempty"`
}

// SearchLog collects the indexer queries one resolution made.
type SearchLog struct {
	mu      sync.Mutex
	queries []SearchQuery
	dropped int
}

type searchLogKey struct{}
type searchRescueKey struct{}

// WithSearchLog returns ctx carrying a fresh query log.
func WithSearchLog(ctx context.Context) (context.Context, *SearchLog) {
	l := &SearchLog{}
	return context.WithValue(ctx, searchLogKey{}, l), l
}

// CarrySearchLog carries src's query log into dst, for a search that runs on
// a context detached from its caller.
func CarrySearchLog(dst, src context.Context) context.Context {
	if l, _ := src.Value(searchLogKey{}).(*SearchLog); l != nil {
		return context.WithValue(dst, searchLogKey{}, l)
	}
	return dst
}

// WithSearchRescue marks queries made under ctx as a play rescue's second pass.
func WithSearchRescue(ctx context.Context) context.Context {
	return context.WithValue(ctx, searchRescueKey{}, true)
}

// NoteSearchQuery appends q to ctx's query log, if any.
func NoteSearchQuery(ctx context.Context, q SearchQuery) {
	l, _ := ctx.Value(searchLogKey{}).(*SearchLog)
	if l == nil {
		return
	}
	if rescue, _ := ctx.Value(searchRescueKey{}).(bool); rescue {
		q.Rescue = true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.queries) >= maxSearchQueries {
		l.dropped++
		return
	}
	l.queries = append(l.queries, q)
}

// Queries returns a copy of the logged queries and how many were dropped.
func (l *SearchLog) Queries() ([]SearchQuery, int) {
	if l == nil {
		return nil, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]SearchQuery(nil), l.queries...), l.dropped
}
