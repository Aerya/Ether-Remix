import { afterEach, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
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
it("handles an unavailable index response from an older server", async () => {
  vi.mocked(apiFetch).mockResolvedValue({ enabled: false });
  render(
    <MemoryRouter>
      <ReleaseLibraryDashboard />
    </MemoryRouter>,
  );
  expect(await screen.findByText("L’index des releases est indisponible.")).toBeVisible();
  expect(screen.queryByRole("button")).toBeNull();
});

it("keeps the explorer mounted when a counter refresh fails", async () => {
  const interval = vi.spyOn(window, "setInterval");
  vi.mocked(apiFetch).mockResolvedValue(data);
  render(
    <MemoryRouter>
      <ReleaseLibraryDashboard />
    </MemoryRouter>,
  );
  const explorer = await screen.findByText("Explorer l’index");
  await screen.findByText("10");
  vi.mocked(apiFetch).mockRejectedValue(new Error("timeout"));
  await act(async () => {
    (interval.mock.calls.find((call) => call[1] === 60000)![0] as () => void)();
  });
  expect(await screen.findByRole("alert")).toHaveTextContent("dernières valeurs conservées");
  expect(screen.getByText("Explorer l’index")).toBe(explorer);
  interval.mockRestore();
});
it("shows the explorer even if the initial counters fail", async () => {
  vi.mocked(apiFetch).mockRejectedValue(new Error("timeout"));
  render(
    <MemoryRouter>
      <ReleaseLibraryDashboard />
    </MemoryRouter>,
  );
  await screen.findByRole("alert");
  expect(screen.getByText("Explorer l’index")).toBeVisible();
});

it("shows audit progress without requiring counters and keeps the explorer mounted", async () => {
  vi.mocked(apiFetch).mockResolvedValue({ enabled: true, auditPending: true });
  render(
    <MemoryRouter>
      <ReleaseLibraryDashboard />
    </MemoryRouter>,
  );
  expect(
    await screen.findByText(/Vérification des associations média\/release en cours/),
  ).toBeVisible();
  expect(screen.getByText("Explorer l’index")).toBeVisible();
});
it("explains an audit failure instead of claiming progress", async () => {
  vi.mocked(apiFetch).mockResolvedValue({ enabled: true, auditPending: true, auditFailed: true });
  render(
    <MemoryRouter>
      <ReleaseLibraryDashboard />
    </MemoryRouter>,
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "La vérification des associations a échoué",
  );
});
