package newznab

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestRetainedCandidatesAvoidFanoutAndRescueBypassesThem(t *testing.T) {
	m := NewManager(nil, nil, Options{})
	calls := 0
	m.SetRetainedSearch(func(context.Context, core.SearchRequest) ([]core.Release, bool) {
		calls++
		return []core.Release{{ID: "retained"}}, true
	})
	arrived := 0
	rs, err := m.SearchWith(t.Context(), core.SearchRequest{}, func(rs []core.Release) { arrived += len(rs) })
	if err != nil || len(rs) != 1 || arrived != 1 || calls != 1 {
		t.Fatal(rs, err, arrived, calls)
	}
	rs, err = m.SearchRescue(t.Context(), core.SearchRequest{})
	if err != nil || len(rs) != 0 || calls != 1 {
		t.Fatal("rescue reused rejected observation", rs, err, calls)
	}
}
