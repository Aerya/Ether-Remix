import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ReleaseLibraryDashboard } from "./ReleaseLibraryDashboard";
import { apiFetch } from "../lib/api";
vi.mock("../i18n", () => ({ getLocale: () => "fr" }));
vi.mock("./IndexerActivity", () => ({ IndexerActivity: () => <div>Recherches en cours</div> }));
vi.mock("./ReleaseBrowser", () => ({ ReleaseBrowser: () => <div>Explorer l’index</div> }));
vi.mock("../lib/api", () => ({ apiFetch: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
const data = {
  enabled: true,
  references: 10,
  ready: 8,
  expired: 1,
  failed: 1,
  sourceChanged: 0,
  metadata: 15,
  titles: 51,
  lastSeen: 0,
  items: [{ key: "42", title: "Un film", lastSeen: 0 }],
};
it("shows counters, activity, pages and selected releases", async () => {
  vi.mocked(apiFetch).mockResolvedValue(data);
  render(
    <MemoryRouter>
      <ReleaseLibraryDashboard />
    </MemoryRouter>,
  );
  expect(await screen.findByText("Explorer l’index")).toBeVisible();
  expect(screen.getByText("Recherches en cours")).toBeVisible();
});
it("shows compact statistics with a link, without loading activity", async () => {
  vi.mocked(apiFetch).mockResolvedValue(data);
  render(
    <MemoryRouter>
      <ReleaseLibraryDashboard compact />
    </MemoryRouter>,
  );
  expect(await screen.findByRole("link")).toHaveAttribute(
    "href",
    "/remix/indexation?view=releases",
  );
  expect(screen.queryByText("Recherches en cours")).toBeNull();
});
it("does not expose the browser when disabled", async () => {
  vi.mocked(apiFetch).mockResolvedValue({ enabled: false });
  render(
    <MemoryRouter>
      <ReleaseLibraryDashboard />
    </MemoryRouter>,
  );
  expect(await screen.findByText("La bibliothèque de releases est désactivée.")).toBeVisible();
  expect(screen.queryByRole("button")).toBeNull();
});
