package usenet

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/usenet/nntptest"
)

// A background BODY read reserves the worst case for the provider's largest
// article (about 128 MiB by default) but must settle to what it moved, or the
// default 512 MiB daily allowance admits only three reads (#791).
func TestBackgroundBodyReadsSettleToBytesMoved(t *testing.T) {
	s := nntptest.Start(t, nntptest.Options{})
	data := bytes.Repeat([]byte("ether"), 150<<10) // 750 KiB: CheckBodyPart abandons it after the header
	for i := range 6 {
		addSeg(t, s, fmt.Sprintf("a%d@x", i), data)
	}
	f := newFetcher(t, []config.UsenetProvider{s.Provider("p", 1)}, Options{})
	const limit = 512 << 20
	var mu sync.Mutex
	var used int64
	ctx := core.WithBackgroundBudget(context.Background(), func(_ context.Context, resource, _ string, n int64) error {
		mu.Lock()
		defer mu.Unlock()
		if resource == "bytes" && n > 0 && used > limit-n {
			return core.ErrBudgetExhausted
		}
		used += n
		return nil
	})
	for i := range 6 {
		id := fmt.Sprintf("a%d@x", i)
		if err := f.CheckBodyPart(ctx, id, nil, core.PriorityPrefetch, core.ArticlePart{}); err != nil {
			t.Fatalf("check %d: %v", i, err)
		}
		if _, _, err := f.BodyPart(ctx, id, nil, core.PriorityPrefetch); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
	}
	// Six full reads moved at least the decoded bytes; the six probes are
	// charged at most the worst-case remainder of the part they abandoned.
	if lo, hi := int64(6*len(data)), int64(6*(4*len(data)+(1<<20))); used < lo || used > hi {
		t.Fatalf("allowance used %d, want between %d and %d", used, lo, hi)
	}
}
