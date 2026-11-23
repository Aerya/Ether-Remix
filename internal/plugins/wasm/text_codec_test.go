package wasm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWhatwgDecodeUTF8Unit(t *testing.T) {
	valid := []byte("hello café € 😀 日本語")
	got, err := whatwgDecodeUTF8(valid, true)
	if err != nil {
		t.Fatalf("valid fatal err = %v", err)
	}
	if !bytes.Equal(got, valid) || &got[0] != &valid[0] {
		t.Fatalf("expected zero-copy return on valid UTF-8")
	}

	cases := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", nil, ""},
		{"nul", []byte{'a', 0x00, 'b'}, "a\x00b"},
		{"lone_continuation", []byte{0x80}, "\uFFFD"},
		{"overlong_2byte_slash", []byte{0xC0, 0xAF}, "\uFFFD\uFFFD"},
		{"c1_lead", []byte{0xC1, 0xBF}, "\uFFFD\uFFFD"},
		{"truncated_2byte_before_ascii", []byte{0xC2, 'A'}, "\uFFFDA"},
		{"overlong_3byte_e0", []byte{0xE0, 0x80, 0xAF}, "\uFFFD\uFFFD\uFFFD"},
		{"truncated_3byte_maximal_subpart", []byte{0xE2, 0x82, 'A'}, "\uFFFDA"},
		{"surrogate_ed_a0_80", []byte{0xED, 0xA0, 0x80}, "\uFFFD\uFFFD\uFFFD"},
		{"overlong_4byte_f0", []byte{0xF0, 0x80, 0x80, 0xAF}, "\uFFFD\uFFFD\uFFFD\uFFFD"},
		{"truncated_4byte_maximal_subpart", []byte{0xF0, 0x9F, 0x98, 'A'}, "\uFFFDA"},
		{"above_10ffff_f4_90", []byte{0xF4, 0x90, 0x80, 0x80}, "\uFFFD\uFFFD\uFFFD\uFFFD"},
		{"invalid_f5", []byte{0xF5, 0x80, 0x80, 0x80}, "\uFFFD\uFFFD\uFFFD\uFFFD"},
		{"trailing_incomplete_eof", []byte{'x', 0xF0, 0x9F, 0x98}, "x\uFFFD"},
	}
	for _, tc := range cases {
		out, err := whatwgDecodeUTF8(tc.in, false)
		if err != nil {
			t.Fatalf("%s: unexpected non-fatal error: %v", tc.name, err)
		}
		if string(out) != tc.want {
			t.Fatalf("%s: got %q (% x), want %q (% x)", tc.name, string(out), out, tc.want, []byte(tc.want))
		}
		if len(tc.in) > 0 && tc.name != "nul" {
			if _, ferr := whatwgDecodeUTF8(tc.in, true); ferr == nil {
				t.Fatalf("%s: expected fatal error on invalid sequence", tc.name)
			}
		}
	}
}

func TestJSTextCodecsGuestAndHotPath(t *testing.T) {
	const src = `
const bootDecoder = new TextDecoder();
const bootEncoder = new TextEncoder();

function assertEq(actual, expected, label) {
  if (actual !== expected) {
    throw new Error(label + ": got " + JSON.stringify(actual) + " want " + JSON.stringify(expected));
  }
}

function assertBytes(actual, expected, label) {
  if (!(actual instanceof Uint8Array)) {
    throw new Error(label + ": expected Uint8Array");
  }
  if (actual.length !== expected.length) {
    throw new Error(label + ": length " + actual.length + " !== " + expected.length);
  }
  for (let i = 0; i < expected.length; i++) {
    if (actual[i] !== expected[i]) {
      throw new Error(label + "[" + i + "]: " + actual[i] + " !== " + expected[i]);
    }
  }
}

function assertThrows(fn, ErrCtor, label) {
  let threw = false;
  try {
    fn();
  } catch (e) {
    threw = true;
    if (!(e instanceof ErrCtor)) {
      throw new Error(label + ": threw " + (e && e.constructor && e.constructor.name) + " (" + e + "), expected " + ErrCtor.name);
    }
  }
  if (!threw) {
    throw new Error(label + ": did not throw " + ErrCtor.name);
  }
}

globalThis.handle = function() {
  assertEq(__ether_text_decode(), "", "empty call");
  assertEq(__ether_text_decode(new ArrayBuffer(0)), "", "empty ArrayBuffer");
  assertEq(__ether_text_decode(new Uint8Array(0)), "", "empty Uint8Array");

  // Shared packed ArrayBuffer with subviews and repeated reads (must never free or corrupt live backing store).
  const prefix = bootEncoder.encode("HEADER_PADDING!");
  const target = bootEncoder.encode("café € 😀 日本語");
  const suffix = bootEncoder.encode("!TRAILER");
  const packed = new ArrayBuffer(prefix.length + target.length + suffix.length);
  const fullView = new Uint8Array(packed);
  fullView.set(prefix, 0);
  fullView.set(target, prefix.length);
  fullView.set(suffix, prefix.length + target.length);

  const subU8 = new Uint8Array(packed, prefix.length, target.length);
  const subDV = new DataView(packed, prefix.length, target.length);
  for (let iter = 0; iter < 256; iter++) {
    assertEq(__ether_text_decode(subU8), "café € 😀 日本語", "subview u8 iter " + iter);
    assertEq(__ether_text_decode(subDV), "café € 😀 日本語", "subview DataView iter " + iter);
  }
  assertEq(__ether_text_decode(packed), "HEADER_PADDING!café € 😀 日本語!TRAILER", "full ArrayBuffer");

  // Embedded NUL bytes survive both encode and decode.
  const withNul = new Uint8Array([0x61, 0x00, 0x62, 0x00, 0xe2, 0x82, 0xac]);
  const decodedNul = __ether_text_decode(withNul);
  assertEq(decodedNul.length, 5, "nul length");
  assertEq(decodedNul.charCodeAt(1), 0, "nul code unit 1");
  assertEq(decodedNul.charCodeAt(3), 0, "nul code unit 3");
  assertEq(decodedNul, "a\x00b\x00€", "nul string");
  assertBytes(bootEncoder.encode(decodedNul), [0x61, 0x00, 0x62, 0x00, 0xe2, 0x82, 0xac], "nul roundtrip");

  // WHATWG maximal-subpart invalid UTF-8 cases.
  assertEq(__ether_text_decode(new Uint8Array([0xc0, 0xaf])), "\ufffd\ufffd", "overlong 2-byte");
  assertEq(__ether_text_decode(new Uint8Array([0xe2, 0x82, 0x41])), "\ufffdA", "truncated 3-byte");
  assertEq(__ether_text_decode(new Uint8Array([0xed, 0xa0, 0x80])), "\ufffd\ufffd\ufffd", "surrogate utf-8");
  assertEq(__ether_text_decode(new Uint8Array([0xf0, 0x9f, 0x98, 0x41])), "\ufffdA", "truncated 4-byte");
  assertEq(__ether_text_decode(new Uint8Array([0xf4, 0x90, 0x80, 0x80])), "\ufffd\ufffd\ufffd\ufffd", "above U+10FFFF");
  assertEq(__ether_text_decode(new Uint8Array([0xf0, 0x9f, 0x98])), "\ufffd", "incomplete EOF");

  // TextDecoder labels, BOM handling, fatal mode, and TypeError/RangeError contracts.
  for (const label of ["utf-8", "utf8", " \tUTF-8\r\n ", "unicode-1-1-utf-8", "unicode11utf8", "unicode20utf8", "x-unicode20utf8"]) {
    const td = new TextDecoder(label);
    assertEq(td.encoding, "utf-8", "label " + label);
    assertEq(td.decode(subU8), "café € 😀 日本語", "decode label " + label);
  }
  assertThrows(() => new TextDecoder("latin1"), RangeError, "unsupported latin1");
  assertThrows(() => new TextDecoder("utf-16le"), RangeError, "unsupported utf-16le");
  assertThrows(() => __ether_text_decode("nope"), TypeError, "__ether_text_decode bad arg");
  assertThrows(() => bootDecoder.decode("nope"), TypeError, "TextDecoder.decode bad arg");

  const bomBytes = new Uint8Array([0xef, 0xbb, 0xbf, 0x68, 0x69]);
  assertEq(new TextDecoder().decode(bomBytes), "hi", "BOM stripped by default");
  assertEq(new TextDecoder("utf-8", { ignoreBOM: true }).decode(bomBytes), "\ufeffhi", "ignoreBOM preserves BOM");
  assertEq(__ether_text_decode(bomBytes), "\ufeffhi", "__ether_text_decode preserves BOM");

  const fatalTD = new TextDecoder("utf-8", { fatal: true });
  assertThrows(() => fatalTD.decode(new Uint8Array([0xff])), TypeError, "fatal invalid byte");
  assertEq(fatalTD.decode(subU8), "café € 😀 日本語", "fatal valid bytes");

  // Streaming multi-byte UTF-8 and split BOM across 1-byte chunks.
  const streamTD = new TextDecoder();
  const streamInput = new Uint8Array([0xef, 0xbb, 0xbf, 0xf0, 0x9f, 0x98, 0x80, 0xe2, 0x82, 0xac, 0x21]);
  let streamed = "";
  for (let i = 0; i < streamInput.length; i++) {
    streamed += streamTD.decode(streamInput.subarray(i, i + 1), { stream: true });
  }
  streamed += streamTD.decode();
  assertEq(streamed, "😀€!", "byte-by-byte stream");

  // Incomplete trailing sequence flushed as U+FFFD when stream ends.
  let tailOut = streamTD.decode(new Uint8Array([0x41, 0xf0, 0x9f]), { stream: true });
  tailOut += streamTD.decode();
  assertEq(tailOut, "A\ufffd", "stream trailing incomplete flush");

  // TextEncoder lone surrogates (with and without embedded NUL) -> U+FFFD UTF-8 bytes.
  assertEq(bootEncoder.encoding, "utf-8", "TextEncoder.encoding");
  assertBytes(bootEncoder.encode("\ud800x\udc00"), [0xef, 0xbf, 0xbd, 0x78, 0xef, 0xbf, 0xbd], "lone surrogates");
  assertBytes(bootEncoder.encode("\ud800\x00\udc00"), [0xef, 0xbf, 0xbd, 0x00, 0xef, 0xbf, 0xbd], "lone surrogates with nul");

  return { status: 200, body: { ok: true } };
};

globalThis.rank_key = function(c) {
  const bytes = bootEncoder.encode(String(c.title || ""));
  return bootDecoder.decode(bytes) === "Édition" ? 42 : 1;
};

globalThis.cond_match = function(c, op, v) {
  return bootDecoder.decode(bootEncoder.encode(op)) === "eq" && c.v === v;
};
`
	e := testEngine(t)
	p, err := e.LoadJS("test.codecs", "1.0.0", src, newFakeHost(), roomyHot())
	if err != nil {
		t.Fatalf("LoadJS: %v", err)
	}
	defer p.Close()

	resp, err := p.Call(context.Background(), Request{Method: "GET", Path: "/check"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("status = %d body = %s", resp.Status, resp.Body)
	}

	scores, out, err := p.RankBatch(context.Background(), []Candidate{
		{ReleaseID: "r1", Facts: json.RawMessage(`{"title":"Édition"}`)},
		{ReleaseID: "r2", Facts: json.RawMessage(`{"title":"Standard"}`)},
	}, nil)
	if err != nil || out.SlowCandidates != 0 || scores[0] != 42 || scores[1] != 1 {
		t.Fatalf("hot RankBatch scores=%v out=%+v err=%v", scores, out, err)
	}
	ok, _, err := p.CondMatch(context.Background(), Candidate{ReleaseID: "c1", Facts: json.RawMessage(`{"v":7}`)}, "eq", json.RawMessage(`7`))
	if err != nil || !ok {
		t.Fatalf("hot CondMatch ok=%v err=%v", ok, err)
	}
}

type archiveMemberHost struct {
	packedRows []map[string]any
}

func (h *archiveMemberHost) CallHost(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
	switch name {
	case "archive.read":
		return json.Marshal(h.packedRows)
	case "log":
		return json.RawMessage("null"), nil
	default:
		return nil, Forbidden("not granted: " + name)
	}
}

func build37KiBArchiveJSON(t *testing.T) []byte {
	t.Helper()
	type entry struct {
		ID        int      `json:"id"`
		Slug      string   `json:"slug"`
		Title     string   `json:"title"`
		Region    string   `json:"region"`
		Developer string   `json:"developer"`
		Languages []string `json:"languages"`
		SHA1      string   `json:"sha1"`
	}
	titles := []string{
		"Pokémon Émeraude — Édition Spéciale",
		"イースVIII -Lacrimosa of DANA-",
		"Wiedźmin 3: Dziki Gon — Edycja Kompletna",
		"Chrono Trigger (USA, Europe) [Rev 1]",
	}
	var entries []entry
	for i := 0; ; i++ {
		entries = append(entries, entry{
			ID:        1000 + i,
			Slug:      fmt.Sprintf("catalogue-item-%04d", i),
			Title:     titles[i%len(titles)],
			Region:    "Worldwide",
			Developer: "Studio Nihon Falcom / CD Projekt",
			Languages: []string{"en", "fr", "ja", "pl"},
			SHA1:      fmt.Sprintf("%040x", i*9973+17),
		})
		doc := map[string]any{
			"schema":  1,
			"archive": "metadata/entries.json",
			"count":   len(entries),
			"entries": entries,
		}
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) >= 37*1024 {
			return b
		}
	}
}

func loadTSSDKFiles(t *testing.T) map[string]string {
	t.Helper()
	sdkDir := filepath.Join("..", "..", "..", "sdk", "typescript", "src")
	ents, err := os.ReadDir(sdkDir)
	if err != nil {
		t.Fatalf("read sdk dir: %v", err)
	}
	files := make(map[string]string, len(ents)+1)
	for _, ent := range ents {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".ts") || strings.Contains(ent.Name(), ".test.") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(sdkDir, ent.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", ent.Name(), err)
		}
		files["src/sdk/"+ent.Name()] = string(b)
	}
	return files
}

func TestJSTSSDKTextCodecsAnd37KiBMemberLatency(t *testing.T) {
	memberJSON := build37KiBArchiveJSON(t)
	host := &archiveMemberHost{
		packedRows: []map[string]any{
			{
				"index":      0,
				"member":     0,
				"size":       16,
				"bodyBase64": base64.StdEncoding.EncodeToString([]byte("LEADING_MEMBER_0")),
			},
			{
				"index":      1,
				"member":     4,
				"size":       len(memberJSON),
				"bodyBase64": base64.StdEncoding.EncodeToString(memberJSON),
			},
			{
				"index":      2,
				"member":     9,
				"size":       17,
				"bodyBase64": base64.StdEncoding.EncodeToString([]byte("TRAILING_MEMBER_2")),
			},
		},
	}

	files := loadTSSDKFiles(t)
	files["src/index.ts"] = `
import {
  createHostedClient,
  decodeText,
  encodeText,
} from "./sdk/index.ts";

// Shape 1 from GitHub issue #556: per-byte String.fromCharCode UTF-8 loop.
function legacyPerByteDecodeUTF8(bytes: Uint8Array): string {
  let out = "";
  let i = 0;
  while (i < bytes.length) {
    const b0 = bytes[i]!;
    if (b0 < 0x80) {
      out += String.fromCharCode(b0);
      i++;
    } else if ((b0 & 0xe0) === 0xc0) {
      out += String.fromCharCode(((b0 & 0x1f) << 6) | (bytes[i + 1]! & 0x3f));
      i += 2;
    } else if ((b0 & 0xf0) === 0xe0) {
      out += String.fromCharCode(
        ((b0 & 0x0f) << 12) | ((bytes[i + 1]! & 0x3f) << 6) | (bytes[i + 2]! & 0x3f),
      );
      i += 3;
    } else {
      const cp =
        (((b0 & 0x07) << 18) |
          ((bytes[i + 1]! & 0x3f) << 12) |
          ((bytes[i + 2]! & 0x3f) << 6) |
          (bytes[i + 3]! & 0x3f)) -
        0x10000;
      out += String.fromCharCode(0xd800 + (cp >> 10), 0xdc00 + (cp & 0x3ff));
      i += 4;
    }
  }
  return out;
}

// Shape 2 from GitHub issue #556: ASCII runs via String.fromCharCode.apply in 8 KiB pieces.
function legacyChunkedDecodeUTF8(bytes: Uint8Array): string {
  let out = "";
  let i = 0;
  while (i < bytes.length) {
    const b0 = bytes[i]!;
    if (b0 < 0x80) {
      let end = i + 1;
      while (end < bytes.length && end - i < 8192 && bytes[end]! < 0x80) end++;
      out += String.fromCharCode.apply(null, bytes.subarray(i, end) as unknown as number[]);
      i = end;
    } else if ((b0 & 0xe0) === 0xc0) {
      out += String.fromCharCode(((b0 & 0x1f) << 6) | (bytes[i + 1]! & 0x3f));
      i += 2;
    } else if ((b0 & 0xf0) === 0xe0) {
      out += String.fromCharCode(
        ((b0 & 0x0f) << 12) | ((bytes[i + 1]! & 0x3f) << 6) | (bytes[i + 2]! & 0x3f),
      );
      i += 3;
    } else {
      const cp =
        (((b0 & 0x07) << 18) |
          ((bytes[i + 1]! & 0x3f) << 12) |
          ((bytes[i + 2]! & 0x3f) << 6) |
          (bytes[i + 3]! & 0x3f)) -
        0x10000;
      out += String.fromCharCode(0xd800 + (cp >> 10), 0xdc00 + (cp & 0x3ff));
      i += 4;
    }
  }
  return out;
}

export function handle(req: { path: string }) {
  const client = createHostedClient();
  const items = client.archive!.read("blob-1", [
    { member: 0, offset: 0, length: 16 },
    { member: 4, offset: 0, length: 65536 },
    { member: 9, offset: 0, length: 17 },
  ]) as Array<{ index: number; member: number; size: number; body: Uint8Array }>;
  const memberBytes = items[1]!.body;

  if (req.path === "/verify") {
    const parsedSDK = JSON.parse(decodeText(memberBytes));
    const parsedTD = JSON.parse(new TextDecoder().decode(memberBytes));
    const parsedLegacy = JSON.parse(legacyChunkedDecodeUTF8(memberBytes));
    const roundTrip = decodeText(encodeText("こんにちは café 😀"));

    return {
      status: 200,
      body: {
        byteLength: memberBytes.byteLength,
        byteOffset: memberBytes.byteOffset,
        countSDK: parsedSDK.count,
        countTD: parsedTD.count,
        countLegacy: parsedLegacy.count,
        firstTitle: parsedSDK.entries[0].title,
        secondTitle: parsedSDK.entries[1].title,
        matchesLegacy: JSON.stringify(parsedSDK) === JSON.stringify(parsedLegacy),
        roundTrip,
      },
    };
  }

  // Benchmark 37 KiB archive JSON member decode + JSON.parse inside QuickJS.
  const warmup = 3;
  const iters = 15;
  for (let i = 0; i < warmup; i++) {
    JSON.parse(legacyPerByteDecodeUTF8(memberBytes));
    JSON.parse(legacyChunkedDecodeUTF8(memberBytes));
    JSON.parse(decodeText(memberBytes));
  }

  const t0 = performance.now();
  let perByteCount = 0;
  for (let i = 0; i < iters; i++) {
    perByteCount += JSON.parse(legacyPerByteDecodeUTF8(memberBytes)).count;
  }
  const perByteTotalMs = (performance.now() - t0) / iters;

  const t1 = performance.now();
  let perByteLen = 0;
  for (let i = 0; i < iters; i++) {
    perByteLen += legacyPerByteDecodeUTF8(memberBytes).length;
  }
  const perByteDecodeMs = (performance.now() - t1) / iters;

  const t2 = performance.now();
  let chunkedCount = 0;
  for (let i = 0; i < iters; i++) {
    chunkedCount += JSON.parse(legacyChunkedDecodeUTF8(memberBytes)).count;
  }
  const chunkedTotalMs = (performance.now() - t2) / iters;

  const t3 = performance.now();
  let chunkedLen = 0;
  for (let i = 0; i < iters; i++) {
    chunkedLen += legacyChunkedDecodeUTF8(memberBytes).length;
  }
  const chunkedDecodeMs = (performance.now() - t3) / iters;

  const t4 = performance.now();
  let afterCount = 0;
  for (let i = 0; i < iters; i++) {
    afterCount += JSON.parse(decodeText(memberBytes)).count;
  }
  const afterTotalMs = (performance.now() - t4) / iters;

  const t5 = performance.now();
  let afterLen = 0;
  for (let i = 0; i < iters; i++) {
    afterLen += decodeText(memberBytes).length;
  }
  const afterDecodeMs = (performance.now() - t5) / iters;

  return {
    status: 200,
    body: {
      bytes: memberBytes.byteLength,
      iters,
      perByteDecodeMs,
      perByteTotalMs,
      chunkedDecodeMs,
      chunkedTotalMs,
      afterDecodeMs,
      afterTotalMs,
      sameCounts:
        perByteCount === afterCount &&
        chunkedCount === afterCount &&
        perByteLen === afterLen &&
        chunkedLen === afterLen,
    },
  };
}
`

	js, _, diags := Compile(files, "src/index.ts")
	if len(diags) != 0 || len(js) == 0 {
		t.Fatalf("Compile TS SDK plugin failed: diags=%+v", diags)
	}

	e := testEngine(t)
	p, err := e.LoadJS("test.sdk.codecs", "1.0.0", string(js), host, Limits{
		MemoryBytes: 64 << 20,
		Deadline:    30 * time.Second,
		UnitBudget:  1_000_000,
	})
	if err != nil {
		t.Fatalf("LoadJS: %v", err)
	}
	defer p.Close()

	verifyResp, err := p.Call(context.Background(), Request{Method: "GET", Path: "/verify"})
	if err != nil {
		t.Fatalf("Call /verify: %v", err)
	}
	var vBody struct {
		ByteLength    int    `json:"byteLength"`
		ByteOffset    int    `json:"byteOffset"`
		CountSDK      int    `json:"countSDK"`
		CountTD       int    `json:"countTD"`
		CountLegacy   int    `json:"countLegacy"`
		FirstTitle    string `json:"firstTitle"`
		SecondTitle   string `json:"secondTitle"`
		MatchesLegacy bool   `json:"matchesLegacy"`
		RoundTrip     string `json:"roundTrip"`
	}
	if err := json.Unmarshal(verifyResp.Body, &vBody); err != nil {
		t.Fatalf("unmarshal /verify (%s): %v", verifyResp.Body, err)
	}
	if vBody.ByteLength != len(memberJSON) || vBody.ByteOffset != 16 {
		t.Fatalf("unexpected subview range: offset=%d len=%d (want 16, %d)", vBody.ByteOffset, vBody.ByteLength, len(memberJSON))
	}
	if !vBody.MatchesLegacy || vBody.CountSDK == 0 || vBody.CountSDK != vBody.CountTD {
		t.Fatalf("mismatch in decoded JSON: %+v", vBody)
	}
	if vBody.FirstTitle != "Pokémon Émeraude — Édition Spéciale" || vBody.SecondTitle != "イースVIII -Lacrimosa of DANA-" {
		t.Fatalf("unexpected UTF-8 titles: %+v", vBody)
	}
	if vBody.RoundTrip != "こんにちは café 😀" {
		t.Fatalf("unexpected roundTrip: %q", vBody.RoundTrip)
	}

	benchResp, err := p.Call(context.Background(), Request{Method: "GET", Path: "/bench"})
	if err != nil {
		t.Fatalf("Call /bench: %v", err)
	}
	var bBody struct {
		Bytes           int     `json:"bytes"`
		Iters           int     `json:"iters"`
		PerByteDecodeMs float64 `json:"perByteDecodeMs"`
		PerByteTotalMs  float64 `json:"perByteTotalMs"`
		ChunkedDecodeMs float64 `json:"chunkedDecodeMs"`
		ChunkedTotalMs  float64 `json:"chunkedTotalMs"`
		AfterDecodeMs   float64 `json:"afterDecodeMs"`
		AfterTotalMs    float64 `json:"afterTotalMs"`
		SameCounts      bool    `json:"sameCounts"`
	}
	if err := json.Unmarshal(benchResp.Body, &bBody); err != nil {
		t.Fatalf("unmarshal /bench (%s): %v", benchResp.Body, err)
	}
	if !bBody.SameCounts {
		t.Fatalf("before/after counts or decoded string lengths differed: %+v", bBody)
	}
	t.Logf(
		"37 KiB (%d B) JSON archive member in QuickJS (avg over %d runs): decode-only per-byte JS %.2f ms / 8KiB-chunked JS %.2f ms -> native %.3f ms (%.1fx vs per-byte, %.1fx vs chunked); decode+JSON.parse %.2f ms / %.2f ms -> %.2f ms",
		bBody.Bytes,
		bBody.Iters,
		bBody.PerByteDecodeMs,
		bBody.ChunkedDecodeMs,
		bBody.AfterDecodeMs,
		bBody.PerByteDecodeMs/max(bBody.AfterDecodeMs, 0.001),
		bBody.ChunkedDecodeMs/max(bBody.AfterDecodeMs, 0.001),
		bBody.PerByteTotalMs,
		bBody.ChunkedTotalMs,
		bBody.AfterTotalMs,
	)
	if bBody.AfterDecodeMs >= bBody.ChunkedDecodeMs || bBody.AfterDecodeMs >= bBody.PerByteDecodeMs {
		t.Fatalf("expected native decode (%.3f ms) to beat JS decoders (chunked=%.3f ms, per-byte=%.3f ms)",
			bBody.AfterDecodeMs, bBody.ChunkedDecodeMs, bBody.PerByteDecodeMs)
	}
}
