import { expect, it } from "vitest";
import { indexerOrigin } from "./indexerOrigin";
it("groups Prowlarr indexers by instance without displaying credentials", () => {
  const a = indexerOrigin({
    url: "https://user:secret@p.example/base/1/api?apikey=private",
    prowlarrImport: {},
  });
  const b = indexerOrigin({ url: "https://p.example/base/2/api", prowlarrImport: {} });
  expect(a.key).toBe(b.key);
  expect(a.label).toBe("Prowlarr · p.example");
  expect(a.key).not.toContain("secret");
});
it("groups Jackett Torznab paths without merging separate instances", () => {
  const a = indexerOrigin({ url: "https://j.example/api/v2.0/indexers/one/results/torznab/api" });
  const b = indexerOrigin({ url: "https://j.example/api/v2.0/indexers/two/results/torznab/api" });
  expect(a.key).toBe(b.key);
  expect(a.label).toBe("Jackett · j.example");
  expect(
    indexerOrigin({ url: "https://other.example/api/v2.0/indexers/two/results/torznab/api" }).key,
  ).not.toBe(a.key);
});
