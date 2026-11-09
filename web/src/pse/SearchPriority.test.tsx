import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { addonPreset } from "./AddonCatalogImport";
import { buildTargets, SearchPriority } from "./SearchPriority";
import type { ThemeData } from "./ThemeLibraries";

const apiFetch = vi.fn();
const rebuild = vi.fn();
vi.mock("../lib/api", async (importOriginal) => {
  const orig = await importOriginal<typeof import("../lib/api")>();
  return {
    ...orig,
    apiFetch: (...args: unknown[]) => apiFetch(...args),
    api: { ...orig.api, rebuildCollection: (id: string) => rebuild(id) },
  };
});

const themes = {
  libraries: [
    { id: 4, name: "Services", type: "movie", collections: ["1", "2"], position: 0, section: 1 },
  ],
  collections: [
    { id: "1", name: "Netflix", mediaType: "movie", items: 3, builtIn: false },
    { id: "2", name: "Seerr", mediaType: "movie", items: 2, builtIn: false },
  ],
  presets: [],
  max: 10,
} as ThemeData;
const prio = {
  priorities: [{ kind: "collection" as const, id: "2", level: 2, titles: 2 }],
  total: 2,
  max: 2000,
};

beforeEach(() => {
  apiFetch.mockReset();
  rebuild.mockReset();
  apiFetch.mockImplementation(async (path: string) => {
    if (path.endsWith("priority/search")) return { queued: 2 };
    return path.endsWith("priority") ? prio : themes;
  });
});

it("puts prioritized targets first and filters by name", () => {
  expect(buildTargets(themes, prio, "").map((t) => t.name)).toEqual([
    "Seerr",
    "Netflix",
    "Services",
  ]);
  expect(buildTargets(themes, prio, "net").map((t) => t.name)).toEqual(["Netflix"]);
});

it("saves a level, searches now and refreshes every collection of a library", async () => {
  render(<SearchPriority />);
  const summary = await screen.findByText("Search priority");
  expect(summary.closest("details")).not.toHaveAttribute("open");
  fireEvent.click(summary);
  await screen.findByText("2 priority title(s)");

  fireEvent.click(screen.getByLabelText("Services priority"));
  fireEvent.click(await screen.findByRole("option", { name: /^High/ }));
  await waitFor(() =>
    expect(apiFetch).toHaveBeenCalledWith(
      "/pse/themes/priority",
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({ kind: "library", id: "4", level: 1 }),
      }),
    ),
  );

  const row = screen
    .getByLabelText("Services priority")
    .closest("[data-priority-target]") as HTMLElement;
  const button = (name: string) => within(row).getByRole("button", { name }) as HTMLButtonElement;
  await waitFor(() => expect(button("Search now").disabled).toBe(false));
  fireEvent.click(button("Search now"));
  await waitFor(() =>
    expect(apiFetch).toHaveBeenCalledWith(
      "/pse/themes/priority/search",
      expect.objectContaining({ body: JSON.stringify({ kind: "library", id: "4" }) }),
    ),
  );

  await waitFor(() => expect(button("Refresh").disabled).toBe(false));
  fireEvent.click(button("Refresh"));
  await waitFor(() => expect(rebuild.mock.calls.map((c) => c[0])).toEqual(["1", "2"]));
});

it("recognizes the SeerrCatalog and RSS Catalog add-ons", () => {
  expect(addonPreset("x", ["plugin:community.seerrcatalog/catalog-a"])).toMatchObject({
    refresh: "hourly",
    level: 2,
  });
  expect(addonPreset("x", ["plugin:community.useflowfr.catalog|host/catalog-a"])).toMatchObject({
    refresh: "3h",
    level: 1,
  });
  expect(addonPreset("Cinemeta", ["plugin:com.linvo.cinemeta/top"])).toBeNull();
});
