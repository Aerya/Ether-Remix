import { expect, it } from "vitest";
import { parseDuration, formatDuration } from "./duration";

it.each([
  ["720h", "30", "d", "720h"],
  ["6h0m0s", "6", "h", "6h"],
  ["90m", "90", "m", "90m"],
  ["20s", "20", "s", "20s"],
  ["1m30s", "90", "s", "90s"],
  ["500ms", "0.5", "s", "0.5s"],
  ["", "", "h", ""],
] as const)("round trips %s", (raw, amount, unit, canonical) => {
  expect(parseDuration(raw)).toEqual({ amount, unit });
  expect(formatDuration(amount, unit)).toBe(canonical);
  expect(parseDuration(formatDuration(amount, unit))).toEqual(parseDuration(raw));
});

it("caps non-integer seconds at two decimal places", () => {
  expect(parseDuration("1.3333333s")).toEqual({ amount: "1.33", unit: "s" });
});
