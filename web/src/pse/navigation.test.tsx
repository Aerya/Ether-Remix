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
  expect(links.some((i) => i.href === "/accent")).toBe(true);
  expect(links.some((i) => i.href === "/sources")).toBe(false);
  expect(sourcesHref("stremio", "?new=addon")).toBe("/accent/sources?new=addon&source=stremio");
  expect(sourcesHref("usage", "?days=30")).toBe("/accent/statistiques?days=30&view=usage");
  expect(legacyRemixRoute("animes")).toBe("/accent/contenus?content=animes");
});
