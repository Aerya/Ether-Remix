package catalog

import (
	"sync"
	"time"
)

// backgroundWriteHold is how long one catalogue background transaction
// aims to hold SQLite's single writer. Playback progress, sessions, play
// queues and receipts queue behind it, and clients drop a server whose
// timeline reports stall for seconds. A batch sized in rows does not
// bound that: the same 32 detail titles took about 30 ms on an NVMe Mac
// and 1-5 s on a small Docker host (#804), so background batches are
// sized by the hold they measured.
const backgroundWriteHold = 150 * time.Millisecond

// holdSpec bounds a holdSizer: the first batch's rows and the range.
type holdSpec struct{ start, lo, hi int }

var (
	// Detail writes, counted in detail titles (each with its credits and
	// facets). Their recommendations are written after, as summaries.
	detailHold = holdSpec{start: 2, lo: 1, hi: detailBatchMax}
	// Season and episode rows, with their guest-star credits.
	seasonRowHold = holdSpec{start: 2, lo: 1, hi: detailBatchMax}
	// Summary rows (lists, recommendations, filmographies, the fill).
	summaryHold = holdSpec{start: 20, lo: 4, hi: syncWriteBatch}
	// Pending tally deltas one fold moves.
	tallyHold = holdSpec{start: 2000, lo: 200, hi: 20000}
	// Queued facet values one naming batch names.
	facetNameHold = holdSpec{start: 100, lo: 10, hi: facetIDBatch}
)

// holdSizer sizes a background write batch so that one transaction holds
// the writer about backgroundWriteHold. It shrinks at once when a batch
// ran long and grows by half at most when batches finish early, within
// the spec's range. The zero value starts at the spec's start.
type holdSizer struct {
	mu sync.Mutex
	n  int
}

// writeHolds are the catalogue's background batch sizers.
type writeHolds struct {
	detail, seasonRows, summary, tally, facetNames holdSizer
}

// size is the next batch's row count.
func (s *holdSizer) size(spec holdSpec) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.n == 0 {
		s.n = min(max(spec.start, spec.lo), spec.hi)
	}
	return s.n
}

// observe records that a transaction of rows rows held the writer for took.
func (s *holdSizer) observe(spec holdSpec, rows int, took time.Duration) {
	if rows <= 0 || took <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.n == 0 {
		s.n = min(max(spec.start, spec.lo), spec.hi)
	}
	want := spec.hi
	if per := took / time.Duration(rows); per > 0 {
		want = int(backgroundWriteHold / per)
	}
	if want < s.n {
		s.n = max(want, spec.lo)
		return
	}
	// A short batch (fewer rows were pending than the size) says little
	// about a full one: grow by half of what was measured at most.
	s.n = min(want, max(s.n, rows+rows/2+1), spec.hi)
}
