package debrid

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestMediaCapWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		record := new(capWaitRecord)
		ctx := context.WithValue(t.Context(), capWaitKey{}, record)
		began := time.Now()
		cap := newConnCap(1)
		gate := newHostGate(cap)
		release, err := gate.acquire(t.Context(), "cdn", func() bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		admitted := make(chan func(), 1)
		go func() {
			r, err := gate.acquire(ctx, "cdn", func() bool { return true })
			if err != nil {
				panic(err)
			}
			admitted <- r
		}()
		synctest.Wait()
		time.Sleep(37 * time.Millisecond)
		release()
		(<-admitted)()
		if got := record.since(began); got != 37*time.Millisecond {
			t.Fatalf("host admission %v", got)
		}
		// Real physical cap admission is recorded separately from the request gate.
		if err := cap.acquire(t.Context(), "cdn"); err != nil {
			t.Fatal(err)
		}
		dialed := make(chan error, 1)
		go func() { dialed <- cap.acquire(ctx, "cdn") }()
		synctest.Wait()
		time.Sleep(17 * time.Millisecond)
		cap.release("cdn")
		if err := <-dialed; err != nil {
			t.Fatal(err)
		}
		cap.release("cdn")
		noteCapReadStarted(ctx)
		if got := record.since(began); got != 54*time.Millisecond {
			t.Fatalf("combined admission %v", got)
		}
		immediate := new(capWaitRecord)
		r, err := gate.acquire(context.WithValue(t.Context(), capWaitKey{}, immediate), "cdn", func() bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		r()
		if len(immediate.intervals) != 0 {
			t.Fatal("uncontended admission reported a wait")
		}
	})

	// A joined request cannot inherit admission paid before it arrived, and
	// simultaneous hedge admissions count elapsed time once.
	at := time.Unix(1000, 0)
	record := &capWaitRecord{intervals: []capInterval{
		{at, at.Add(10 * time.Millisecond)},
		{at.Add(20 * time.Millisecond), at.Add(50 * time.Millisecond)},
		{at.Add(30 * time.Millisecond), at.Add(60 * time.Millisecond)},
	}}
	if got := record.since(at.Add(25 * time.Millisecond)); got != 35*time.Millisecond {
		t.Fatalf("joined cap wait %v", got)
	}
	measurement := new(core.MediaCapWait)
	ctx := core.WithMediaCapWait(context.Background(), measurement)
	if !core.MediaCapWaitFrom(ctx).BeginRead() {
		t.Fatal("initial read not claimed")
	}
	if measurement.BeginRead() {
		t.Fatal("later body read replaced initial read")
	}
	if ms, known := measurement.Milliseconds(); ms != 0 || !known {
		t.Fatal("cache read not zero")
	}
	measurement.FirstUpstream(record.since(at.Add(25 * time.Millisecond)))
	if ms, known := measurement.Milliseconds(); ms != 35 || !known {
		t.Fatalf("measurement %d %t", ms, known)
	}
}
