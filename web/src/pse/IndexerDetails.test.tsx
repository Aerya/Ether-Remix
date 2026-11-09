import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { IndexerDetails } from "./IndexerDetails";

vi.mock("./CatalogHistory", () => ({ CatalogHistory: () => null }));
vi.mock("./IndexerActivity", () => ({ IndexerActivity: () => null }));
const apiFetch = vi.fn();
vi.mock("../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => apiFetch(...args),
}));

const c = (
  titles: number,
  indexed: number,
  found: number,
  playable: number,
  recent = 0,
  stale = 0,
  failing = 0,
) => ({
  titles,
  indexed,
  found,
  playable,
  recent,
  stale,
  failing,
});

it("loads per type, library and collection details", async () => {
  apiFetch.mockResolvedValue({
    at: 1800000000,
    min: 2,
    types: { movie: c(100, 40, 30, 20, 5, 7, 4), show: c(50, 10, 4, 2), anime: c(12, 6, 4, 2) },
    libraries: [
      {
        id: "",
        name: "Studios & labels",
        ...c(30, 15, 10, 8),
        rows: [{ id: "a", name: "Pixar", ...c(20, 10, 8, 6) }],
      },
    ],
    collections: [{ id: "a", name: "Pixar", type: "movie", ...c(20, 10, 8, 6) }],
    empty: 3,
  });
  render(<IndexerDetails />);
  expect(await screen.findByText("Studios & labels")).toBeTruthy();
  expect(apiFetch.mock.calls[0]![0]).toBe("/pse/indexer/details");
  expect(screen.getAllByText("Pixar")).toHaveLength(2);
  expect(screen.getByText("150")).toBeTruthy(); // total films + séries, sans ajouter le sous-ensemble anime
  expect(screen.getByText("Anime")).toBeTruthy();
  expect(screen.queryByText("162")).toBeNull();
  expect(screen.getByText(/3 collection/)).toBeTruthy();
  expect(screen.getAllByText("Old observations").length).toBeGreaterThan(0);
  expect(screen.getAllByText("7").length).toBeGreaterThan(0); // observations anciennes des films
});
