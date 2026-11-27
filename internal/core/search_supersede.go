package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrSearchSuperseded is the cancellation cause of a search that a newer
// search from the same viewer and client replaced (the viewer typed on).
var ErrSearchSuperseded = errors.New("search superseded by a newer search from the same client")

// searchTurnIdle is how long a finished search stays the client's latest
// one when nothing newer arrives; searchTurnsMax bounds the table.
const (
	searchTurnIdle = time.Minute
	searchTurnsMax = 4096
	// searchGenerationWindow is how recent a client's latest search must be
	// for a lower generation to read as an older keystroke arriving late; a
	// client numbers its queries afresh after a relaunch.
	searchGenerationWindow = 5 * time.Second
)

type searchTurnKey struct {
	viewer int64
	client string
}

// searchTurn is a client's latest search: its query, the requests running
// it, and a channel closed when a newer query replaces it.
type searchTurn struct {
	query      string
	generation int64
	superseded chan struct{}
	running    map[uint64]context.CancelCauseFunc
	last       time.Time
}

type searchTurnTable struct {
	mu    sync.Mutex
	turns map[searchTurnKey]*searchTurn
	seq   uint64
}

var searchTurns = &searchTurnTable{turns: map[searchTurnKey]*searchTurn{}}

type searchTurnCtxKey struct{}

// EnterSearch makes ctx the latest search of (viewerID, client) for query.
// A request for a different query from the same viewer and client cancels
// every request still running an older one (context.Cause is
// ErrSearchSuperseded) and closes the older one's SearchSuperseded channel,
// so detached sub-work it started (a plugin's remote lookup) can be
// dropped unless a current search still wants it. Requests for the same
// query (a client asking for several types at once, or polling a pending
// search) share one turn. Without a viewer or client nothing can be
// superseded and ctx is returned as is. leave must be called when the
// request finishes; a ctx that already entered a search is returned as is.
func EnterSearch(ctx context.Context, viewerID int64, client, query string) (context.Context, func()) {
	return EnterSearchGeneration(ctx, viewerID, client, query, 0)
}

// EnterSearchGeneration is EnterSearch for a client that numbers its
// queries (Vela's generation, > 0). Requests can reach this point out of
// order: an older keystroke whose request arrives after a newer one's is
// itself superseded at once (it answers empty) instead of superseding the
// newer search.
func EnterSearchGeneration(ctx context.Context, viewerID int64, client, query string, generation int64) (context.Context, func()) {
	if viewerID == 0 || client == "" || ctx.Value(searchTurnCtxKey{}) != nil {
		return ctx, func() {}
	}
	return searchTurns.enter(ctx, searchTurnKey{viewer: viewerID, client: client}, normalizeSearchTurn(query), max(generation, 0), time.Now())
}

// SearchSuperseded returns a channel closed once a newer search from the
// same viewer and client replaced the search in ctx; nil (never closed)
// when the search cannot be superseded.
func SearchSuperseded(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	turn, _ := ctx.Value(searchTurnCtxKey{}).(*searchTurn)
	if turn == nil {
		return nil
	}
	return turn.superseded
}

// SearchWasSuperseded reports whether ctx was cancelled because a newer
// search from the same viewer and client replaced it. Such a search
// answers empty rather than failing: an error invites a client to retry
// the stale query, which would in turn supersede the newer one.
func SearchWasSuperseded(ctx context.Context) bool {
	return ctx != nil && errors.Is(context.Cause(ctx), ErrSearchSuperseded)
}

// IsShortSearch reports a query of one or two letters or digits: a prefix
// being typed. It answers from indexed local paths only, never a remote
// lookup or AI.
func IsShortSearch(query string) bool {
	n := 0
	for _, r := range query {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		n++
		if n >= 3 {
			return false
		}
	}
	return true
}

func normalizeSearchTurn(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}

func (t *searchTurnTable) enter(ctx context.Context, key searchTurnKey, query string, generation int64, now time.Time) (context.Context, func()) {
	var cancels []context.CancelCauseFunc
	t.mu.Lock()
	turn := t.turns[key]
	if turn != nil && turn.query != query && generation > 0 && generation < turn.generation && now.Sub(turn.last) < searchGenerationWindow {
		// A keystroke the client already typed past, arriving late.
		t.mu.Unlock()
		stale := &searchTurn{query: query, generation: generation, superseded: make(chan struct{})}
		close(stale.superseded)
		staleCtx, cancel := context.WithCancelCause(context.WithValue(ctx, searchTurnCtxKey{}, stale))
		cancel(ErrSearchSuperseded)
		return staleCtx, func() {}
	}
	if turn == nil || turn.query != query {
		if turn != nil {
			close(turn.superseded)
			for _, cancel := range turn.running {
				cancels = append(cancels, cancel)
			}
		} else if len(t.turns) >= searchTurnsMax {
			t.evictLocked(now)
		}
		turn = &searchTurn{query: query, generation: generation, superseded: make(chan struct{}), running: map[uint64]context.CancelCauseFunc{}}
		t.turns[key] = turn
	}
	turn.generation = max(turn.generation, generation)
	t.seq++
	id := t.seq
	turn.last = now
	runCtx, cancel := context.WithCancelCause(context.WithValue(ctx, searchTurnCtxKey{}, turn))
	turn.running[id] = cancel
	t.mu.Unlock()
	for _, c := range cancels {
		c(ErrSearchSuperseded)
	}
	return runCtx, func() {
		t.mu.Lock()
		delete(turn.running, id)
		turn.last = time.Now()
		t.mu.Unlock()
		cancel(context.Canceled)
	}
}

// evictLocked forgets idle turns; t.mu is held. A forgotten turn counts as
// superseded: nothing it started is still wanted.
func (t *searchTurnTable) evictLocked(now time.Time) {
	for k, turn := range t.turns {
		if len(turn.running) == 0 && now.Sub(turn.last) >= searchTurnIdle {
			close(turn.superseded)
			delete(t.turns, k)
		}
	}
	if len(t.turns) < searchTurnsMax {
		return
	}
	for k, turn := range t.turns {
		if len(turn.running) == 0 {
			close(turn.superseded)
			delete(t.turns, k)
		}
	}
}
