import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import type { DashboardData } from "../lib/types";
import PsePage from "./PsePage";
import { buildDiagnostic } from "./RemixDiagnostic";
import { buildNavigation } from "../App";
import {
  REMIX_NAV,
  REMIX_EXPERT_HREF,
  filterRemixNav,
  isSimpleRoute,
  pseNavLabel,
  remixHref,
} from "./navigation";
import { RemixExpertToggle, useRemixExpertView } from "./RemixNavigation";
import { remixDescriptors } from "./mock";

const apiFetch = vi.fn();
const app = vi.hoisted(() => ({
  dashboard: null as DashboardData | null,
  health: [] as unknown[],
}));
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => apiFetch(...args),
}));
vi.mock("../app-context", () => ({
  useApp: () => ({ dashboard: app.dashboard, health: app.health, refreshDashboard: vi.fn() }),
}));
vi.mock("../i18n", async (original) => ({
  ...(await original<typeof import("../i18n")>()),
  getLocale: () => "fr",
}));

vi.mock("../pages/sources/Indexers", () => ({ IndexerList: () => null }));
vi.mock("./IndexerGroups", () => ({ IndexerGroups: () => null }));
vi.mock("../pages/sources/Debrid", () => ({ DebridSources: () => null }));
vi.mock("../pages/sources/Usenet", () => ({ UsenetProviders: () => null }));
vi.mock("../pages/sources/IPTV", () => ({ IPTVProviders: () => null }));
vi.mock("./LocalMediaHealth", () => ({ LocalMediaHealth: () => null }));
vi.mock("./OwnedMedia", () => ({ LocalMedia: () => null, IBroadcastSource: () => null }));
vi.mock("../components/ProwlarrImport", () => ({ ProwlarrImport: () => null }));
vi.mock("../pages/libraries/browser/NewReleasesSettings", () => ({
  NewReleaseBudgetSection: () => null,
}));
vi.mock("../pages/sources/cards", () => ({ AddSourceGallery: () => null }));
vi.mock("./Submaker", () => ({ Submaker: () => null }));
vi.mock("./IndexerActivity", () => ({ IndexerActivity: () => null }));
vi.mock("./SearchPriority", () => ({ SearchPriority: () => null }));
vi.mock("./IndexerDetails", () => ({ IndexerDetails: () => null }));
vi.mock("./ReleaseLibraryDashboard", () => ({ ReleaseLibraryDashboard: () => null }));

const dashboard = (sources: boolean): DashboardData => ({
  sections: {},
  names: [],
  setupComplete: true,
  version: "test",
  setupSteps: [
    { id: "sources", title: "Sources", complete: sources, required: true },
    { id: "catalog", title: "TMDB", complete: true, required: true },
  ],
});
function Probe() {
  const l = useLocation();
  return <p data-testid="where">{l.pathname + l.search}</p>;
}
const page = (path: string) => (
  <MemoryRouter initialEntries={[path]}>
    <Routes>
      <Route path="/remix/:tab?" element={<PsePage />} />
      <Route path="/apps" element={<Probe />} />
    </Routes>
  </MemoryRouter>
);

beforeEach(() => {
  window.localStorage.clear();
  apiFetch.mockReset();
  apiFetch.mockResolvedValue({ features: [] });
  app.dashboard = dashboard(true);
  app.health = [];
});

it("ouvre l’accueil, sans barre d’onglets, avec le titre de la page", async () => {
  window.localStorage.setItem("remix.setup.wizard", "1");
  render(page("/remix"));
  expect(await screen.findByText("3 étapes pour regarder un film")).toBeTruthy();
  expect(screen.getByRole("heading", { name: "Démarrage", level: 1 })).toBeTruthy();
  expect(screen.queryAllByRole("tab")).toHaveLength(0);
  expect(screen.getByRole("link", { name: /Relancer l’assistant/ })).toBeTruthy();
});

it("lance l’assistant une seule fois quand aucune source n’existe", async () => {
  app.dashboard = dashboard(false);
  render(page("/remix/accueil"));
  expect(await screen.findByText("Où trouver vos films et séries ?")).toBeTruthy();
  expect(window.localStorage.getItem("remix.setup.wizard")).toBe("1");
});

it("enregistre le choix de langue de l’assistant puis passe à l’application", async () => {
  render(page("/remix/assistant?step=2"));
  fireEvent.click(await screen.findByRole("radio", { name: /Français/ }));
  fireEvent.click(screen.getByRole("button", { name: "Suivant" }));
  await waitFor(() =>
    expect(
      apiFetch.mock.calls.some(
        ([path, init]) =>
          path === "/pse/" &&
          (init as RequestInit)?.method === "PUT" &&
          String((init as RequestInit)?.body).includes('"PSE_FRENCH":"on"'),
      ),
    ).toBe(true),
  );
  expect(await screen.findByText("Connectez votre application")).toBeTruthy();
});

function Sidebar() {
  const expert = useRemixExpertView();
  const nav = filterRemixNav(buildNavigation(remixDescriptors), expert);
  return (
    <ul aria-label="barre">
      {nav
        .flatMap((g) => g.items)
        .map((i) =>
          i.href === REMIX_EXPERT_HREF ? (
            <li key={i.href}>
              <RemixExpertToggle>{i.label}</RemixExpertToggle>
            </li>
          ) : (
            <li key={i.href}>{i.label}</li>
          ),
        )}
    </ul>
  );
}
const sidebar = (path: string) => (
  <MemoryRouter initialEntries={[path]}>
    <Routes>
      <Route path="*" element={<Sidebar />} />
    </Routes>
  </MemoryRouter>
);
const labels = () => screen.getAllByRole("listitem").map((l) => l.textContent);

it("la barre latérale affiche les entrées du mode simple par défaut", () => {
  render(sidebar("/remix/accueil"));
  expect(labels()).toEqual([
    "Démarrage",
    "Sources",
    "Langues et qualité",
    "Contenus",
    "Diagnostic",
    "Expert",
  ]);
});

it("la barre latérale affiche les entrées Expert en mode Expert, sans Langues et qualité", () => {
  window.localStorage.setItem("remix.mode", "expert");
  render(sidebar("/remix/accueil"));
  expect(labels()).toEqual([
    "Démarrage",
    "Sources",
    "Indexation",
    "Lecture",
    "Contenus",
    "Visuels",
    "Suivi",
    "Statistiques",
    "Diagnostic",
    "Expert",
  ]);
});

it("l’interrupteur Expert mémorise le choix par navigateur", () => {
  render(sidebar("/remix/accueil"));
  const toggle = screen.getByRole("button", { name: "Mode expert" });
  expect(toggle.getAttribute("aria-pressed")).toBe("false");
  fireEvent.click(toggle);
  expect(window.localStorage.getItem("remix.mode")).toBe("expert");
  expect(screen.getByRole("button", { name: "Mode expert" }).getAttribute("aria-pressed")).toBe(
    "true",
  );
  fireEvent.click(screen.getByRole("button", { name: "Mode expert" }));
  expect(window.localStorage.getItem("remix.mode")).toBe("simple");
});

it("affiche les entrées Expert sur une page réservée à l’expert sans changer le mode mémorisé", () => {
  render(sidebar("/remix/indexation"));
  expect(labels()).toContain("Indexation");
  expect(window.localStorage.getItem("remix.mode")).toBeNull();
});

it("distingue les pages du mode simple de celles réservées à l’expert", () => {
  expect(isSimpleRoute("langues", null)).toBe(true);
  expect(isSimpleRoute("lecture", "qualite")).toBe(true);
  expect(isSimpleRoute("lecture", "priorites")).toBe(false);
  expect(isSimpleRoute("contenus", null)).toBe(true);
  expect(isSimpleRoute("diagnostic", null)).toBe(true);
  expect(isSimpleRoute("indexation", null)).toBe(false);
  expect(isSimpleRoute("apercu", null)).toBe(false);
});

it("place le groupe Remix au-dessus de Bibliothèque, avec des libellés traduits", () => {
  const groups = buildNavigation([
    ...remixDescriptors,
    {
      kind: "navItem",
      id: "libraries",
      owner: "builtin",
      label: { source: "Libraries" },
      group: "library",
      meta: { route: "/libraries" },
    },
  ]);
  expect(groups.map((g) => g.label)).toEqual(["Remix", "Library"]);
  expect(groups[0]!.items).toHaveLength(REMIX_NAV.length);
  expect(pseNavLabel("pse-langues")).toBe("Langues et qualité");
  expect(groups[0]!.items.map((i) => i.href)).toContain(remixHref("diagnostic"));
});

it("l’adresse /remix/langues ouvre la page Qualité de Lecture sans barre de vues", async () => {
  render(page("/remix/langues"));
  expect(await screen.findByRole("heading", { name: "Langues et qualité", level: 1 })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Priorités" })).toBeNull();
});

it("le diagnostic signale la source manquante, l’indexation coupée et l’absence de titre", () => {
  const rows = buildDiagnostic(dashboard(false), [], {
    indexing: false,
    state: null,
    playable: null,
  });
  expect(rows.find((r) => r.id === "source")?.level).toBe("error");
  expect(rows.find((r) => r.id === "indexing")?.level).toBe("warn");
  const live = buildDiagnostic(dashboard(true), [], {
    indexing: true,
    state: "waiting",
    playable: 0,
  });
  expect(live.find((r) => r.id === "source")?.level).toBe("ok");
  expect(live.find((r) => r.id === "playable")?.level).toBe("warn");
  const ready = buildDiagnostic(dashboard(true), [], {
    indexing: true,
    state: "waiting",
    playable: 12,
  });
  expect(ready.find((r) => r.id === "playable")?.level).toBe("ok");
  expect(ready.find((r) => r.id === "apps")?.level).toBe("unknown");
});

it("la page Diagnostic mène à l’action de chaque problème", async () => {
  app.dashboard = dashboard(true);
  apiFetch.mockImplementation(async (path: string) =>
    path === "/pse/" ? { features: [{ fields: [{ key: "PSE_INDEXER", value: "off" }] }] } : {},
  );
  render(page("/remix/diagnostic"));
  expect(await screen.findByText("La préparation automatique est désactivée")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Connecter" }));
  expect((await screen.findByTestId("where")).textContent).toBe("/apps");
});

it("la page Sources du mode simple masque les catégories réservées aux experts", async () => {
  render(page("/remix/sources"));
  await screen.findByRole("button", { name: "Ajouter une source" });
  const nav = screen.getByRole("tablist", { name: "Catégories de sources" });
  expect(
    within(nav)
      .getAllByRole("tab")
      .map((t) => t.textContent),
  ).toEqual([
    "Stremio",
    "Debrid",
    "Fournisseurs Usenet",
    "Streaming musical",
    "Fichiers locaux",
    "IPTV",
  ]);
  expect(screen.queryByText("Contenus concernés :")).toBeNull();
  expect(screen.queryByText("Seedstrem")).toBeNull();
  expect(screen.queryByRole("tab", { name: "XDCC" })).toBeNull();
});

it("la page Sources du mode expert garde toutes les catégories", async () => {
  window.localStorage.setItem("remix.mode", "expert");
  render(page("/remix/sources"));
  await screen.findByRole("button", { name: "Ajouter une source" });
  expect(screen.getByRole("tab", { name: "XDCC" })).toBeTruthy();
  expect(screen.getByRole("tab", { name: "FTP / SFTP" })).toBeTruthy();
  expect(screen.getByText("Contenus concernés :")).toBeTruthy();
});

it("place chaque fonction Remix dans un seul onglet, sans orpheline", async () => {
  const { readFileSync, readdirSync } = await import("node:fs");
  const { resolve } = await import("node:path");
  const dir = resolve(process.cwd(), "../internal/pse/console");
  const declared = new Set<string>(["torrent2", "torrent3"]);
  for (const file of readdirSync(dir).filter(
    (f) => /^fields.*\.go$/.test(f) && !f.endsWith("_test.go"),
  ))
    for (const m of readFileSync(resolve(dir, file), "utf8").matchAll(/\bID:\s*"([a-z0-9_]+)"/g))
      declared.add(m[1]!);
  const { REMIX_TABS } = await import("./navigation");
  const placed: string[] = REMIX_TABS.flatMap((t) => [...t.features]);
  expect(placed.filter((id, i) => placed.indexOf(id) !== i)).toEqual([]);
  expect([...declared].filter((id) => !placed.includes(id))).toEqual([]);
  expect(placed.filter((id) => !declared.has(id))).toEqual([]);
});

it("range la section Pré dans Indexation", async () => {
  const { REMIX_TABS } = await import("./navigation");
  const where = REMIX_TABS.filter((t) => (t.features as readonly string[]).includes("predb"));
  expect(where.map((t) => t.key)).toEqual(["indexation"]);
});

it("range SubMaker, la recherche SeerrCatalog et le tri chacun à un seul endroit", async () => {
  const { REMIX_TABS } = await import("./navigation");
  const where = (id: string) =>
    REMIX_TABS.filter((t) => (t.features as readonly string[]).includes(id)).map((t) => t.key);
  expect(where("submaker")).toEqual(["lecture"]);
  expect(where("seerrcatalog_search")).toEqual(["sources"]);
  expect(where("librarysort")).toEqual(["contenus"]);
});
