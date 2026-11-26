package collections

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
)

func mkRule(field, op string, v any) core.CollectionRule {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("collections: rule value: %v", err))
	}
	return core.CollectionRule{Field: field, Op: op, Value: b}
}

func ratingExceeds(rating, max string) bool { return contentrating.EnforceExceeds(rating, max) }

// refreshInterval is how often a scheduled collection is rebuilt.
func refreshInterval(r string) time.Duration {
	if d, ok := pseRefreshEvery(r); ok {
		return d
	}
	switch r {
	case RefreshHourly:
		return time.Hour
	case RefreshWeekly:
		return 7 * 24 * time.Hour
	case RefreshLive:
		return 0
	}
	return 24 * time.Hour
}
