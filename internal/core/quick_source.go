package core

import "context"

// QuickSource scopes discovery and acquisition to one owner-selected source.
// It is only set by the resolver, never by background indexing.
type quickSourceKey struct{}
type quickSourceScope struct {
	Source string
	IDs    map[string]bool
}

func WithQuickSource(ctx context.Context, source string, ids map[string]bool) context.Context {
	return context.WithValue(ctx, quickSourceKey{}, quickSourceScope{source, ids})
}
func QuickSource(ctx context.Context) string {
	s, _ := ctx.Value(quickSourceKey{}).(quickSourceScope)
	return s.Source
}
func QuickIndexerAllowed(ctx context.Context, id string) bool {
	s, ok := ctx.Value(quickSourceKey{}).(quickSourceScope)
	return !ok || s.IDs[id]
}
