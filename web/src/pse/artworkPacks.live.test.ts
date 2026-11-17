import { expect, it } from "vitest";
import { PACK_SOURCES, parsePack } from "./artworkPacks";
// Opt-in probe of public metadata; normal CI never depends on external sites.
it.skipIf(process.env.LIVE_ARTWORK_PACKS !== "1")(
  "loads every public pack manifest",
  async () => {
    for (const source of PACK_SOURCES) {
      const response = await fetch(source.url, { signal: AbortSignal.timeout(30000) });
      expect(response.ok, source.name).toBe(true);
      const images = parsePack(await response.json(), source);
      expect(images.length, source.name).toBeGreaterThan(0);
      console.info(source.name, images.length);
    }
  },
  180000,
);
