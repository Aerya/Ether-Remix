package core

import (
	"context"
	"sync"
)

// ProviderMissDemote is how many more "no such article" answers than
// deliveries a Usenet provider may give for one posted file before it is
// asked after its tier peers for that file's articles. A server with shorter
// retention then stops costing a round trip on every segment of an old post.
// It is still asked whenever its peers miss too, so nothing is proved
// missing that it could have served.
const (
	ProviderMissDemote = 3
	maxHintProviders   = 32
	maxHintScore       = 2 * ProviderMissDemote
)

// ProviderMissHint remembers, for one posted file, which Usenet providers
// keep missing its articles. Keys identify provider pool generations. Safe for
// concurrent use; the zero value is ready.
type ProviderMissHint struct {
	mu     sync.Mutex
	misses map[string]uint8
}

// Miss records a "no such article" answer from provider.
func (h *ProviderMissHint) Miss(provider string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	score := h.misses[provider]
	if score == maxHintScore || (score == 0 && len(h.misses) == maxHintProviders) {
		return
	}
	if h.misses == nil {
		h.misses = make(map[string]uint8)
	}
	h.misses[provider] = score + 1
}

// Hit records a delivery from provider, letting it work its way back up.
func (h *ProviderMissHint) Hit(provider string) {
	h.mu.Lock()
	switch score := h.misses[provider]; score {
	case 0:
	case 1:
		delete(h.misses, provider)
	default:
		h.misses[provider] = score - 1
	}
	h.mu.Unlock()
}

// Demoted reports whether provider should be asked after its tier peers.
func (h *ProviderMissHint) Demoted(provider string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.misses[provider] >= ProviderMissDemote
}

type providerMissHintKey struct{}

// WithProviderMissHint attaches a file's hint to the article requests made
// with ctx. A nil hint returns ctx unchanged.
func WithProviderMissHint(ctx context.Context, h *ProviderMissHint) context.Context {
	if h == nil {
		return ctx
	}
	return context.WithValue(ctx, providerMissHintKey{}, h)
}

// ProviderMissHintFrom returns the hint attached by WithProviderMissHint, or
// nil.
func ProviderMissHintFrom(ctx context.Context) *ProviderMissHint {
	h, _ := ctx.Value(providerMissHintKey{}).(*ProviderMissHint)
	return h
}
