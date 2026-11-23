import test from "node:test";
import assert from "node:assert/strict";
import {
  decodeText,
  dispatchOverTunnelFrame,
  encodeText,
  plugin,
} from "./index.ts";

test("decodeText and encodeText handle subviews, NUL, BOM, and WHATWG invalid UTF-8", () => {
  const sample = "\ufeffcafé € 😀 日本語\x00end";
  const encoded = encodeText(sample);
  assert.equal(decodeText(encoded), sample);

  // Subviews into a shared ArrayBuffer must honor byteOffset and byteLength.
  const packed = new ArrayBuffer(encoded.byteLength + 20);
  const full = new Uint8Array(packed);
  full.fill(0x78);
  full.set(encoded, 7);
  const subU8 = new Uint8Array(packed, 7, encoded.byteLength);
  const subDV = new DataView(packed, 7, encoded.byteLength);
  assert.equal(decodeText(subU8), sample);
  assert.equal(decodeText(subDV), sample);

  // WHATWG maximal-subpart replacement.
  assert.equal(decodeText(new Uint8Array([0xc0, 0xaf])), "\ufffd\ufffd");
  assert.equal(decodeText(new Uint8Array([0xe2, 0x82, 0x41])), "\ufffdA");
  assert.equal(decodeText(new Uint8Array([0xed, 0xa0, 0x80])), "\ufffd\ufffd\ufffd");
  assert.equal(decodeText(new Uint8Array([0xf0, 0x9f, 0x98, 0x41])), "\ufffdA");

  // Lone surrogates encode to U+FFFD.
  assert.deepEqual(
    Array.from(encodeText("\ud800x\udc00")),
    [0xef, 0xbf, 0xbd, 0x78, 0xef, 0xbf, 0xbd],
  );
});

test("decodeText and encodeText prefer __ether_text_* hooks and fall back cleanly without TextDecoder", () => {
  const g = globalThis as Record<string, unknown>;
  const origTD = g.TextDecoder;
  const origTE = g.TextEncoder;
  let nativeDecodeCalls = 0;
  let nativeEncodeCalls = 0;

  try {
    g.__ether_text_decode = (bytes: ArrayBuffer | ArrayBufferView) => {
      nativeDecodeCalls++;
      return new (origTD as typeof TextDecoder)("utf-8", { ignoreBOM: true }).decode(bytes);
    };
    g.__ether_text_encode = (text: string) => {
      nativeEncodeCalls++;
      const u8 = new (origTE as typeof TextEncoder)().encode(text);
      return u8.buffer.slice(u8.byteOffset, u8.byteOffset + u8.byteLength);
    };

    const round = decodeText(encodeText("Pokémon Émeraude 😀"));
    assert.equal(round, "Pokémon Émeraude 😀");
    assert.equal(nativeEncodeCalls, 1);
    assert.equal(nativeDecodeCalls, 1);

    // Pure-JS fallback when neither native hooks nor TextDecoder/TextEncoder exist.
    delete g.__ether_text_decode;
    delete g.__ether_text_encode;
    g.TextDecoder = undefined;
    g.TextEncoder = undefined;

    const text = "\ufeffこんにちは café € 😀\x00ok";
    const fallbackBytes = encodeText(text);
    assert.equal(decodeText(fallbackBytes), text);
    assert.equal(decodeText(new Uint8Array([0xe2, 0x82, 0x41])), "\ufffdA");
    assert.equal(decodeText(new Uint8Array([0xf0, 0x9f, 0x98, 0x41])), "\ufffdA");
    assert.deepEqual(
      Array.from(encodeText("\ud800x\udc00")),
      [0xef, 0xbf, 0xbd, 0x78, 0xef, 0xbf, 0xbd],
    );
  } finally {
    delete g.__ether_text_decode;
    delete g.__ether_text_encode;
    g.TextDecoder = origTD;
    g.TextEncoder = origTE;
  }
});

test("dispatchOverTunnelFrame decodes and encodes UTF-8 JSON bodies via decodeText/encodeText", async () => {
  const app = plugin({
    manifest: {
      id: "org.ether.test.tunnel.utf8",
      name: "Tunnel UTF-8",
      version: "0.1.0",
      ether: {
        protocol: 1,
        kind: "extension",
        capabilities: ["tools"],
        summary: "Tunnel UTF-8 test.",
        runtime: { kind: "hosted", entry: "plugin.ts" },
      },
    },
    routes: {
      tool({ name, input }) {
        return { tool: name, echoed: input };
      },
    },
  });

  const payloadBytes = encodeText(JSON.stringify({ title: "イースVIII — Édition 😀" }));
  let binIn = "";
  for (let i = 0; i < payloadBytes.length; i++) {
    binIn += String.fromCharCode(payloadBytes[i]!);
  }
  const frames = await dispatchOverTunnelFrame(app, {
    type: "request",
    id: "utf8-1",
    method: "POST",
    path: "/ether/tool/echo",
    body: btoa(binIn),
  });
  assert.equal(frames.length, 1);
  assert.equal(frames[0]?.status, 200);
  const binOut = atob(frames[0]!.body!);
  const outBytes = new Uint8Array(binOut.length);
  for (let i = 0; i < binOut.length; i++) {
    outBytes[i] = binOut.charCodeAt(i);
  }
  const decodedResp = JSON.parse(decodeText(outBytes)) as {
    tool: string;
    echoed: { title: string };
  };
  assert.equal(decodedResp.tool, "echo");
  assert.equal(decodedResp.echoed.title, "イースVIII — Édition 😀");
});
