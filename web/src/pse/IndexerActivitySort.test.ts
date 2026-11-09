import { describe, expect, it } from "vitest";
import { sourcesByReleases } from "./IndexerActivity";

const list = [
  { id: "1", name: "Zeta", releases: 5 },
  { id: "2", name: "Alpha", releases: 1 },
  { id: "3", name: "Beta", releases: 9 },
];

describe("sourcesByReleases", () => {
  it("trie par nombre", () => {
    expect(sourcesByReleases(list, "count").map((s) => s.name)).toEqual(["Beta", "Zeta", "Alpha"]);
  });
  it("trie par nom", () => {
    expect(sourcesByReleases(list, "name").map((s) => s.name)).toEqual(["Alpha", "Beta", "Zeta"]);
  });
});
