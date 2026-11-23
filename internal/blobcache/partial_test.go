package blobcache

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/segcache"
)

func TestPartialStaysUnreadableUntilCommitAndResumesAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	payload := bytes.Repeat([]byte("ether-resumable-blob-payload-0123456789"), 200)
	split := len(payload) / 3

	c1, err := Open(dir, Options{SweepPeriod: -1, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	ns1 := c1.Namespace("pk-test-lib", Quota{MaxBytes: 1 << 20})

	p1, err := ns1.OpenPartial(ctx, "https://example.test/archive.zip\x00archive.zip")
	if err != nil {
		t.Fatal(err)
	}
	if p1.Size() != 0 {
		t.Fatalf("initial partial size = %d, want 0", p1.Size())
	}
	if err := p1.Reset(PartialMeta{ETag: `"v1"`, ContentLength: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := p1.Write(payload[:split]); err != nil {
		t.Fatal(err)
	}
	if err := p1.Close(); err != nil {
		t.Fatal(err)
	}

	// Partial bytes count toward Stats() bytes but must never appear as a readable blob.
	if used, blobs := ns1.Stats(); used != int64(split) || blobs != 0 {
		t.Fatalf("after partial write Stats = (%d, %d), want (%d, 0)", used, blobs, split)
	}
	if _, ok := ns1.LookupKey("archive.zip"); ok {
		t.Fatal("partial blob leaked into LookupKey before Commit")
	}
	partialHash := sha256.Sum256(payload[:split])
	if _, err := ns1.Open(Ref("blob:" + hex.EncodeToString(partialHash[:]))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("partial blob Open err = %v, want ErrNotFound", err)
	}
	if err := c1.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen cache from disk (simulating process restart): partial survives and stays accounted.
	c2, err := Open(dir, Options{SweepPeriod: -1, Now: func() time.Time { return now.Add(time.Minute) }})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	ns2 := c2.Namespace("pk-test-lib", Quota{MaxBytes: 1 << 20})
	if used, blobs := ns2.Stats(); used != int64(split) || blobs != 0 {
		t.Fatalf("after reopen Stats = (%d, %d), want (%d, 0)", used, blobs, split)
	}

	p2, err := ns2.OpenPartial(ctx, "https://example.test/archive.zip\x00archive.zip")
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	if p2.Size() != int64(split) || p2.Meta().ETag != `"v1"` || p2.Meta().ContentLength != int64(len(payload)) {
		t.Fatalf("reopened partial = size:%d meta:%+v, want size:%d etag:\"v1\" cl:%d", p2.Size(), p2.Meta(), split, len(payload))
	}
	if _, err := p2.Write(payload[split:]); err != nil {
		t.Fatal(err)
	}
	res, err := p2.Commit(ctx, "archive.zip")
	if err != nil {
		t.Fatal(err)
	}

	wantSHA256 := sha256.Sum256(payload)
	wantSHA1 := sha1.Sum(payload)
	wantMD5 := md5.Sum(payload)
	wantCRC := fmt.Sprintf("%08x", crc32.ChecksumIEEE(payload))
	if res.Ref != Ref("blob:"+hex.EncodeToString(wantSHA256[:])) ||
		res.Size != int64(len(payload)) ||
		res.SHA1 != hex.EncodeToString(wantSHA1[:]) ||
		res.MD5 != hex.EncodeToString(wantMD5[:]) ||
		res.CRC32 != wantCRC {
		t.Fatalf("commit result = %+v, want sha256=%x size=%d", res, wantSHA256, len(payload))
	}
	if pBytes, pCount := ns2.PartialStats(); pBytes != 0 || pCount != 0 {
		t.Fatalf("partial stats after commit = (%d, %d), want (0, 0)", pBytes, pCount)
	}
	if used, blobs := ns2.Stats(); used != int64(len(payload)) || blobs != 1 {
		t.Fatalf("Stats after commit = (%d, %d), want (%d, 1)", used, blobs, len(payload))
	}
	src, err := ns2.Open(res.Ref)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	got, err := io.ReadAll(src)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("committed blob bytes mismatch: len=%d err=%v", len(got), err)
	}
}

func TestPartialQuotaDiskFloorEvictionAndLocking(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

	// 1. Namespace quota, shared cap, and disk free-space floor enforcement.
	const totalDisk int64 = 100 << 20                   // 100 MiB -> segcache MinFreeBytes = 5 MiB (5%)
	freeDisk := segcache.MinFreeBytes(totalDisk) + 4096 // only 4 KiB headroom above disk floor
	c, err := Open(t.TempDir(), Options{
		SweepPeriod: -1,
		Now:         func() time.Time { return now },
		DiskUsage: func() (int64, int64, error) {
			return totalDisk, freeDisk, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ns := c.Namespace("pk-quota-lib", Quota{MaxBytes: 8192})
	p, err := ns.OpenPartial(ctx, "big-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Reset(PartialMeta{ETag: `"e1"`, ContentLength: 6000}); err != nil {
		t.Fatal(err)
	}
	// Exceeding the disk free-space floor (4 KiB headroom) must fail with ErrTooLarge.
	if _, err := p.Write(bytes.Repeat([]byte("a"), 5000)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("write past disk floor err = %v, want ErrTooLarge", err)
	}
	_ = p.Discard()
	_ = p.Close()

	// Raise disk headroom and verify namespace quota (8192) still stops oversized writes.
	freeDisk = 50 << 20
	p2, err := ns.OpenPartial(ctx, "ns-over")
	if err != nil {
		t.Fatal(err)
	}
	if err := p2.Reset(PartialMeta{ETag: `"e2"`, ContentLength: 9000}); err != nil {
		t.Fatal(err)
	}
	if _, err := p2.Write(bytes.Repeat([]byte("b"), 4096)); err != nil {
		t.Fatal(err)
	}
	if _, err := p2.Write(bytes.Repeat([]byte("c"), 5000)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("write past namespace quota err = %v, want ErrTooLarge", err)
	}
	_ = p2.Discard()
	_ = p2.Close()

	// 2. Idle partials are evicted before completed blobs when sweeping under quota.
	keptRef, err := ns.Put(ctx, "kept.bin", bytes.NewReader(bytes.Repeat([]byte("k"), 5000)), 5000)
	if err != nil {
		t.Fatal(err)
	}
	idle, err := ns.OpenPartial(ctx, "idle-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := idle.Reset(PartialMeta{ETag: `"idle"`, ContentLength: 6000}); err != nil {
		t.Fatal(err)
	}
	if _, err := idle.Write(bytes.Repeat([]byte("i"), 3000)); err != nil {
		t.Fatal(err)
	}
	_ = idle.Close() // now idle (unlocked)

	// Adding another 3000-byte blob pushes total to 11000 > 8192; sweep evicts idle partial first.
	if _, err := ns.Put(ctx, "second.bin", bytes.NewReader(bytes.Repeat([]byte("s"), 3000)), 3000); err != nil {
		t.Fatal(err)
	}
	if pBytes, pCount := ns.PartialStats(); pBytes != 0 || pCount != 0 {
		t.Fatalf("idle partial not evicted under quota pressure: (%d, %d)", pBytes, pCount)
	}
	if src, err := ns.Open(keptRef); err != nil {
		t.Fatalf("kept completed blob was evicted instead of idle partial: %v", err)
	} else {
		_ = src.Close()
	}

	// 3. Concurrent OpenPartial on the same key serializes cleanly.
	first, err := ns.OpenPartial(ctx, "contended")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Reset(PartialMeta{ETag: `"lock"`, ContentLength: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte("hello-")); err != nil {
		t.Fatal(err)
	}
	secondAcquired := make(chan int64, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		second, oerr := ns.OpenPartial(ctx, "contended")
		if oerr != nil {
			secondAcquired <- -1
			return
		}
		defer second.Close()
		secondAcquired <- second.Size()
	}()
	select {
	case sz := <-secondAcquired:
		t.Fatalf("second OpenPartial acquired lock prematurely with size %d", sz)
	case <-time.After(20 * time.Millisecond):
	}
	_ = first.Close()
	if got := <-secondAcquired; got != 6 {
		t.Fatalf("second OpenPartial observed size %d, want 6", got)
	}
	wg.Wait()
}
