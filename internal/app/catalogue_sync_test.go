package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestCatalogueSyncJobsCoalesceAndReportFailure(t *testing.T) {
	var jobs catalogueSyncJobs
	var calls atomic.Int32
	entered, finish := make(chan struct{}), make(chan struct{})
	work := func(ctx context.Context) error {
		calls.Add(1)
		close(entered)
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) < 19*time.Minute {
			t.Error("job still uses request timeout")
		}
		<-finish
		return errors.New("upstream unavailable")
	}
	first := jobs.start("one", work)
	<-entered
	second := jobs.start("one", work)
	if !first.Running || second != first || calls.Load() != 1 {
		t.Fatal("duplicate job")
	}
	close(finish)
	deadline := time.Now().Add(time.Second)
	for jobs.status("one").Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	done := jobs.status("one")
	if done.Running || done.Error != "upstream unavailable" || done.Finished == 0 {
		t.Fatalf("status=%+v", done)
	}
}

func TestCatalogueSyncTimeout(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		seconds int
	}{{"", 180}, {"bad", 180}, {"29", 180}, {"901", 180}, {"60", 60}, {"900", 900}} {
		t.Setenv("PSE_CATALOGUE_SYNC_TIMEOUT", tc.raw)
		if got := catalogueSyncTimeout(); got != time.Duration(tc.seconds)*time.Second {
			t.Errorf("%q: %s", tc.raw, got)
		}
	}
}
