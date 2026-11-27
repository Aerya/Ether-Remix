package releasedb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestIndexerLoopWarnings(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		warn bool
	}{
		{"nothing to do", errNothingToDo, false},
		{"daily budget exhausted", core.ErrBudgetExhausted, false},
		{"wrapped daily budget exhausted", fmt.Errorf("source: %w", core.ErrBudgetExhausted), false},
		{"catalogue failure", errors.New("catalogue unavailable"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFreshEnv(t)
			var logs bytes.Buffer
			e.s.d.Logger = slog.New(slog.NewTextHandler(&logs, nil))
			e.s.d.Settings.Pace = time.Millisecond
			calls := make(chan struct{}, 3)
			e.s.d.Browse = func(context.Context, core.BrowseQuery) (core.Page, error) {
				select {
				case calls <- struct{}{}:
				default:
				}
				return core.Page{}, tc.err
			}
			e.s.Start()
			t.Cleanup(e.s.Close)
			timeout := time.NewTimer(5 * time.Second)
			defer timeout.Stop()
			for range 3 {
				select {
				case <-calls:
				case <-timeout.C:
					t.Fatal("indexer did not continue ticking")
				}
			}
			e.s.Close()
			// Read only after all workers stop: bytes.Buffer is not concurrent.
			if got := strings.Contains(logs.String(), "pse release indexer step failed"); got != tc.warn {
				t.Fatalf("step warning = %v, want %v; logs: %s", got, tc.warn, logs.String())
			}
		})
	}
}
