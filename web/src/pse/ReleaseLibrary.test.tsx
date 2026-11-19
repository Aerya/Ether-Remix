import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ReleaseCard, ReleaseLibrary, sortReleases, type ReleaseRow } from "./ReleaseLibrary";
vi.mock("../lib/api", () => ({
  apiFetch: vi.fn(async () => ({ enabled: false, releases: rows })),
}));
const row = (name: string, height: number, size: number): ReleaseRow => ({
  name,
  height,
  size,
  source: "source",
  protocol: "http",
  videoCodec: "hevc",
  audioCodec: "aac",
  languages: ["fr"],
  cached: [],
  published: 0,
  firstSeen: 100,
  lastSeen: 200,
  state: "metadata",
});
const rows = [row("Large", 2160, 12e9), row("Small", 720, 2e9), row("Unknown", 0, 0)];
describe("indexed releases", () => {
  it("sorts numerically, keeps unknowns last, preserves input", () => {
    expect(sortReleases(rows, "size", false).map((r) => r.name)).toEqual([
      "Small",
      "Large",
      "Unknown",
    ]);
    expect(sortReleases(rows, "height", true).map((r) => r.name)).toEqual([
      "Large",
      "Small",
      "Unknown",
    ]);
    expect(rows[0]?.name).toBe("Large");
  });
  it("shows stored metadata and filters without searching", async () => {
    render(<ReleaseLibrary titleKey="42" />);
    await screen.findByText("Large");
    const input = screen.getByRole("textbox");
    fireEvent.change(input, { target: { value: "Small" } });
    expect(screen.queryByText("Large")).toBeNull();
    expect(screen.getByText("Small")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.getByText("Small").closest("details")).toBeTruthy();
  });
});

it("does not present a stored but rejected link as available", () => {
  render(<ReleaseCard row={{ ...row("Son.Yemek.2026.1080p", 1080, 100), state: "excluded" }} />);
  expect(screen.getByText("Outside criteria · removal pending")).toBeVisible();
  expect(screen.queryByText("Reference available")).toBeNull();
});
