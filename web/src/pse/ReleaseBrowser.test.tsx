import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { ReleaseBrowser, periodBounds } from "./ReleaseBrowser";
import { apiFetch } from "../lib/api";
vi.mock("../i18n", async (original) => ({
  ...(await original<typeof import("../i18n")>()),
  getLocale: () => "fr",
}));
vi.mock("../lib/api", () => ({ apiFetch: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
const item = {
  key: "2000000000000002",
  title: "Une série",
  category: "show",
  count: 5,
  firstSeen: 100,
  lastSeen: 200,
};
it("searches the server, combines filters and resets pagination", async () => {
  vi.mocked(apiFetch).mockImplementation(async (url) => ({
    enabled: true,
    items: [item],
    total: 51,
    page: Number(new URL(String(url), "http://local").searchParams.get("page")),
  }));
  render(<ReleaseBrowser />);
  fireEvent.click(await screen.findByRole("button", { name: "Suivant" }));
  await waitFor(() =>
    expect(apiFetch).toHaveBeenLastCalledWith(expect.stringContaining("page=1"), expect.anything()),
  );
  fireEvent.change(screen.getByLabelText("Chercher un média"), { target: { value: "série" } });
  await waitFor(() =>
    expect(apiFetch).toHaveBeenLastCalledWith(
      expect.stringContaining("q=s%C3%A9rie"),
      expect.anything(),
    ),
  );
  expect(String(vi.mocked(apiFetch).mock.lastCall?.[0])).toContain("page=0");
  fireEvent.change(screen.getByLabelText("Catégorie"), { target: { value: "anime" } });
  fireEvent.change(screen.getByLabelText("Période"), { target: { value: "week" } });
  await waitFor(() => expect(String(vi.mocked(apiFetch).mock.lastCall?.[0])).toContain("from="));
  expect(String(vi.mocked(apiFetch).mock.lastCall?.[0])).toContain("category=anime");
});
it("opens every episode of a media and searches global releases", async () => {
  vi.mocked(apiFetch).mockResolvedValue({ enabled: true, items: [item], total: 1, page: 0 });
  render(<ReleaseBrowser />);
  fireEvent.click(await screen.findByRole("button", { name: /Une série/ }));
  await waitFor(() =>
    expect(String(vi.mocked(apiFetch).mock.lastCall?.[0])).toContain("media=" + item.key),
  );
  expect(screen.getByLabelText("Chercher une release")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /^Releases$/ }));
  await waitFor(() =>
    expect(String(vi.mocked(apiFetch).mock.lastCall?.[0])).not.toContain("media="),
  );
});
it("uses local calendar days, Monday weeks and inclusive custom end dates", () => {
  const now = new Date(2026, 10, 18, 12);
  expect(periodBounds("week", "", "", now).from).toBe(
    String(new Date(2026, 10, 16).getTime() / 1000),
  );
  expect(periodBounds("month", "", "", now).from).toBe(
    String(new Date(2026, 10, 1).getTime() / 1000),
  );
  expect(periodBounds("custom", "2026-11-17", "2026-11-17", now)).toEqual({
    from: String(new Date(2026, 10, 17).getTime() / 1000),
    to: String(new Date(2026, 10, 18).getTime() / 1000),
  });
});
it("rejects reversed date ranges without sending them", async () => {
  vi.mocked(apiFetch).mockResolvedValue({ enabled: true, items: [], total: 0, page: 0 });
  render(<ReleaseBrowser />);
  await screen.findByText("Aucun résultat pour ces filtres.");
  fireEvent.change(screen.getByLabelText("Période"), { target: { value: "custom" } });
  fireEvent.change(screen.getByLabelText("Du"), { target: { value: "2026-11-18" } });
  fireEvent.change(screen.getByLabelText("Au inclus"), { target: { value: "2026-11-17" } });
  expect(await screen.findByRole("alert")).toHaveTextContent("La date de fin");
});

it("waits for each preparation batch instead of aborting slow requests", async () => {
  let finish!: (value: unknown) => void;
  vi.mocked(apiFetch)
    .mockResolvedValueOnce({ enabled: true, preparing: true, items: [] })
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }),
    );
  render(<ReleaseBrowser />);
  await waitFor(() => expect(apiFetch).toHaveBeenCalledTimes(2));
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 650));
  });
  expect(apiFetch).toHaveBeenCalledTimes(2);
  await act(async () => {
    finish({ enabled: true, preparing: false, items: [], total: 0, page: 0 });
  });
  expect(await screen.findByText("Aucun résultat pour ces filtres.")).toBeVisible();
});
