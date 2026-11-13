package music

import "context"

type cachedOnlyKey struct{}

// WithCachedOnly interdit aux sources locales de lancer un téléchargement pendant un diagnostic.
func WithCachedOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, cachedOnlyKey{}, true)
}
func CachedOnly(ctx context.Context) bool { v, _ := ctx.Value(cachedOnlyKey{}).(bool); return v }
