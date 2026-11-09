import { expect, it } from "vitest";
import { pseMergeOrder, pseShowsIndexer } from "./stremioTab";

const list = [
  { id: "a", kind: "newznab" },
  { id: "s1", kind: "stremio" },
  { id: "b", kind: "site" },
  { id: "s2", kind: "stremio" },
];

it("splits Stremio addons from the other indexers", () => {
  expect(list.filter((ix) => pseShowsIndexer("stremio", ix)).map((ix) => ix.id)).toEqual([
    "s1",
    "s2",
  ]);
  expect(list.filter((ix) => pseShowsIndexer("indexers", ix)).map((ix) => ix.id)).toEqual([
    "a",
    "b",
  ]);
  expect(list.filter((ix) => pseShowsIndexer("all", ix))).toHaveLength(4);
});

it("reorders the visible addons without moving hidden indexers", () => {
  const next = pseMergeOrder(list, [1, 3], [list[3]!, list[1]!]);
  expect(next.map((ix) => ix.id)).toEqual(["a", "s2", "b", "s1"]);
});
