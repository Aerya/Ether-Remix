package experience

import (
	"context"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
)

// WithBootstrapProof shares policy reads only inside a bootstrap assembly whose
// caller checks the shared policy epoch again before publishing any handles.
// It must never be used for a standalone read or a user mutation.
func WithBootstrapProof(ctx context.Context, viewer int64, epoch int64) context.Context {
	return context.WithValue(ctx, bootstrapProofKey{}, &bootstrapProof{viewer: viewer, epoch: epoch})
}

type bootstrapProofKey struct{}
type bootstrapProof struct {
	mu            sync.Mutex
	viewer, epoch int64
	active        *core.User
	revision      string
}

func bootstrapProofFor(ctx context.Context, id int64) *bootstrapProof {
	p, _ := ctx.Value(bootstrapProofKey{}).(*bootstrapProof)
	if p != nil && p.viewer == id {
		return p
	}
	return nil
}
func bootstrapActive(ctx context.Context, read func(context.Context, int64) (*core.User, error), id int64) (*core.User, error) {
	p := bootstrapProofFor(ctx, id)
	if p == nil {
		return read(ctx, id)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != nil {
		return p.active, nil
	}
	u, err := read(ctx, id)
	if err == nil && u != nil && u.ID == id {
		p.active = u
	}
	return u, err
}
