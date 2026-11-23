import { describe, expect, it } from "vitest";
import {
  compareChannelNumber,
  computeReorderPlan,
  slotToTargetIndex,
  targetIndexToSlot,
  type ReorderChannel,
} from "../pages/livetv/reorder";

function ch(id: string, number: string, name = id): ReorderChannel {
  return { id, number, name };
}

function applyAndSort(
  ordered: readonly ReorderChannel[],
  nextNumbers: Record<string, string>,
): ReorderChannel[] {
  return ordered
    .map((c) => (nextNumbers[c.id] !== undefined ? { ...c, number: nextNumbers[c.id]! } : c))
    .sort((a, b) => compareChannelNumber(a.number, b.number) || a.id.localeCompare(b.id));
}

describe("compareChannelNumber", () => {
  it("matches Go parseNumberKey natural major.minor ordering", () => {
    const nums = ["10", "4.10", "2", "4.2", "4.1", "4", "1", "100"];
    const sorted = [...nums].sort(compareChannelNumber);
    expect(sorted).toEqual(["1", "2", "4", "4.1", "4.2", "4.10", "10", "100"]);
  });
});

describe("slot/index conversions", () => {
  it("maps visual slots around the dragged row consistently", () => {
    // List of 5 items, dragging index 3:
    expect(slotToTargetIndex(0, 3, 5)).toBe(0);
    expect(slotToTargetIndex(1, 3, 5)).toBe(1);
    expect(slotToTargetIndex(3, 3, 5)).toBe(3); // same spot
    expect(slotToTargetIndex(4, 3, 5)).toBe(3); // same spot
    expect(slotToTargetIndex(5, 3, 5)).toBe(4); // end

    expect(targetIndexToSlot(0, 3)).toBe(0);
    expect(targetIndexToSlot(1, 3)).toBe(1);
    expect(targetIndexToSlot(4, 3)).toBe(5);
  });
});

describe("computeReorderPlan", () => {
  it("uses an existing integer gap without shifting other channels", () => {
    const lineup = [ch("a", "1"), ch("b", "5"), ch("c", "100"), ch("d", "101")];
    // Move d (#101) between b (#5) and c (#100) -> index 2
    const plan = computeReorderPlan(lineup, "d", 2);
    expect(plan).not.toBeNull();
    expect(plan!.toNumber).toBe("6");
    expect(plan!.shifted).toEqual([]);
    expect(plan!.nextNumbers).toEqual({ d: "6" });
    expect(plan!.prevNumbers).toEqual({ d: "101" });
    expect(applyAndSort(lineup, plan!.nextNumbers).map((c) => `${c.id}:${c.number}`)).toEqual([
      "a:1",
      "b:5",
      "d:6",
      "c:100",
    ]);
  });

  it("shifts only contiguous following numbers until the first gap", () => {
    const lineup = [
      ch("a", "1"),
      ch("b", "2"),
      ch("c", "3"),
      ch("d", "4"),
      ch("e", "5"),
      ch("f", "100"),
      ch("g", "101"),
    ];
    // Move f (#100) between b (#2) and c (#3) -> index 2
    const plan = computeReorderPlan(lineup, "f", 2);
    expect(plan).not.toBeNull();
    expect(plan!.toNumber).toBe("3");
    expect(plan!.nextNumbers).toEqual({ f: "3", c: "4", d: "5", e: "6" });
    expect(plan!.prevNumbers).toEqual({ f: "100", c: "3", d: "4", e: "5" });
    expect(applyAndSort(lineup, plan!.nextNumbers).map((c) => `${c.id}:${c.number}`)).toEqual([
      "a:1",
      "b:2",
      "f:3",
      "c:4",
      "d:5",
      "e:6",
      "g:101",
    ]);
  });

  it("drops at the top cleanly with or without room below the first channel", () => {
    // Room below #100 -> #99, no shifts
    const high = [ch("a", "100"), ch("b", "101"), ch("c", "102")];
    const highPlan = computeReorderPlan(high, "c", 0)!;
    expect(highPlan.nextNumbers).toEqual({ c: "99" });
    expect(highPlan.shifted).toEqual([]);

    // No room below #1 -> becomes #1, shifts 1..2 -> 2..3, stops before gap at 10
    const low = [ch("a", "1"), ch("b", "2"), ch("c", "10"), ch("d", "20")];
    const lowPlan = computeReorderPlan(low, "d", 0)!;
    expect(lowPlan.nextNumbers).toEqual({ d: "1", a: "2", b: "3" });
    expect(applyAndSort(low, lowPlan.nextNumbers).map((c) => `${c.id}:${c.number}`)).toEqual([
      "d:1",
      "a:2",
      "b:3",
      "c:10",
    ]);
  });

  it("drops at the bottom cleanly without shifting other channels", () => {
    const lineup = [ch("a", "1"), ch("b", "2"), ch("c", "50")];
    const plan = computeReorderPlan(lineup, "a", 2)!;
    expect(plan.nextNumbers).toEqual({ a: "51" });
    expect(plan.shifted).toEqual([]);
  });

  it("handles subchannel gaps, subchannel shifts, and parent-major shifts", () => {
    // Subchannel gap: 4.1 .. 4.3 -> 4.2
    const subGap = [ch("a", "4.1"), ch("b", "4.3"), ch("c", "10")];
    const gapPlan = computeReorderPlan(subGap, "c", 1)!;
    expect(gapPlan.nextNumbers).toEqual({ c: "4.2" });
    expect(gapPlan.shifted).toEqual([]);

    // Subchannel collision: 4.1 .. 4.2 .. 4.3 .. 5 -> shifts 4.2..4.3 to 4.3..4.4, stops at 5
    const subTight = [ch("a", "4.1"), ch("b", "4.2"), ch("c", "4.3"), ch("d", "5"), ch("e", "9")];
    const tightPlan = computeReorderPlan(subTight, "e", 1)!;
    expect(tightPlan.nextNumbers).toEqual({ e: "4.2", b: "4.3", c: "4.4" });
    expect(applyAndSort(subTight, tightPlan.nextNumbers).map((c) => `${c.id}:${c.number}`)).toEqual(
      ["a:4.1", "e:4.2", "b:4.3", "c:4.4", "d:5"],
    );

    // Shifting a major carries its subchannels (3 -> 4, 3.1 -> 4.1, 4 -> 5)
    const mixed = [ch("a", "1"), ch("b", "2"), ch("c", "3"), ch("c1", "3.1"), ch("d", "4")];
    const mixedPlan = computeReorderPlan(mixed, "d", 2)!;
    expect(applyAndSort(mixed, mixedPlan.nextNumbers).map((c) => c.id)).toEqual([
      "a",
      "b",
      "d",
      "c",
      "c1",
    ]);
  });

  it("preserves exact requested order for every move in a lineup with duplicates and gaps", () => {
    const lineup = [
      ch("c1", "1"),
      ch("c2", "2"),
      ch("c2b", "2"),
      ch("c3", "3"),
      ch("c4", "4.1"),
      ch("c5", "4.2"),
      ch("c6", "10"),
      ch("c7", "100"),
    ];
    for (let from = 0; from < lineup.length; from++) {
      for (let to = 0; to < lineup.length; to++) {
        if (from === to) {
          expect(computeReorderPlan(lineup, lineup[from]!.id, to)).toBeNull();
          continue;
        }
        const plan = computeReorderPlan(lineup, lineup[from]!.id, to);
        expect(plan).not.toBeNull();
        const after = applyAndSort(lineup, plan!.nextNumbers).map((c) => c.id);
        const expected = lineup.map((c) => c.id);
        const [movedId] = expected.splice(from, 1);
        expected.splice(to, 0, movedId!);
        expect(after).toEqual(expected);
      }
    }
  });
});
