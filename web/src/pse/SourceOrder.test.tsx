import { describe, expect, it } from "vitest";
import { applyPriorities, moved, movedInFamily } from "./SourceOrder";

describe("moved", () => {
  it("échange deux sources voisines", () => {
    expect(moved(["a", "b", "c"], 1, -1)).toEqual(["b", "a", "c"]);
  });
  it("ne sort pas de la liste", () => {
    const list = ["a", "b"];
    expect(moved(list, 0, -1)).toBe(list);
    expect(moved(list, 1, 1)).toBe(list);
  });
});

describe("applyPriorities", () => {
  it("suit l'ordre choisi sans toucher aux sources absentes", () => {
    const list = [
      { id: "a", priority: 5 },
      { id: "b", priority: 7 },
      { id: "off", priority: 9 },
    ];
    expect(applyPriorities(list, ["b", "a"])).toEqual([
      { id: "a", priority: 2 },
      { id: "b", priority: 1 },
      { id: "off", priority: 9 },
    ]);
  });
});

describe("movedInFamily", () => {
  const familyOf = (id: string) => id[0] as string;
  it("déplace dans la famille sans toucher les autres", () => {
    expect(movedInFamily(["s1", "u1", "s2", "u2"], familyOf, "s", 1, -1)).toEqual([
      "s2",
      "u1",
      "s1",
      "u2",
    ]);
  });
});
