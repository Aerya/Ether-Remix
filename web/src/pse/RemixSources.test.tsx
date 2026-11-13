import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { MemoryRouter, useLocation } from "react-router-dom";
import { RemixSources } from "./RemixSources";
import { IPTVProviders } from "../pages/sources/IPTV";

const sources = vi.hoisted(() => vi.fn());
vi.mock("../pages/livetv/model", async (original) => ({
  ...(await original<typeof import("../pages/livetv/model")>()),
  tv: { sources },
}));
vi.mock("../pages/livetv/SourceEditor", () => ({
  SourceEditor: ({
    source,
    onSaved,
    onDeleted,
  }: {
    source: { id: string; name: string } | null;
    onSaved: (s: { id: string }) => void;
    onDeleted: () => void;
  }) => (
    <div>
      <p>{source ? `Editing ${source.name}` : "Creating IPTV"}</p>
      <button onClick={() => onSaved({ id: source?.id ?? "new-provider" })}>Save fixture</button>
      <button onClick={onDeleted}>Delete fixture</button>
    </div>
  ),
}));
vi.mock("../components/Drawer", () => ({
  Drawer: ({
    open,
    children,
    onClose,
  }: {
    open: boolean;
    children: React.ReactNode;
    onClose: () => void;
  }) =>
    open ? (
      <div role="dialog">
        {children}
        <button onClick={onClose}>Close fixture</button>
      </div>
    ) : null,
}));
vi.mock("../components/addons/AddAddonDrawer", () => ({ AddAddonDrawer: () => null }));
vi.mock("../pages/sources/Indexers", () => ({ IndexerList: () => <p>Stremio fixtures</p> }));
vi.mock("../pages/sources/Debrid", () => ({ DebridSources: () => null }));
vi.mock("../pages/sources/Usenet", () => ({ UsenetProviders: () => null }));
vi.mock("../components/ProwlarrImport", () => ({ ProwlarrImport: () => null }));
vi.mock("../pages/catalog/NewReleasesSettings", () => ({ NewReleaseBudgetSection: () => null }));
vi.mock("../pages/sources/cards", () => ({
  AddSourceGallery: ({ open, onPick }: { open: boolean; onPick: (v: unknown) => void }) =>
    open ? (
      <button onClick={() => onPick({ type: "iptv", kind: "xtream" })}>
        Choose Xtream fixture
      </button>
    ) : null,
}));
function Location() {
  return <output data-testid="location">{useLocation().search}</output>;
}
const provider = { id: "king-123", name: "King", auth: { type: "xtream" }, channelCount: 276 };
beforeEach(() => {
  sources.mockReset();
  sources.mockResolvedValue([provider]);
});
function remix(search = "?source=iptv") {
  return render(
    <MemoryRouter initialEntries={["/remix/sources" + search]}>
      <RemixSources music={null} indexing={null} />
      <Location />
    </MemoryRouter>,
  );
}
it("opens the existing IPTV provider without treating its category as a provider ID", async () => {
  remix();
  await screen.findByText("King");
  expect(screen.queryByText(/Suggested providers/)).toBeNull();
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Edit King" }));
  expect(await screen.findByText("Editing King")).toBeTruthy();
  expect(screen.getByTestId("location").textContent).toBe("?source=iptv&iptvSource=king-123");
  fireEvent.click(screen.getByText("Save fixture"));
  await waitFor(() => expect(sources).toHaveBeenCalledTimes(2));
  expect(screen.getByText("Editing King")).toBeTruthy();
  fireEvent.click(screen.getByText("Close fixture"));
  expect(screen.getByTestId("location").textContent).toBe("?source=iptv");
  expect(screen.getByRole("tab", { name: "IPTV" }).getAttribute("aria-selected")).toBe("true");
});
it("creates an IPTV source, opens its saved record and keeps the category after deletion", async () => {
  remix("?source=stremio");
  fireEvent.click(screen.getByText("Add a source"));
  fireEvent.click(screen.getByText("Choose Xtream fixture"));
  expect(await screen.findByText("Creating IPTV")).toBeTruthy();
  sources.mockResolvedValue([{ ...provider, id: "new-provider" }]);
  fireEvent.click(screen.getByText("Save fixture"));
  expect(await screen.findByText("Editing King")).toBeTruthy();
  expect(screen.getByTestId("location").textContent).toBe("?source=iptv&iptvSource=new-provider");
  sources.mockResolvedValue([]);
  fireEvent.click(screen.getByText("Delete fixture"));
  await screen.findByText("No channel sources yet");
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByTestId("location").textContent).toBe("?source=iptv");
});
it("supports IPTV deep links and clears the editor when switching source categories", async () => {
  remix("?source=iptv&iptvSource=king-123");
  expect(await screen.findByText("Editing King")).toBeTruthy();
  fireEvent.click(screen.getByRole("tab", { name: "Stremio" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByRole("tab", { name: "IPTV" }));
  expect(screen.queryByRole("dialog")).toBeNull();
});
it("retains the native Ether IPTV source query outside Remix", async () => {
  render(
    <MemoryRouter initialEntries={["/sources/iptv?source=king-123"]}>
      <IPTVProviders />
      <Location />
    </MemoryRouter>,
  );
  expect(await screen.findByText("Editing King")).toBeTruthy();
  fireEvent.click(screen.getByText("Close fixture"));
  expect(screen.getByTestId("location").textContent).toBe("");
});

it("renders torrent clients in their own source category", () => {
  render(
    <MemoryRouter initialEntries={["/remix/sources?source=torrent"]}>
      <RemixSources
        music={null}
        indexing={<p>Indexing fixture</p>}
        torrent={<p>Torrent clients fixture</p>}
      />
    </MemoryRouter>,
  );
  expect(screen.getByText("Torrent clients fixture")).toBeTruthy();
  expect(screen.queryByText("Indexing fixture")).toBeNull();
  expect(screen.getByRole("tab", { name: "Torrent client" }).getAttribute("aria-selected")).toBe(
    "true",
  );
});
