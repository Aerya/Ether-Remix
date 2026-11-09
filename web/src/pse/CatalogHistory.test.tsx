import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { CatalogHistory } from "./CatalogHistory";

const apiFetch = vi.fn();
vi.mock("../lib/api", () => ({ apiFetch: (...args: unknown[]) => apiFetch(...args) }));
afterEach(() => {
  apiFetch.mockReset();
});

const snapshot = {
  at: 1800000000,
  period: "day",
  tmdb: { movies: 1000, shows: 500, exportDate: "2026-11-08", updatedAt: 1800000000 },
  history: [
    {
      start: "2026-11-08",
      movies: 10,
      shows: 2,
      movieSearches: 30,
      showSearches: 5,
      cumulativeMovies: 100,
      cumulativeShows: 20,
      cumulativeMovieSearches: 200,
      cumulativeShowSearches: 50,
    },
    {
      start: "2026-11-09",
      movies: 5,
      shows: 3,
      movieSearches: 10,
      showSearches: 2,
      cumulativeMovies: 105,
      cumulativeShows: 23,
      cumulativeMovieSearches: 210,
      cumulativeShowSearches: 52,
    },
  ],
  sources: [
    { id: "a", name: "Prowlarr" },
    { id: "b", name: "AIOStreams" },
  ],
  searchTrackingSince: "2026-11-08",
  undated: 0,
};

it("shows dated TMDB totals and switches between period and lifetime cumulative counts", async () => {
  apiFetch.mockResolvedValue(snapshot);
  render(<CatalogHistory localMovies={100} localShows={50} />);
  expect(await screen.findByText(/1,000/)).toBeTruthy();
  expect(screen.getByText(/2026-11-08/)).toBeTruthy();
  let chart = screen.getByRole("img");
  expect(chart.getAttribute("aria-label")).toContain("10 movies");
  fireEvent.click(screen.getByRole("button", { name: "Cumulative" }));
  chart = screen.getByRole("img");
  expect(chart.getAttribute("aria-label")).toContain("100 movies");
  fireEvent.change(screen.getByLabelText("History media type"), { target: { value: "show" } });
  expect(screen.getByRole("img").getAttribute("aria-label")).toContain("0 movies");
  expect(screen.getByRole("img").getAttribute("aria-label")).toContain("23 shows");
});

it("requests year aggregation and source filtering independently of movie/show selection", async () => {
  apiFetch.mockImplementation(async (path: string) => ({
    ...snapshot,
    period: path.includes("period=year") ? "year" : "day",
  }));
  render(<CatalogHistory localMovies={100} localShows={50} />);
  await screen.findByRole("img");
  fireEvent.change(screen.getByLabelText("History metric"), { target: { value: "searches" } });
  fireEvent.change(screen.getByLabelText("History period"), { target: { value: "year" } });
  await waitFor(() =>
    expect(apiFetch.mock.calls.some(([path]) => String(path).includes("period=year"))).toBe(true),
  );
  fireEvent.change(screen.getByLabelText("History source"), { target: { value: "a" } });
  await waitFor(() =>
    expect(apiFetch.mock.calls.some(([path]) => String(path).includes("source=a"))).toBe(true),
  );
  await screen.findByRole("img");
  fireEvent.change(screen.getByLabelText("History metric"), { target: { value: "catalogue" } });
  expect(screen.queryByLabelText("History source")).toBeNull();
});

it("marks unmeasured searches as unknown instead of inventing historical zeros", async () => {
  apiFetch.mockResolvedValue({ ...snapshot, searchTrackingSince: "" });
  render(<CatalogHistory localMovies={100} localShows={50} />);
  await screen.findByRole("img");
  fireEvent.change(screen.getByLabelText("History metric"), { target: { value: "searches" } });
  expect(screen.getByText(/Source tracking starts/)).toBeTruthy();
  const rows = within(screen.getByRole("table")).getAllByRole("row");
  expect(
    within(rows[1]!)
      .getAllByRole("cell")
      .every((cell) => cell.textContent === "—"),
  ).toBe(true);
});

it("requests the anime subset with a source and marks older searches as unmeasured", async () => {
  apiFetch.mockResolvedValue({ ...snapshot, searchTrackingSince: "2026-11-09" });
  render(<CatalogHistory localMovies={100} localShows={50} />);
  await screen.findByRole("img");
  fireEvent.change(screen.getByLabelText("History media type"), { target: { value: "anime" } });
  fireEvent.change(screen.getByLabelText("History metric"), { target: { value: "searches" } });
  fireEvent.change(screen.getByLabelText("History source"), { target: { value: "b" } });
  await waitFor(() =>
    expect(
      apiFetch.mock.calls.some(([path]) => String(path).includes("source=b&media=anime")),
    ).toBe(true),
  );
  expect(await screen.findByText(/Anime: a subset/)).toBeTruthy();
  await waitFor(() =>
    expect(screen.getByRole("img").getAttribute("aria-label")).toContain("not measured"),
  );
});
