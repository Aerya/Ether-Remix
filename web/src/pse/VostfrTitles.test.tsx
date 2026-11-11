import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import { VostfrTitles } from "./VostfrTitles";

const apiFetch = vi.fn();
vi.mock("../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => apiFetch(...args),
}));
vi.mock("../lib/titles", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/titles")>()),
  useTitle: () => undefined,
}));

let submaker: { available: boolean; enabled: boolean; titles: { key: string; state: string }[] };
const puts: unknown[] = [];

beforeEach(() => {
  puts.length = 0;
  apiFetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if (path === "/pse/themes/vostfr") return { keys: ["1000000000000010"], max: 500 };
    if (path === "/pse/submaker/vostfr") {
      if (init?.method === "PUT") {
        const body = JSON.parse(String(init.body)) as { enabled: boolean };
        puts.push(body);
        submaker = { ...submaker, enabled: body.enabled };
        return { ...submaker, queued: 1 };
      }
      return submaker;
    }
    return {};
  });
});

const show = () =>
  render(
    <MemoryRouter>
      <VostfrTitles />
    </MemoryRouter>,
  );

it("turns SubMaker on for VOSTFR titles and shows their subtitle state", async () => {
  submaker = {
    available: true,
    enabled: false,
    titles: [{ key: "1000000000000010", state: "translated" }],
  };
  show();
  const toggle = await screen.findByRole("switch", { name: /SubMaker/ });
  expect(screen.queryByText(/SRT traduit|Translated SRT/)).toBeNull();
  fireEvent.click(toggle);
  await waitFor(() => expect(puts).toEqual([{ enabled: true }]));
  expect(await screen.findByText(/SRT traduit|Translated SRT/)).toBeTruthy();
});

it("disables the switch and links to the settings while SubMaker is not configured", async () => {
  submaker = { available: false, enabled: false, titles: [] };
  show();
  const toggle = await screen.findByRole("switch", { name: /SubMaker/ });
  expect((toggle as HTMLInputElement).disabled).toBe(true);
  expect(screen.getByRole("link", { name: /SubMaker/ }).getAttribute("href")).toBe(
    "/remix/sources?source=stremio#submaker-integration",
  );
});

it("hides the switch when SubMaker is not loaded", async () => {
  apiFetch.mockImplementation(async (path: string) => {
    if (path === "/pse/themes/vostfr") return { keys: [], max: 500 };
    throw new Error("404");
  });
  show();
  await screen.findByText(/Aucun titre|No titles/);
  expect(screen.queryByRole("switch")).toBeNull();
});
