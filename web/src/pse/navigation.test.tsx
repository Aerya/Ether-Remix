import { expect, it } from "vitest";
import { buildNavigation } from "../App";
import { remixDescriptors } from "./mock";
import { legacyRemixRoute, sourcesHref } from "./navigation";
it("keeps one source entry and maps legacy source addresses without losing parameters", () => {
  const nav = buildNavigation([
    ...remixDescriptors,
    {
      kind: "navItem",
      id: "sources",
      owner: "builtin",
      label: { source: "Sources" },
      group: "top",
      meta: { route: "/sources" },
    },
  ]);
  const links = nav.flatMap((g) => g.items);
  expect(links.some((i) => i.href === "/remix")).toBe(true);
  expect(links.some((i) => i.href === "/sources")).toBe(false);
  expect(sourcesHref("stremio", "?new=addon")).toBe("/remix/sources?new=addon&source=stremio");
  expect(sourcesHref("usage", "?days=30")).toBe("/remix/apercu?days=30&view=usage");
  expect(legacyRemixRoute("priorites")).toBe("/remix/lecture");
  expect(legacyRemixRoute("animes")).toBe("/remix/contenus?content=animes");
  expect(legacyRemixRoute("statistiques", "?days=7")).toBe("/remix/apercu?days=7");
  expect(legacyRemixRoute("demarrage")).toBe("/remix/apercu?view=demarrage");
  expect(legacyRemixRoute("releases")).toBe("/remix/indexation?view=releases");
  expect(legacyRemixRoute("langues")).toBe("/remix/lecture?view=qualite");
  expect(legacyRemixRoute("demande")).toBe("/remix/lecture?view=vostfr");
  expect(legacyRemixRoute("medias-locaux")).toBe("/remix/sources?source=local#local-media-health");
});

it("preserves native IPTV provider IDs when redirecting to Remix", () => {
  const url = new URL(sourcesHref("iptv", "?source=king-123"), "https://example.invalid");
  expect(url.searchParams.get("source")).toBe("iptv");
  expect(url.searchParams.get("iptvSource")).toBe("king-123");
});

it("always exposes the release index", async () => {
  const { visibleRemixViews } = await import("./navigation");
  expect(visibleRemixViews("indexation").some((v) => v.key === "releases")).toBe(true);
});
