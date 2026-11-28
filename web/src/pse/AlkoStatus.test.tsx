import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { apiFetch } from "../lib/api";
import { AlkoStatus, alkoErrorText } from "./AlkoStatus";
vi.mock("../lib/api", () => ({ apiFetch: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});
const state = {
  state: "idle",
  files: 382058,
  movies: 90000,
  episodes: 292058,
  published: 120,
  last: 1795000000,
  journal: "ok",
  langFR: true,
  every: 600,
};
it("tests a disabled source with the saved settings", async () => {
  vi.mocked(apiFetch).mockResolvedValue({ ok: false, message: "Clé refusée", code: "key" });
  render(<AlkoStatus status={{ active: false, problem: "clé de lecture manquante" }} />);
  expect(screen.getByText(/clé de lecture manquante|read key missing/i)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /Test connection|Tester la connexion/ }));
  expect(await screen.findByText("Clé refusée")).toBeTruthy();
  expect(vi.mocked(apiFetch).mock.calls[0]?.[0]).toBe("/pse/alko/test");
});
it("shows the mirror, the journal and the language filter", () => {
  render(<AlkoStatus status={{ active: true, state }} />);
  expect(screen.getByText(/382058 (fichiers|files)/)).toBeTruthy();
  expect(screen.getByText(/120 (publiés|published)/)).toBeTruthy();
  expect(screen.getByText(/Journal à jour|Journal up to date/)).toBeTruthy();
  expect(
    screen.getByText(/FR, MULTi et VOSTFR seulement|French, MULTi and VOSTFR only/),
  ).toBeTruthy();
});
it("shows the progress of a full read and disables the sync button", () => {
  render(
    <AlkoStatus
      status={{ active: true, state: { ...state, state: "syncing", phase: "full", read: 4000 } }}
    />,
  );
  expect(screen.getByText(/4000/)).toBeTruthy();
  const button = screen.getByRole("button", { name: /Synchronisation…|Syncing…/ });
  expect((button as HTMLButtonElement).disabled).toBe(true);
});
it("explains a generic error code without any address or key", () => {
  render(
    <AlkoStatus status={{ active: true, state: { ...state, state: "error", error: "key" } }} />,
  );
  expect(screen.getByRole("alert").textContent).toMatch(/alko-lecture/);
  for (const code of ["key", "old", "views", "unreachable", "protocol", "internal"]) {
    expect(alkoErrorText(code)).not.toMatch(/https?:\/\/|alko-key/);
  }
});
it("starts a sync", async () => {
  vi.mocked(apiFetch).mockResolvedValue({ ok: true, message: "Synchronisation lancée" });
  render(<AlkoStatus status={{ active: true, state }} />);
  fireEvent.click(screen.getByRole("button", { name: /Sync now|Synchroniser maintenant/ }));
  await vi.waitFor(() => expect(apiFetch).toHaveBeenCalled());
  expect(vi.mocked(apiFetch).mock.calls[0]?.[0]).toBe("/pse/alko/sync");
});
