package vela

import (
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

func TestIndexerThrottledAnswer(t *testing.T) {
	w := httptest.NewRecorder()
	fail(w, fmt.Errorf("search: %w", &frontend.IndexerThrottledError{Indexers: []string{"Nzb A"}, RetryAt: time.Now().Add(15 * time.Minute), Err: frontend.ErrBudgetExhausted}))
	seconds, _ := strconv.Atoi(w.Header().Get("Retry-After"))
	if w.Code != 429 || seconds < 899 || seconds > 900 || !strings.Contains(w.Body.String(), `"code":"indexer_throttled"`) {
		t.Fatal("throttle answer", w.Code, w.Header(), w.Body.String())
	}
	w = httptest.NewRecorder()
	fail(w, &frontend.IndexerThrottledError{Indexers: []string{"Nzb A"}, Err: frontend.ErrBudgetExhausted})
	if w.Code != 429 || w.Header().Get("Retry-After") != "" || !strings.Contains(w.Body.String(), "indexer_throttled") {
		t.Fatal("unknown resume time", w.Code, w.Header(), w.Body.String())
	}
	w = httptest.NewRecorder()
	fail(w, frontend.ErrBudgetExhausted)
	if w.Code != 429 || w.Header().Get("Retry-After") != "" || !strings.Contains(w.Body.String(), "indexer_budget_exhausted") {
		t.Fatal("daily budget answer changed", w.Code, w.Body.String())
	}
}
