import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { ViewerLibraries, remixChecked, remixSection } from "./ViewerAccess";
import type { Library } from "../libraries/api";
import type { ThemeLibrary } from "../../pse/ThemeLibraries";

const lib = (id: number, name: string, mediaType: string) => ({ id, name, mediaType }) as Library;
const libs = [lib(1, "Movies", "movie"), lib(2, "Shows", "show")];
const theme = (id: number, name: string, type: ThemeLibrary["type"]) =>
  ({ id, name, type, collections: [], position: 0, section: 5100 + id }) as ThemeLibrary;
const themes = [theme(1, "Concerts", "movie"), theme(2, "Anime", "mixed")];

vi.mock("../libraries/api", () => ({
  librariesApi: { list: vi.fn(async () => libs) },
}));
vi.mock("../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/api")>()),
  apiFetch: vi.fn(async () => ({ libraries: themes })),
}));

describe("Remix libraries in viewer access", () => {
  it("uses 5100 + id as the section", () => {
    expect(remixSection(themes[1]!)).toBe(5102);
  });
  it("legacy restricted list follows the base library", () => {
    expect([...remixChecked([1], themes, libs)]).toEqual([5101, 5102]);
    expect([...remixChecked([2], themes, libs)]).toEqual([5102]);
  });
  it("explicit list is reflected; orphan ids are ignored", () => {
    expect([...remixChecked([1, 2, 5102, 5199], themes, libs)]).toEqual([5102]);
    expect([...remixChecked([1, 2, 5201], themes, libs)]).toEqual([5101]);
  });
  it("lists Remix libraries when restricted", async () => {
    render(<ViewerLibraries userId={7} allowed={[1, 5102]} onSaved={() => {}} />);
    expect(await screen.findByText("Concerts")).toBeTruthy();
    expect(screen.getByText("Anime")).toBeTruthy();
    expect(screen.getByText("Remix libraries")).toBeTruthy();
  });
});
