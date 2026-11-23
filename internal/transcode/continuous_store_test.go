package transcode

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Publication never waits on storage: with the DVR disk stalled, every
// segment PUT by the encoder is published and served from memory at once,
// and the history reaches the disk once it recovers.
func TestContinuousStorePublishesThroughAStalledDisk(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := t.TempDir()
	e := &continuousEncoder{continuousProcess: &continuousProcess{ctx: ctx}, dir: dir, req: EncoderRequest{Source: RawFormat{Width: 320, Height: 180, FPSNum: 30, FPSDen: 1}, SegmentDuration: 2 * time.Second, LiveSource: true}}
	index := newContinuousSegmentIndex(e)
	e.index = index
	store, err := newContinuousStore(dir, 4, index.kick)
	if err != nil {
		t.Fatal(err)
	}
	stalled := make(chan struct{})
	store.writeFile = func(name string, b []byte, mode os.FileMode) error {
		<-stalled
		return os.WriteFile(name, b, mode)
	}
	e.store = store
	go index.watch()
	index.setStart(time.Now())
	client := &http.Client{}
	for n := int64(0); n < 8; n++ {
		for _, name := range []string{index.ts(n), index.mp4(n)} {
			req, _ := http.NewRequest(http.MethodPut, store.url()+name, strings.NewReader(fmt.Sprintf("body %d", n)))
			res, err := client.Do(req)
			if err != nil || res.StatusCode != http.StatusCreated {
				t.Fatalf("PUT %s: %v %v", name, err, res)
			}
			res.Body.Close()
		}
		wait, stop := context.WithTimeout(ctx, 500*time.Millisecond)
		seg, err := index.Wait(wait, n-1)
		stop()
		if err != nil || seg.Seq != n {
			t.Fatalf("segment %d not published with the disk stalled: %v", n, err)
		}
		rec := httptest.NewRecorder()
		index.ServeTS(rec, httptest.NewRequest(http.MethodGet, "/", nil), n)
		if rec.Code != http.StatusOK || rec.Body.String() != fmt.Sprintf("body %d", n) {
			t.Fatalf("segment %d not served from memory: %d %q", n, rec.Code, rec.Body.String())
		}
	}
	close(stalled)
	waitDisk(t, dir, index.ts(7), index.mp4(7))
	store.close()
}

func TestContinuousSegmentIndexStartSeq(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dir := t.TempDir()
	const startSeq int64 = 14
	e := &continuousEncoder{
		continuousProcess: &continuousProcess{ctx: ctx},
		dir:               dir,
		req: EncoderRequest{
			Source:          RawFormat{Width: 320, Height: 180, FPSNum: 30, FPSDen: 1},
			SegmentDuration: 2 * time.Second,
			StartSeq:        startSeq,
		},
	}
	index := newContinuousSegmentIndex(e)
	e.index = index
	store, err := newContinuousStore(dir, 6, index.kick)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	e.store = store
	go index.watch()

	anchor := time.Date(2026, 11, 22, 22, 0, 30, 0, time.UTC)
	index.setStart(anchor)
	index.recordPolicy(0, []any{"p0"})
	index.recordPolicy(60, []any{"p1"})

	client := &http.Client{}
	for offset := int64(0); offset < 3; offset++ {
		seq := startSeq + offset
		for _, name := range []string{index.ts(seq), index.mp4(seq)} {
			req, _ := http.NewRequest(http.MethodPut, store.url()+name, strings.NewReader(fmt.Sprintf("seg %d", seq)))
			res, err := client.Do(req)
			if err != nil || res.StatusCode != http.StatusCreated {
				t.Fatalf("PUT %s: %v %v", name, err, res)
			}
			res.Body.Close()
		}
		after := seq - 1
		if offset == 0 {
			after = -1
		}
		wait, stop := context.WithTimeout(ctx, 500*time.Millisecond)
		seg, err := index.Wait(wait, after)
		stop()
		if err != nil {
			t.Fatalf("Wait(after=%d): %v", after, err)
		}
		if seg.Seq != seq {
			t.Fatalf("seg.Seq = %d, want %d", seg.Seq, seq)
		}
		wantStart := anchor.Add(time.Duration(offset) * 2 * time.Second)
		if !seg.Start.Equal(wantStart) || seg.Duration != 2*time.Second {
			t.Fatalf("seg %d start/dur = %v/%v, want %v/2s", seq, seg.Start, seg.Duration, wantStart)
		}
		if offset == 0 && (len(seg.PolicyTags) != 1 || seg.PolicyTags[0] != "p0") {
			t.Fatalf("seg %d policy tags = %v, want [p0]", seq, seg.PolicyTags)
		}
		if offset == 1 && (len(seg.PolicyTags) != 1 || seg.PolicyTags[0] != "p1") {
			t.Fatalf("seg %d policy tags = %v, want [p1]", seq, seg.PolicyTags)
		}
	}
}
