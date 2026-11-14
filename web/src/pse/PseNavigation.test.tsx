import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { expect, it, vi } from "vitest";
import PsePage from "./PsePage";

const apiFetch = vi.fn();
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => apiFetch(...args),
}));
vi.mock("../i18n", async (original) => ({
  ...(await original<typeof import("../i18n")>()),
  getLocale: () => "fr",
}));
vi.mock("./VostfrTitles", () => ({ VostfrTitles: () => <div>Titres en VOSTFR à la demande</div> }));
vi.mock("./RemixSources", () => ({
  RemixSources: ({ indexing, music }: { indexing: React.ReactNode; music: React.ReactNode }) => (
    <>
      {indexing}
      {music}
    </>
  ),
}));
vi.mock("./IndexerDetails", () => ({ IndexerDetails: () => <div>Tableau statistiques</div> }));
vi.mock("./SearchPriority", () => ({ SearchPriority: () => null }));
vi.mock("./OwnedMedia", () => ({ IBroadcastSource: () => null }));
vi.mock("./MusicAccess", () => ({ MusicAccess: () => null }));
vi.mock("./MusicPlaylists", () => ({ MusicPlaylists: () => null }));
vi.mock("./SourceOrder", () => ({ SourceOrder: () => null }));
const txt = (s: string) => ({ fr: s, en: s });

it("orders tabs and lets vertically listed imported sources be selected and saved", async () => {
  const data = {
    features: [
      {
        id: "indexer",
        title: txt("Indexation"),
        description: txt(""),
        fields: [
          {
            key: "PSE_INDEXER_SOURCES",
            kind: "multi",
            label: txt("Sources importées"),
            help: txt("Sources sélectionnées"),
            default: "",
            value: "",
            source: "console",
            options: [
              { value: "a", label: txt("Prowlarr") },
              { value: "b", label: txt("AIOStreams") },
            ],
          },
        ],
      },
    ],
  };
  apiFetch.mockResolvedValue(data);
  render(
    <MemoryRouter initialEntries={["/remix/sources?source=indexation"]}>
      <Routes>
        <Route path="/remix/:tab" element={<PsePage />} />
      </Routes>
    </MemoryRouter>,
  );
  const source = await screen.findByLabelText("Prowlarr");
  fireEvent.click(source.closest("details")!.querySelector("summary")!);
  const tabs = screen.getAllByRole("tab").map((tab) => tab.textContent);
  expect(tabs).toEqual([
    "Sources",
    "Priorités",
    "Langues et qualité",
    "VOSTFR à la demande",
    "Contenus et bibliothèques",
    "Médias locaux",
    "Statistiques",
    "Notifications et suivi",
  ]);
  expect(screen.getByRole("table")).toBeTruthy();
  fireEvent.click(screen.getByLabelText("Prowlarr"));
  fireEvent.click(screen.getByRole("button", { name: /Save|Enregistrer/ }));
  await waitFor(() =>
    expect(
      apiFetch.mock.calls.some(
        ([path, init]) =>
          path === "/pse/" &&
          (init as RequestInit)?.method === "PUT" &&
          String((init as RequestInit)?.body).includes('"PSE_INDEXER_SOURCES":"a"'),
      ),
    ).toBe(true),
  );
});

it("isolates VOSTFR exceptions in the on-demand tab", async () => {
  apiFetch.mockResolvedValue({ features: [] });
  render(
    <MemoryRouter initialEntries={["/remix/demande"]}>
      <Routes>
        <Route path="/remix/:tab" element={<PsePage />} />
      </Routes>
    </MemoryRouter>,
  );
  expect(await screen.findByText("Titres en VOSTFR à la demande")).toBeTruthy();
});

it("redirects a legacy indexing anchor to its new source category", async () => {
  apiFetch.mockResolvedValue({
    features: [{ id: "indexer", title: txt("Indexation"), description: txt(""), fields: [] }],
  });
  render(
    <MemoryRouter initialEntries={["/remix/films#pse-indexer"]}>
      <Routes>
        <Route path="/remix/:tab" element={<PsePage />} />
      </Routes>
    </MemoryRouter>,
  );
  expect(await screen.findByRole("heading", { name: "Indexation" })).toBeTruthy();
});

it("opens statistics when entering Remix without a tab", async () => {
  apiFetch.mockResolvedValue({ features: [] });
  render(
    <MemoryRouter initialEntries={["/remix"]}>
      <Routes>
        <Route path="/remix/:tab?" element={<PsePage />} />
      </Routes>
    </MemoryRouter>,
  );
  expect(await screen.findByText("Tableau statistiques")).toBeTruthy();
  expect(screen.getByRole("tab", { name: "Statistiques" }).getAttribute("aria-selected")).toBe(
    "true",
  );
});

it("redirige les anciens favoris en conservant paramètres et ancre", async () => {
  apiFetch.mockResolvedValue({ features: [] });
  render(
    <MemoryRouter
      initialEntries={["/accent/sources?source=musique&musicGroup=ibroadcast#music-ibroadcast"]}
    >
      <Routes>
        <Route path="/accent/:tab?" element={<PsePage />} />
        <Route path="/remix/:tab?" element={<LocationProbe />} />
      </Routes>
    </MemoryRouter>,
  );
  expect(
    await screen.findByText("/remix/sources?source=musique&musicGroup=ibroadcast#music-ibroadcast"),
  ).toBeTruthy();
});

function LocationProbe() {
  const l = useLocation();
  return <p>{l.pathname + l.search + l.hash}</p>;
}
it("réserve les informations Subsonic et bibliothèque aux contenus musicaux", async () => {
  apiFetch.mockResolvedValue({
    features: [
      {
        id: "music",
        title: txt("Musique"),
        description: txt(""),
        fields: [],
        status: { active: true, library: { artist: 2, album: 3, track: 4 } },
      },
    ],
  });
  const page = (path: string) => (
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/remix/:tab" element={<PsePage />} />
      </Routes>
    </MemoryRouter>
  );
  const sources = render(page("/remix/sources?source=musique"));
  await screen.findByRole("tab", { name: "Eclipse" });
  expect(screen.queryByText(/Adresse Subsonic/)).toBeNull();
  expect(screen.queryByText(/2 artistes, 3 albums, 4 titres/)).toBeNull();
  sources.unmount();
  render(page("/remix/contenus?content=musique"));
  expect(await screen.findByText(/Adresse Subsonic/)).toBeTruthy();
  const library = screen.getByText(/2 artistes, 3 albums, 4 titres/);
  expect(library.closest("details")).not.toHaveAttribute("open");
});
