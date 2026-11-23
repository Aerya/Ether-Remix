package wasm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/fastschema/qjs"
)

func idleJSMemBytes(t testing.TB, p *pool) uint32 {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.idle) != 1 {
		t.Fatalf("expected 1 idle instance, got %d", len(p.idle))
	}
	in, ok := p.idle[0].(*jsInstance)
	if !ok {
		t.Fatalf("expected *jsInstance, got %T", p.idle[0])
	}
	return in.rt.Mem().Size()
}

type staticKVHost struct {
	val json.RawMessage
}

func (h *staticKVHost) CallHost(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
	switch name {
	case "kv.get":
		return h.val, nil
	case "kv.put", "log":
		return json.RawMessage("null"), nil
	default:
		return nil, Forbidden("not granted: " + name)
	}
}

func TestJSStringBridgeMemoryBounded(t *testing.T) {
	const payloadSize = 1 << 20 // 1 MiB
	bigStr := strings.Repeat("a", payloadSize)
	bigJSON, err := json.Marshal(bigStr)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("host_result", func(t *testing.T) {
		const src = `
globalThis.handle = function() {
  let total = 0;
  for (let i = 0; i < 10; i++) {
    total += ether.kv.get("big").length;
  }
  return { status: 200, body: { total } };
};`
		e := testEngine(t)
		p, err := e.LoadJS("test.leak.host-result", "1.0.0", src, &staticKVHost{val: bigJSON}, Limits{
			MemoryBytes: 128 << 20,
			PoolSize:    1,
			UnitBudget:  1_000_000,
			Deadline:    30 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()

		var sizes [4]uint32
		for pass := range sizes {
			for call := 0; call < 2; call++ {
				resp, err := p.Call(context.Background(), Request{Method: "GET", Path: "/get"})
				if err != nil {
					t.Fatalf("pass %d call %d: %v", pass, call, err)
				}
				var got struct{ Total int }
				if err := json.Unmarshal(resp.Body, &got); err != nil || got.Total != 10*payloadSize {
					t.Fatalf("pass %d call %d: body=%s err=%v", pass, call, resp.Body, err)
				}
			}
			sizes[pass] = idleJSMemBytes(t, p.svc)
		}
		t.Logf("host_result guest linear memory across 4 passes (20 MiB/pass): %v bytes", sizes)
		if sizes[3] > sizes[1] {
			t.Fatalf("guest linear memory kept growing across passes: %v", sizes)
		}
	})

	t.Run("response_body", func(t *testing.T) {
		src := fmt.Sprintf(`
const body = "b".repeat(%d);
globalThis.handle = function() {
  return { status: 200, body };
};`, payloadSize)
		e := testEngine(t)
		p, err := e.LoadJS("test.leak.resp", "1.0.0", src, nil, Limits{
			MemoryBytes: 128 << 20,
			PoolSize:    1,
			UnitBudget:  1_000_000,
			Deadline:    30 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()

		var sizes [4]uint32
		for pass := range sizes {
			for call := 0; call < 5; call++ {
				resp, err := p.Call(context.Background(), Request{Method: "GET", Path: "/resp"})
				if err != nil {
					t.Fatalf("pass %d call %d: %v", pass, call, err)
				}
				if len(resp.Body) != payloadSize+2 {
					t.Fatalf("pass %d call %d: len(body)=%d want %d", pass, call, len(resp.Body), payloadSize+2)
				}
			}
			sizes[pass] = idleJSMemBytes(t, p.svc)
		}
		t.Logf("response_body guest linear memory across 4 passes (5 MiB/pass): %v bytes", sizes)
		if sizes[3] > sizes[1] {
			t.Fatalf("guest linear memory kept growing across passes: %v", sizes)
		}
	})

	t.Run("request_body", func(t *testing.T) {
		const src = `
globalThis.handle = function(req) {
  return { status: 200, body: { len: req.body.length } };
};`
		e := testEngine(t)
		p, err := e.LoadJS("test.leak.req", "1.0.0", src, nil, Limits{
			MemoryBytes: 128 << 20,
			PoolSize:    1,
			UnitBudget:  1_000_000,
			Deadline:    30 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()

		req := Request{Method: "POST", Path: "/req", Body: bigJSON}
		var sizes [4]uint32
		for pass := range sizes {
			for call := 0; call < 5; call++ {
				resp, err := p.Call(context.Background(), req)
				if err != nil {
					t.Fatalf("pass %d call %d: %v", pass, call, err)
				}
				var got struct{ Len int }
				if err := json.Unmarshal(resp.Body, &got); err != nil || got.Len != payloadSize {
					t.Fatalf("pass %d call %d: body=%s err=%v", pass, call, resp.Body, err)
				}
			}
			sizes[pass] = idleJSMemBytes(t, p.svc)
		}
		t.Logf("request_body guest linear memory across 4 passes (5 MiB/pass): %v bytes", sizes)
		if sizes[3] > sizes[1] {
			t.Fatalf("guest linear memory kept growing across passes: %v", sizes)
		}
	})

	t.Run("host_args", func(t *testing.T) {
		src := fmt.Sprintf(`
const payload = "c".repeat(%d);
globalThis.handle = function() {
  ether.kv.put("big", payload);
  return { status: 200, body: { ok: true } };
};`, payloadSize)
		e := testEngine(t)
		p, err := e.LoadJS("test.leak.host-args", "1.0.0", src, &staticKVHost{}, Limits{
			MemoryBytes: 128 << 20,
			PoolSize:    1,
			UnitBudget:  1_000_000,
			Deadline:    30 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()

		var sizes [4]uint32
		for pass := range sizes {
			for call := 0; call < 5; call++ {
				if _, err := p.Call(context.Background(), Request{Method: "POST", Path: "/put"}); err != nil {
					t.Fatalf("pass %d call %d: %v", pass, call, err)
				}
			}
			sizes[pass] = idleJSMemBytes(t, p.svc)
		}
		t.Logf("host_args guest linear memory across 4 passes (5 MiB/pass): %v bytes", sizes)
		if sizes[3] > sizes[1] {
			t.Fatalf("guest linear memory kept growing across passes: %v", sizes)
		}
	})

	t.Run("async_utf8", func(t *testing.T) {
		// Multibyte UTF-8 forces QuickJS wide (16-bit) JSStrings so JS_ToCString
		// allocates a separate UTF-8 copy on every Value.String() call.
		wideStr := strings.Repeat("é日本語😀", 32*1024)
		wideJSON, err := json.Marshal(wideStr)
		if err != nil {
			t.Fatal(err)
		}
		const src = `
globalThis.handle = async function(req) {
  const fromHost = ether.kv.get("wide");
  if (fromHost !== req.body) throw new Error("utf8 mismatch");
  return { status: 200, body: fromHost };
};`
		e := testEngine(t)
		p, err := e.LoadJS("test.leak.async-utf8", "1.0.0", src, &staticKVHost{val: wideJSON}, Limits{
			MemoryBytes: 128 << 20,
			PoolSize:    1,
			UnitBudget:  1_000_000,
			Deadline:    30 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()

		req := Request{Method: "POST", Path: "/utf8", Body: wideJSON}
		var sizes [4]uint32
		for pass := range sizes {
			for call := 0; call < 4; call++ {
				resp, err := p.Call(context.Background(), req)
				if err != nil {
					t.Fatalf("pass %d call %d: %v", pass, call, err)
				}
				var got string
				if err := json.Unmarshal(resp.Body, &got); err != nil || got != wideStr {
					t.Fatalf("pass %d call %d: utf8 roundtrip mismatch (err=%v len=%d want %d)", pass, call, err, len(got), len(wideStr))
				}
			}
			sizes[pass] = idleJSMemBytes(t, p.svc)
		}
		t.Logf("async_utf8 guest linear memory across 4 passes: %v bytes", sizes)
		if sizes[3] > sizes[1] {
			t.Fatalf("async_utf8 guest linear memory kept growing across passes: %v", sizes)
		}
	})

	t.Run("hot_batches", func(t *testing.T) {
		const src = `
globalThis.rank_key = function(c) { return c.n; };
globalThis.cond_match = function(c, op, v) { return op === "gte" && c.n >= v; };`
		e := testEngine(t)
		lim := roomyHot()
		lim.MemoryBytes = 128 << 20
		lim.PoolSize = 1
		p, err := e.LoadJS("test.leak.hot", "1.0.0", src, nil, lim)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()

		pad := strings.Repeat("z", 2048)
		makeCands := func(prefix string) []Candidate {
			cands := make([]Candidate, 100)
			for i := range cands {
				cands[i] = Candidate{
					ReleaseID: fmt.Sprintf("%s-%d", prefix, i),
					Facts:     json.RawMessage(fmt.Sprintf(`{"n":%d,"pad":%q}`, i, pad)),
				}
			}
			return cands
		}

		var sizes [4]uint32
		for pass := range sizes {
			for iter := 0; iter < 15; iter++ {
				cands := makeCands(fmt.Sprintf("p%d-i%d", pass, iter))
				scores, out, err := p.RankBatch(context.Background(), cands, json.RawMessage(`{"w":1}`))
				if err != nil || out.SlowCandidates != 0 || len(scores) != len(cands) {
					t.Fatalf("rank pass %d iter %d: out=%+v err=%v", pass, iter, out, err)
				}
				matches, out, err := p.CondBatch(context.Background(), cands, "gte", json.RawMessage(`50`))
				if err != nil || out.SlowCandidates != 0 || len(matches) != len(cands) {
					t.Fatalf("cond pass %d iter %d: out=%+v err=%v", pass, iter, out, err)
				}
			}
			sizes[pass] = idleJSMemBytes(t, p.hot)
		}
		t.Logf("hot_batches guest linear memory across 4 passes (~6 MiB/pass): %v bytes", sizes)
		if sizes[3] > sizes[1] {
			t.Fatalf("hot guest linear memory kept growing across passes: %v", sizes)
		}
	})
}

func TestQJSStringHelpersNoLeakOrDoubleFree(t *testing.T) {
	rt, err := qjs.New(qjs.Option{
		Context:     context.Background(),
		MemoryLimit: 64 << 20,
		Stdout:      io.Discard,
		Stderr:      io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	qc := rt.Context()

	big := strings.Repeat("x", 256<<10) // 256 KiB
	bigJSON, err := json.Marshal(map[string]string{"v": big})
	if err != nil {
		t.Fatal(err)
	}
	bigEval := fmt.Sprintf("(%q).length", big)

	var sizes [4]uint32
	for pass := range sizes {
		for i := 0; i < 16; i++ {
			s := qc.NewString(big)
			if got := s.String(); len(got) != len(big) {
				t.Fatalf("NewString/String roundtrip len=%d want %d", len(got), len(big))
			}
			s.Free()

			empty := qc.NewString("")
			if got := empty.String(); got != "" {
				t.Fatalf("empty roundtrip = %q", got)
			}
			empty.Free()

			parsed := qc.ParseJSON(string(bigJSON))
			if qc.HasException() {
				t.Fatalf("ParseJSON exception: %v", qc.Exception())
			}
			strVal := parsed.GetPropertyStr("v")
			if got := strVal.String(); len(got) != len(big) {
				t.Fatalf("ParseJSON/GetPropertyStr len=%d want %d", len(got), len(big))
			}
			strVal.Free()
			parsed.Free()

			atom := qc.NewAtom(big)
			if got := atom.String(); len(got) != len(big) {
				t.Fatalf("Atom.String len=%d want %d", len(got), len(big))
			}
			atom.Free()

			evalVal, err := qc.Eval("big.js", qjs.Code(bigEval))
			if err != nil || evalVal.Int64() != int64(len(big)) {
				t.Fatalf("Eval big: val=%v err=%v", evalVal, err)
			}
			evalVal.Free()

			// Exercise SetPropertyStr (large key) + JSONStringify (large value;
			// QJS_JSONStringify frees its C string internally, so this must
			// neither leak nor double-free).
			obj := qc.NewObject()
			obj.SetPropertyStr(big, qc.NewString(big))
			jsStr, err := obj.JSONStringify()
			if err != nil || len(jsStr) != 2*len(big)+7 {
				t.Fatalf("JSONStringify: len=%d err=%v", len(jsStr), err)
			}
			obj.Free()
		}
		sizes[pass] = rt.Mem().Size()
	}
	t.Logf("qjs direct helpers linear memory across 4 passes: %v bytes", sizes)
	if sizes[3] > sizes[1] {
		t.Fatalf("qjs direct helpers linear memory kept growing: %v", sizes)
	}
}

func BenchmarkLargeStringBridgeJS(b *testing.B) {
	const payloadSize = 1 << 20 // 1 MiB
	bigJSON, err := json.Marshal(strings.Repeat("a", payloadSize))
	if err != nil {
		b.Fatal(err)
	}
	const src = `
globalThis.handle = function(req) {
  const v = ether.kv.get("big");
  return { status: 200, body: v };
};`
	e := benchEngine(b)
	p, err := e.LoadJS("bench.strings", "1.0.0", src, &staticKVHost{val: bigJSON}, Limits{
		MemoryBytes: 512 << 20,
		PoolSize:    1,
		UnitBudget:  1_000_000,
		Deadline:    30 * time.Second,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer p.Close()
	req := Request{Method: "POST", Path: "/echo", Body: bigJSON}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := p.Call(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
		if len(resp.Body) != payloadSize+2 {
			b.Fatalf("len(body)=%d want %d", len(resp.Body), payloadSize+2)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(idleJSMemBytes(b, p.svc)), "guest-mem-B")
}
