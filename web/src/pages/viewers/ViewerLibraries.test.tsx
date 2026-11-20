import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { api, apiFetch } from "../../lib/api";
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
  it("saves the Remix marker when everything is unticked", async () => {
    const spy = vi.spyOn(api, "updateUser").mockResolvedValue({} as never);
    render(<ViewerLibraries userId={7} allowed={[1, 5100, 5102]} onSaved={() => {}} />);
    fireEvent.click(await screen.findByRole("switch", { name: "Anime" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(spy).toHaveBeenCalledWith(7, { allowedLibraries: [1, 5100] }));
  });
  it("blocks saving when the Remix libraries fail to load", async () => {
    vi.mocked(apiFetch).mockRejectedValueOnce(new Error("boom"));
    const spy = vi.spyOn(api, "updateUser").mockResolvedValue({} as never);
    render(<ViewerLibraries userId={7} allowed={[1, 2]} onSaved={() => {}} />);
    await screen.findByRole("alert");
    fireEvent.click(await screen.findByRole("switch", { name: "Shows" }));
    const save = screen.getByRole("button", { name: "Save" }) as HTMLButtonElement;
    expect(save.disabled).toBe(true);
    fireEvent.click(save);
    expect(spy).not.toHaveBeenCalled();
  });
  it("adds no marker when there is no Remix library", async () => {
    vi.mocked(apiFetch).mockResolvedValueOnce({ libraries: [] });
    const spy = vi.spyOn(api, "updateUser").mockResolvedValue({} as never);
    render(<ViewerLibraries userId={7} allowed={[1, 2]} onSaved={() => {}} />);
    fireEvent.click(await screen.findByRole("switch", { name: "Shows" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(spy).toHaveBeenCalledWith(7, { allowedLibraries: [1] }));
  });
  it("keeps the Remix switch enabled without a base library ticked", async () => {
    render(<ViewerLibraries userId={7} allowed={[5100, 5101]} onSaved={() => {}} />);
    const sw = (await screen.findByRole("switch", { name: "Concerts" })) as HTMLInputElement;
    expect(sw.disabled).toBe(false);
    expect(sw.checked).toBe(true);
    expect((screen.getByRole("switch", { name: "Anime" }) as HTMLInputElement).disabled).toBe(
      false,
    );
  });
});
