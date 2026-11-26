package stremio

import (
	"context"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// streamDecisionBudget bounds the wait for the resolver's verdict; past it
// the stream is offered as before.
const streamDecisionBudget = 4 * time.Second

// pseNoSourceMaxAge is how long a client may keep an empty stream list.
const pseNoSourceMaxAge = "max-age=120"

// pseNoSource answers an empty stream list, and reports true, when the
// resolver already said that no release exists for this request. Any doubt
// (no decider, no verdict in time) keeps the stream.
func (i *instance) pseNoSource(ctx context.Context, w http.ResponseWriter, credential string, plan frontend.PlanRequest) bool {
	decider, ok := i.host.LazyStreams.(frontend.LazyStreamDecider)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, streamDecisionBudget)
	defer cancel()
	if decider.WarmDecision(ctx, credential, plan, streamDecisionBudget) != frontend.WarmNoSource {
		return false
	}
	w.Header().Set("Cache-Control", "private, "+pseNoSourceMaxAge)
	writeJSON(w, map[string]any{"streams": []any{}})
	return true
}
