package core

import (
	"context"
	"slices"
	"sync"
)

type localResponseKey struct{}

// WithLocalResponse requests an immediate answer from local snapshots and
// caches. Optional enrichment may refresh behind the response.
func WithLocalResponse(ctx context.Context) context.Context {
	return context.WithValue(ctx, localResponseKey{}, true)
}

// WithoutLocalResponse clears LocalResponse on ctx while preserving deadlines,
// cancellation and other request values.
func WithoutLocalResponse(ctx context.Context) context.Context {
	return context.WithValue(ctx, localResponseKey{}, false)
}

// LocalResponse reports whether this read must avoid enrichment waits.
func LocalResponse(ctx context.Context) bool {
	v, _ := ctx.Value(localResponseKey{}).(bool)
	return v
}

// WithColdDetail permits one bounded provider payload for this exact page's
// missing catalogue record. Nested list rows and unrelated recommendations
// retain the ordinary local-response policy.
func WithColdDetail(ctx context.Context, key RatingKey) context.Context {
	return context.WithValue(ctx, coldDetailKey{}, key)
}

type coldDetailKey struct{}

// ColdDetail reports whether key is the exact page allowed a cold fill.
func ColdDetail(ctx context.Context, key RatingKey) bool {
	requested, ok := ctx.Value(coldDetailKey{}).(RatingKey)
	return ok && requested == key
}

type noContainerExpandKey struct{}

// WithNoContainerExpand prevents plugin container-on-open expansion for
// background, list, artwork, ancestor, and internal lookup paths that read a
// plugin item without opening it as a container view.
func WithNoContainerExpand(ctx context.Context) context.Context {
	return context.WithValue(ctx, noContainerExpandKey{}, true)
}

// NoContainerExpand reports whether plugin container-on-open expansion is
// suppressed for this call.
func NoContainerExpand(ctx context.Context) bool {
	v, _ := ctx.Value(noContainerExpandKey{}).(bool)
	return v
}

// DetailPending records work admitted while reading one exact title. It is a
// response snapshot: completion after a read cannot make its missing data whole.
type DetailPending struct {
	key    RatingKey
	mu     sync.Mutex
	fields []string
}
type detailPendingKey struct{}

// WithDetailPending observes admitted work without scheduling any enrichment.
func WithDetailPending(ctx context.Context, key RatingKey) (context.Context, *DetailPending) {
	p := &DetailPending{key: key}
	return context.WithValue(ctx, detailPendingKey{}, p), p
}

// ReportDetailPending is called only by a producer with queued or running work.
// Work for nested rows or unrelated titles does not affect the requested page.
func ReportDetailPending(ctx context.Context, key RatingKey, fields ...string) {
	p, _ := ctx.Value(detailPendingKey{}).(*DetailPending)
	if p == nil || p.key != key {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, field := range fields {
		if !slices.Contains(p.fields, field) {
			p.fields = append(p.fields, field)
		}
	}
}

// Fields returns a stable copy of the partial response's field names.
func (p *DetailPending) Fields() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	fields := slices.Clone(p.fields)
	slices.Sort(fields)
	return fields
}
