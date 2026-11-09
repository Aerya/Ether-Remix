import { describe, expect, it } from "vitest";
import { moved } from "./SourceOrder";

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
