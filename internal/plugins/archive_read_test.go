package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

func TestArchiveReadBatchAndLimits(t *testing.T) {
	ctx := context.Background()

	t.Run("60-member ZIP batch reads match archive.open and enforce boundaries", func(t *testing.T) {
		var archiveBuf bytes.Buffer
		zw := zip.NewWriter(&archiveBuf)
		const memberCount = 60
		wantBodies := make([][]byte, memberCount)
		for i := 0; i < memberCount; i++ {
			method := zip.Deflate
			if i%3 == 0 {
				method = zip.Store
			}
			body := make([]byte, 1024+i*137)
			for j := range body {
				body[j] = byte((i*53 + j*19 + 7) & 0xff)
			}
			wantBodies[i] = body
			w, err := zw.CreateHeader(&zip.FileHeader{
				Name:   fmt.Sprintf("sets/entry-%03d.dat", i),
				Method: method,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write(body); err != nil {
				t.Fatal(err)
			}
		}
		// Member 60: directory; Member 61: unsafe traversal path.
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: "sets/subdir/", Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
		unsafeW, err := zw.CreateHeader(&zip.FileHeader{Name: "../outside.dat", Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = unsafeW.Write([]byte("unsafe")); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}

		h := &dataTestHost{data: archiveBuf.Bytes()}

		// Read all 60 regular members in 3 batches of 20 and compare with archive.open.
		for batchStart := 0; batchStart < memberCount; batchStart += 20 {
			reads := make([]ArchiveReadItem, 0, 20)
			for m := batchStart; m < batchStart+20; m++ {
				reads = append(reads, ArchiveReadItem{
					Member: m,
					Offset: int64(m * 3),
					Length: int64(len(wantBodies[m]) - m*3),
				})
			}
			batchArgs, _ := json.Marshal([]any{"blob:test", reads})
			rawBatch, err := CallDataHost(ctx, h, "archive.read", batchArgs)
			if err != nil {
				t.Fatalf("batch %d: %v", batchStart, err)
			}
			var gotBatch []ArchiveReadResult
			if err := json.Unmarshal(rawBatch, &gotBatch); err != nil {
				t.Fatal(err)
			}
			if len(gotBatch) != len(reads) {
				t.Fatalf("got %d results, want %d", len(gotBatch), len(reads))
			}
			for idx, item := range reads {
				openArgs, _ := json.Marshal([]any{"blob:test", item.Member, DataRange{Offset: item.Offset, Length: item.Length}})
				rawOpen, err := CallDataHost(ctx, h, "archive.open", openArgs)
				if err != nil {
					t.Fatalf("archive.open(%d): %v", item.Member, err)
				}
				var openOut struct {
					Size       int    `json:"size"`
					BodyBase64 string `json:"bodyBase64"`
				}
				if err := json.Unmarshal(rawOpen, &openOut); err != nil {
					t.Fatal(err)
				}
				res := gotBatch[idx]
				if res.Index != idx || res.Member != item.Member || res.Size != openOut.Size || res.BodyBase64 != openOut.BodyBase64 {
					t.Fatalf("mismatch at batch idx %d (member %d): got (%d,%d,%d), want (%d,%d,%d)",
						idx, item.Member, res.Index, res.Member, res.Size, idx, item.Member, openOut.Size)
				}
				decoded, err := base64.StdEncoding.DecodeString(res.BodyBase64)
				if err != nil || !bytes.Equal(decoded, wantBodies[item.Member][item.Offset:]) {
					t.Fatalf("decoded bytes mismatch for member %d", item.Member)
				}
			}
		}
		if h.opens != h.closes {
			t.Fatalf("leaked readers after batches: %d opens vs %d closes", h.opens, h.closes)
		}

		// Pre-open validation: empty reads, >4096 items, and sum-over-readBytes fail before CacheOpen.
		beforeOpens := h.opens
		emptyArgs, _ := json.Marshal([]any{"blob:test", []ArchiveReadItem{}})
		if _, err := CallDataHost(ctx, h, "archive.read", emptyArgs); err == nil {
			t.Fatal("empty reads accepted")
		}
		tooMany := make([]ArchiveReadItem, MaxArchiveReadItems+1)
		for i := range tooMany {
			tooMany[i] = ArchiveReadItem{Member: 0, Offset: 0, Length: 1}
		}
		tooManyArgs, _ := json.Marshal([]any{"blob:test", tooMany})
		if _, err := CallDataHost(ctx, h, "archive.read", tooManyArgs); err == nil {
			t.Fatal(">4096 reads accepted")
		}
		overBudgetReads := []ArchiveReadItem{
			{Member: 0, Offset: 0, Length: 5 << 20},
			{Member: 1, Offset: 0, Length: (3 << 20) + 1},
		}
		overBudgetArgs, _ := json.Marshal([]any{"blob:test", overBudgetReads})
		if _, err := CallDataHost(ctx, h, "archive.read", overBudgetArgs); err == nil {
			t.Fatal("sum over 8 MiB default readBytes accepted")
		}
		if h.opens != beforeOpens {
			t.Fatalf("invalid archive.read opened cache (%d -> %d)", beforeOpens, h.opens)
		}

		// Directory, out-of-range member, and offset past end fail cleanly and close handles;
		// flagged non-directory members are readable by index without failing the batch.
		dirArgs, _ := json.Marshal([]any{"blob:test", []ArchiveReadItem{{Member: 60, Offset: 0, Length: 1}}})
		if _, err := CallDataHost(ctx, h, "archive.read", dirArgs); err == nil {
			t.Fatal("directory member accepted")
		}
		unsafeArgs, _ := json.Marshal([]any{"blob:test", []ArchiveReadItem{{Member: 0, Offset: 0, Length: 16}, {Member: 61, Offset: 0, Length: 6}}})
		rawUnsafeBatch, err := CallDataHost(ctx, h, "archive.read", unsafeArgs)
		if err != nil {
			t.Fatalf("batch with flagged member failed: %v", err)
		}
		var unsafeBatch []ArchiveReadResult
		if err := json.Unmarshal(rawUnsafeBatch, &unsafeBatch); err != nil || len(unsafeBatch) != 2 {
			t.Fatalf("batch with flagged member results = %s, %v", rawUnsafeBatch, err)
		}
		gotUnsafe, err := base64.StdEncoding.DecodeString(unsafeBatch[1].BodyBase64)
		if err != nil || string(gotUnsafe) != "unsafe" {
			t.Fatalf("flagged member body = %q, %v", gotUnsafe, err)
		}
		badIdxArgs, _ := json.Marshal([]any{"blob:test", []ArchiveReadItem{{Member: 999, Offset: 0, Length: 1}}})
		if _, err := CallDataHost(ctx, h, "archive.read", badIdxArgs); err == nil {
			t.Fatal("out-of-range member index accepted")
		}
		badOffArgs, _ := json.Marshal([]any{"blob:test", []ArchiveReadItem{{Member: 0, Offset: int64(len(wantBodies[0]) + 1), Length: 1}}})
		if _, err := CallDataHost(ctx, h, "archive.read", badOffArgs); err == nil {
			t.Fatal("offset past member size accepted")
		}
		if h.opens != h.closes {
			t.Fatalf("leaked readers after error cases: %d opens vs %d closes", h.opens, h.closes)
		}
	})

	t.Run("readBytes scales with memoryBytes and widens hosted WASM envelope", func(t *testing.T) {
		if got := DataReadBytes(0); got != 8<<20 {
			t.Fatalf("default DataReadBytes(0) = %d, want %d", got, 8<<20)
		}
		if got := DataReadBytes(32 << 20); got != 8<<20 {
			t.Fatalf("DataReadBytes(32 MiB) = %d, want %d", got, 8<<20)
		}
		if got := DataReadBytes(1 << 30); got != 256<<20 {
			t.Fatalf("DataReadBytes(1 GiB) = %d, want %d", got, 256<<20)
		}
		if got := DataReadBytes(4 << 30); got != 512<<20 {
			t.Fatalf("DataReadBytes(4 GiB) = %d, want %d", got, 512<<20)
		}

		env := newTestEnv(t)
		rec := installContent(t, env)
		libKey := core.PluginLibraryKey(rec.ID, "recording")

		blobs, err := blobcache.Open(t.TempDir(), blobcache.Options{SweepPeriod: -1})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = blobs.Close() })
		cachePut, cacheOpen, cacheDelete := blobs.HostBackends(func(_, _ string) blobcache.Quota {
			return blobcache.Quota{MaxBytes: 512 << 20}
		})
		env.m.SetKindBackends(KindBackends{CachePut: cachePut, CacheOpen: cacheOpen, CacheDelete: cacheDelete})

		h := env.m.HostFor(rec.ID).(*host)
		rawLimits, err := CallDataHost(ctx, h, "runtime.limits", json.RawMessage("[]"))
		if err != nil {
			t.Fatal(err)
		}
		var defaultLim RuntimeLimits
		if err := json.Unmarshal(rawLimits, &defaultLim); err != nil {
			t.Fatal(err)
		}
		if defaultLim.MemoryBytes != 32<<20 || defaultLim.ReadBytes != 8<<20 {
			t.Fatalf("default runtime.limits = %+v, want 32 MiB / 8 MiB", defaultLim)
		}

		// Verify 1 GiB manifest request yields 256 MiB readBytes.
		bigMan := rec.Manifest
		bigMan.Ether.Runtime.Limits = &RuntimeLimits{MemoryBytes: 1 << 30}
		bigLim := env.m.grantedRuntimeLimits(bigMan)
		if bigLim == nil || bigLim.ReadBytes != 256<<20 {
			t.Fatalf("1 GiB manifest grantedRuntimeLimits = %+v, want readBytes=256 MiB", bigLim)
		}

		// Grant 128 MiB memory (readBytes = 32 MiB) via runtime config + manifest and read 6 MiB in one hosted WASM call
		// (exceeding both the old 1 MiB range cap and the old 4 MiB WASM host-result cap).
		env.m.deps.ReadRuntimeConfig = func() config.Plugins {
			return config.Plugins{
				RuntimeOverrides: map[string]config.PluginRuntimeLimits{
					rec.ID: {MemoryBytes: 256 << 20},
				},
			}
		}
		manUpdated := rec.Manifest
		manUpdated.Version = "1.0.1"
		manUpdated.Ether.Runtime.Limits = &RuntimeLimits{MemoryBytes: 256 << 20}
		rawMan, err := json.Marshal(manUpdated)
		if err != nil {
			t.Fatal(err)
		}
		prev, err := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: rawMan, Files: map[string][]byte{"dist/plugin.js": []byte("// library")}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.m.Install(ctx, prev, InstallOptions{AcceptAddedPermissions: true}); err != nil {
			t.Fatal(err)
		}

		sixMiB := bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef, 0x42, 0x99, 0x11, 0x77}, (6<<20)/8)
		var bigZip bytes.Buffer
		zw := zip.NewWriter(&bigZip)
		for idx := 0; idx < 2; idx++ {
			w, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("big-%d.bin", idx), Method: zip.Deflate})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write(sixMiB); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		archiveRef, err := h.CachePut(ctx, libKey, "big.zip", bytes.NewReader(bigZip.Bytes()))
		if err != nil {
			t.Fatal(err)
		}

		hostTS, err := os.ReadFile("../../sdk/typescript/src/host.ts")
		if err != nil {
			t.Fatal(err)
		}
		typesTS, err := os.ReadFile("../../sdk/typescript/src/types.ts")
		if err != nil {
			t.Fatal(err)
		}
		kitTS, err := os.ReadFile("../../sdk/typescript/src/kit.ts")
		if err != nil {
			t.Fatal(err)
		}
		guestSrc := `
import { createHostedClient } from "./host.ts";
const client = createHostedClient();
export function handle(req: any) {
  const lim = client.runtime!.limits() as any;
  const single = client.archive!.open(req.body.blob, 0, { offset: 0, length: 6 * 1024 * 1024 }) as Uint8Array;
  const batch = client.archive!.read(req.body.blob, [
    { member: 0, offset: 0, length: 5 * 1024 * 1024 },
    { member: 1, offset: 1024 * 1024, length: 5 * 1024 * 1024 },
  ]) as Array<{ index: number; member: number; size: number; body: Uint8Array }>;
  return {
    status: 200,
    body: {
      readBytes: lim.readBytes,
      singleLen: single.length,
      firstByte: single[0],
      lastByte: single[single.length - 1],
      batchSizes: batch.map((x) => x.body.length),
    },
  };
}
`
		jsBytes, _, diags := wasm.Compile(map[string]string{
			"index.ts": guestSrc,
			"host.ts":  string(hostTS),
			"types.ts": string(typesTS),
			"kit.ts":   string(kitTS),
		}, "index.ts")
		if err := diagnosticsError(diags); err != nil {
			t.Fatal(err)
		}
		eng := wasm.NewEngine(nil)
		t.Cleanup(func() { _ = eng.Close() })
		plug, err := eng.LoadJS(rec.ID, "1.0.0", string(jsBytes), hostBridge{h: h}, wasm.Limits{MemoryBytes: 256 << 20, UnitBudget: 1_000_000})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = plug.Close() })

		reqBody, _ := json.Marshal(map[string]any{"blob": archiveRef})
		resp, outcome, err := plug.CallDetailed(ctx, wasm.Request{Method: "POST", Path: "/ether/job/run", Body: reqBody})
		if err != nil {
			t.Fatalf("hosted big read failed: %v", err)
		}
		var out struct {
			ReadBytes  int64 `json:"readBytes"`
			SingleLen  int   `json:"singleLen"`
			FirstByte  int   `json:"firstByte"`
			LastByte   int   `json:"lastByte"`
			BatchSizes []int `json:"batchSizes"`
		}
		if err := json.Unmarshal(resp.Body, &out); err != nil {
			t.Fatalf("unmarshal guest response %s: %v", resp.Body, err)
		}
		if out.ReadBytes != 64<<20 {
			t.Fatalf("guest saw readBytes=%d, want %d", out.ReadBytes, 64<<20)
		}
		if out.SingleLen != 6<<20 || out.FirstByte != 0xde || out.LastByte != 0x77 {
			t.Fatalf("unexpected single 6 MiB result: %+v", out)
		}
		if len(out.BatchSizes) != 2 || out.BatchSizes[0] != 5<<20 || out.BatchSizes[1] != 5<<20 {
			t.Fatalf("unexpected 10 MiB batch result: %+v", out)
		}
		// Host units: runtime.limits (1) + archive.open (25) + archive.read (50 + 2*2 = 54) = 80 (+ wall-time units).
		if outcome.Units < 80 {
			t.Fatalf("units = %d, want >= 80", outcome.Units)
		}
		// Budget enforcement: 3-item archive.read needs 50 + 3*2 = 56 host units; UnitBudget=55 must fail.
		tightJS, _, tDiags := wasm.Compile(map[string]string{"index.ts": `export function handle(req: any) { return { status: 200, body: (globalThis as any).ether.archive.read(req.body.blob, [{member:0,offset:0,length:8},{member:0,offset:0,length:8},{member:0,offset:0,length:8}]) }; }`}, "index.ts")
		if err := diagnosticsError(tDiags); err != nil {
			t.Fatal(err)
		}
		tightPlug, err := eng.LoadJS(rec.ID, "1.0.1", string(tightJS), hostBridge{h: h}, wasm.Limits{MemoryBytes: 64 << 20, UnitBudget: 55})
		if err != nil {
			t.Fatal(err)
		}
		defer tightPlug.Close()
		if _, _, err := tightPlug.CallDetailed(ctx, wasm.Request{Method: "POST", Path: "/ether/job/run", Body: reqBody}); err == nil {
			t.Fatal("expected unit budget exhaustion when UnitBudget=55 < 50+3*2")
		}
	})
}
