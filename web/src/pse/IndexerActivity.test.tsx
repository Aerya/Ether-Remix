import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { IndexerActivity } from "./IndexerActivity";

const apiFetch = vi.fn();
vi.mock("../lib/api", () => ({ apiFetch: (...args: unknown[]) => apiFetch(...args) }));
afterEach(() => {
  vi.useRealTimers();
  apiFetch.mockReset();
});

const event = {
  id: 1,
  sourceId: "a",
  source: "Source A",
  title: "Movie A",
  type: "movie",
  started: 1000,
  status: "searching",
  results: 0,
  durationMs: 0,
};
const source = {
  id: "a",
  name: "Source A",
  enabled: true,
  selected: true,
  limit: 100,
  hits: 12,
  titles: 10,
  releases: 20,
  searches: 4,
  errors: 1,
  partial: 0,
  denied: 0,
  results: 20,
  durationMs: 4000,
};
const overview = {
  at: 4000,
  sources: [source, { ...source, id: "b", name: "Source B" }],
  history: [{ day: "2026-11-08", sourceId: "a", hits: 12, searches: 4, errors: 1 }],
};
const flush = async () => {
  await act(async () => {
    await Promise.resolve();
  });
};

it("updates running searches, retains data on error and stops polling after unmount", async () => {
  vi.useFakeTimers();
  let live = { at: 4000, state: "searching", active: [event], recent: [] as (typeof event)[] };
  let failed = false;
  apiFetch.mockImplementation(async (path: string) => {
    if (failed) throw new Error("private upstream");
    return path.endsWith("/sources") ? overview : live;
  });
  const view = render(<IndexerActivity />);
  await flush();
  expect(screen.getByText("Movie A")).toBeTruthy();
  expect(screen.getByText(/3 s ·/)).toBeTruthy();
  live = {
    at: 7000,
    state: "waiting",
    active: [],
    recent: [{ ...event, status: "empty", durationMs: 2000 }],
  };
  await act(async () => {
    await vi.advanceTimersByTimeAsync(3000);
  });
  expect(screen.getByText(/No results|Aucun résultat/)).toBeTruthy();
  failed = true;
  await act(async () => {
    await vi.advanceTimersByTimeAsync(3000);
  });
  expect(screen.getByRole("alert").textContent).toMatch(/Last received|dernières données/);
  expect(screen.getByText("Movie A")).toBeTruthy();
  expect(screen.queryByText("private upstream")).toBeNull();
  const count = apiFetch.mock.calls.length;
  view.unmount();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(apiFetch).toHaveBeenCalledTimes(count);
});

it("filters sources and pauses only the display updates", async () => {
  vi.useFakeTimers();
  apiFetch.mockImplementation(async (path: string) =>
    path.endsWith("/sources")
      ? overview
      : {
          at: 4000,
          state: "waiting",
          active: [],
          recent: [event, { ...event, id: 2, sourceId: "b", source: "Source B", title: "Movie B" }],
        },
  );
  render(<IndexerActivity />);
  await flush();
  fireEvent.change(screen.getByRole("combobox"), { target: { value: "a" } });
  expect(screen.queryByText("Movie B")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /Pause updates|Suspendre le suivi/ }));
  await flush();
  const count = apiFetch.mock.calls.length;
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(apiFetch).toHaveBeenCalledTimes(count);
  expect(apiFetch.mock.calls.every(([path]) => String(path).startsWith("/pse/indexer/"))).toBe(
    true,
  );
});

it.each([
  [/^Source$/, ["Alpha", "Zulu"], ["Zulu", "Alpha"]],
  [/Requests \/ quota|Requêtes \/ quota/, ["Zulu", "Alpha"], ["Alpha", "Zulu"]],
  [/^Searches$|^Recherches$/, ["Alpha", "Zulu"], ["Zulu", "Alpha"]],
  [/Errors \/ partial|Erreurs \/ partielles/, ["Zulu", "Alpha"], ["Alpha", "Zulu"]],
  [/Quota denials|Quotas refusés/, ["Alpha", "Zulu"], ["Zulu", "Alpha"]],
  [/Average duration|Durée moyenne/, ["Zulu", "Alpha"], ["Alpha", "Zulu"]],
  [/Stored titles|Titres en stock/, ["Zulu", "Alpha"], ["Alpha", "Zulu"]],
])("sorts source rows in both directions using %s", async (column, first, second) => {
  vi.useFakeTimers();
  const data = {
    ...overview,
    sources: [
      {
        ...source,
        id: "z",
        name: "Zulu",
        hits: 100,
        searches: 2,
        errors: 0,
        partial: 3,
        denied: 1,
        durationMs: 10000,
        titles: 100,
      },
      {
        ...source,
        id: "a",
        name: "Alpha",
        hits: 20,
        searches: 10,
        errors: 2,
        partial: 0,
        denied: 5,
        durationMs: 20000,
        titles: 20,
      },
    ],
  };
  apiFetch.mockImplementation(async (path: string) =>
    path.endsWith("/sources") ? data : { at: 4000, state: "waiting", active: [], recent: [] },
  );
  render(<IndexerActivity />);
  await flush();
  const table = screen.getByRole("table");
  const names = () =>
    within(table)
      .getAllByRole("rowheader")
      .map((row) => row.querySelector("span")?.textContent);
  const button = within(table).getByRole("button", { name: column });
  // La colonne Source est active par défaut : on sélectionne une autre colonne avant le test.
  if (String(column) === String(/^Source$/))
    fireEvent.click(within(table).getByRole("button", { name: /Stored titles|Titres en stock/ }));
  fireEvent.click(button);
  expect(names()).toEqual(first);
  const header = button.closest("th")!;
  expect(header.getAttribute("aria-sort")).toBe(
    String(column) === String(/^Source$/) ? "ascending" : "descending",
  );
  fireEvent.click(button);
  expect(names()).toEqual(second);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(33000);
  });
  expect(names()).toEqual(second);
});

it("keeps sources without a measured duration last in both directions", async () => {
  vi.useFakeTimers();
  apiFetch.mockImplementation(async (path: string) =>
    path.endsWith("/sources")
      ? {
          ...overview,
          sources: [
            { ...source, id: "missing", name: "No searches", searches: 0 },
            { ...source, id: "measured", name: "Measured source" },
          ],
        }
      : { at: 4000, state: "waiting", active: [], recent: [] },
  );
  render(<IndexerActivity />);
  await flush();
  const table = screen.getByRole("table");
  const button = within(table).getByRole("button", { name: /Average duration|Durée moyenne/ });
  for (let i = 0; i < 2; i++) {
    fireEvent.click(button);
    const rows = within(table).getAllByRole("rowheader");
    expect(rows[0]!.textContent).toContain("Measured source");
    expect(rows[1]!.textContent).toContain("No searches");
  }
});
