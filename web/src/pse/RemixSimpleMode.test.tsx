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
  expect(screen.getByRole("heading", { name: "Accueil", level: 1 })).toBeTruthy();
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
    "Accueil",
    "Sources",
    "Langues et qualité",
    "Contenus",
    "Statistiques",
    "Diagnostic",
    "Expert",
  ]);
});

it("la barre latérale affiche les entrées Expert en mode Expert, sans Langues et qualité", () => {
  window.localStorage.setItem("remix.mode", "expert");
  render(sidebar("/remix/accueil"));
  expect(labels()).toEqual([
    "Accueil",
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
  expect(isSimpleRoute("apercu", null)).toBe(true);
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

it("range la section Pre dans Indexation", async () => {
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

it("donne à la section Pre (bêta) sa propre vue dans Indexation", async () => {
  window.localStorage.setItem("remix.mode", "expert");
  apiFetch.mockResolvedValue({
    features: [
      {
        id: "predb",
        title: { fr: "Section Pre", en: "Pre section" },
        description: { fr: "", en: "" },
        fields: [],
      },
      {
        id: "indexer",
        title: { fr: "Indexation des releases", en: "Release indexing" },
        description: { fr: "", en: "" },
        fields: [],
      },
    ],
  });
  render(page("/remix/indexation?view=pre"));
  expect(await screen.findByText("Section Pre")).toBeTruthy();
  expect(screen.queryByText("Indexation des releases")).toBeNull();
  expect(screen.getByRole("button", { name: "Pre (bêta)" })).toBeTruthy();
});

it("Statistiques propose Résumé, Catalogue, Recherches, Sources et Utilisation", async () => {
  window.localStorage.setItem("remix.mode", "expert");
  render(page("/remix/apercu"));
  const nav = await screen.findByRole("navigation", { name: "Vues de l’onglet" });
  expect(
    within(nav)
      .getAllByRole("button")
      .map((b) => b.textContent),
  ).toEqual(["Résumé", "Catalogue", "Recherches", "Sources", "Utilisation"]);
});

it("la vue Pre cite les flux publics lus par défaut, avec leurs liens", async () => {
  window.localStorage.setItem("remix.mode", "expert");
  render(page("/remix/indexation?view=pre"));
  expect(await screen.findByText("Flux publics lus par défaut")).toBeTruthy();
  const hrefs = screen.getAllByRole("link").map((a) => a.getAttribute("href"));
  expect(hrefs).toContain("https://predb.net/");
  expect(hrefs).toContain("https://api.predb.net/feed/");
  expect(hrefs).toContain("https://predb.me/?cats=movies&rss=1");
  expect(hrefs.some((h) => h?.endsWith("/docs/pse/PRE.fr.md"))).toBe(true);
});

it("la liste des flux publics affichée est celle du serveur", async () => {
  const { readFileSync } = await import("node:fs");
  const { resolve } = await import("node:path");
  const { PRE_PUBLIC_FEEDS } = await import("./PreFeeds");
  const go = readFileSync(resolve(process.cwd(), "../internal/pse/releasedb/predb.go"), "utf8");
  const block = go.slice(
    go.indexOf("var preDBPublicFeeds"),
    go.indexOf("}", go.indexOf("var preDBPublicFeeds")),
  );
  const urls = [...block.matchAll(/"(https:\/\/[^"]+)"/g)].map((m) => m[1]);
  expect(PRE_PUBLIC_FEEDS.map((f) => f.url)).toEqual(urls);
});

it("Statistiques affiche les releases indexées au total et celles issues des pre quand l’option est activée", async () => {
  const { PreStats } = await import("./PreStats");
  apiFetch.mockImplementation(async (path: string) =>
    path.startsWith("/pse/release-library") ? { references: 1000 } : {},
  );
  const { container, rerender } = render(
    <MemoryRouter>
      <PreStats status={{ active: false }} />
    </MemoryRouter>,
  );
  expect(container.textContent).toContain("Désactivée");
  expect(container.textContent).not.toContain("Releases issues des pre");
  rerender(
    <MemoryRouter>
      <PreStats status={{ active: true, import: { added: 250, pending: 3, last: 1700000000 } }} />
    </MemoryRouter>,
  );
  expect(await screen.findByText("Releases indexées (global)")).toBeTruthy();
  expect(await screen.findByText(/^1\s?000$/)).toBeTruthy();
  expect(screen.getByText("Releases issues des pre")).toBeTruthy();
  expect(screen.getByText("250")).toBeTruthy();
  expect(screen.getByText("25 % du total")).toBeTruthy();
  expect(screen.getByText("Pre en attente")).toBeTruthy();
});

it("la vue Résumé de Statistiques montre le bloc Pre seulement si l’option est activée", async () => {
  window.localStorage.setItem("remix.mode", "expert");
  const features = (active: boolean) => ({
    features: [
      {
        id: "predb",
        title: { fr: "Section Pre", en: "Pre section" },
        description: { fr: "", en: "" },
        fields: [],
        status: { active, import: { added: 5, pending: 0 } },
      },
    ],
  });
  apiFetch.mockImplementation(async (path: string) =>
    path === "/pse/"
      ? features(true)
      : path.startsWith("/pse/release-library")
        ? { references: 50 }
        : {},
  );
  const on = render(page("/remix/apercu?view=resume"));
  expect(await screen.findByText("Releases issues des pre")).toBeTruthy();
  on.unmount();
  apiFetch.mockImplementation(async (path: string) => (path === "/pse/" ? features(false) : {}));
  render(page("/remix/apercu?view=resume"));
  await screen.findByRole("heading", { name: "Statistiques", level: 1 });
  expect(screen.queryByText("Releases issues des pre")).toBeNull();
});

it("Statistiques détaille les releases indexées pour chaque pre, sans exposer les flux personnels", async () => {
  const { PreStats } = await import("./PreStats");
  apiFetch.mockResolvedValue({ references: 1000 });
  render(
    <MemoryRouter>
      <PreStats
        status={{
          active: true,
          import: {
            added: 40,
            pending: 2,
            feeds: [
              {
                feed: "https://predb.me/?cats=movies&rss=1",
                seen: 30,
                indexed: 25,
                pending: 2,
                unmatched: 3,
                state: "ok",
                fetched: 100,
                kept: 12,
              },
              {
                feed: "perso:1",
                seen: 10,
                indexed: 10,
                pending: 0,
                unmatched: 0,
                state: "error",
                error: "flux refusé (403)",
              },
              { feed: "", seen: 0, indexed: 5, pending: 0, unmatched: 0 },
            ],
          },
        }}
      />
    </MemoryRouter>,
  );
  const table = await screen.findByRole("table");
  const rows = within(table)
    .getAllByRole("row")
    .map((r) => [...r.querySelectorAll("th,td")].map((c) => c.textContent));
  expect(rows[1]).toEqual([
    "predb.me · films",
    "30",
    "25",
    "2",
    "3",
    "OK · 100 annonces · 12 retenues",
  ]);
  expect(rows[2]).toEqual(["Site 1", "10", "10", "0", "0", "Erreur · flux refusé (403)"]);
  expect(rows[3]![0]).toBe("Plus anciennes ou flux inconnu");
  expect(rows[4]).toEqual(["Total", "40", "40", "2", "3", ""]);
});

it("Statistiques est visible dès le mode Simple et montre les releases indexées par source", async () => {
  const { IndexedReleases } = await import("./IndexedReleases");
  apiFetch.mockResolvedValue({
    sources: [
      { id: "a", name: "AIOStreams", enabled: true, titles: 10, releases: 300 },
      {
        id: "remix-pre",
        name: "Pre (annonces de releases)",
        enabled: true,
        titles: 4,
        releases: 40,
      },
      { id: "z", name: "Source vide", enabled: false, titles: 0, releases: 0 },
    ],
  });
  render(
    <MemoryRouter>
      <IndexedReleases pre={{ active: false }} />
    </MemoryRouter>,
  );
  expect(await screen.findByText("Releases indexées (global)")).toBeTruthy();
  expect(screen.getByText("340")).toBeTruthy();
  const rows = within(screen.getByRole("table"))
    .getAllByRole("row")
    .map((r) => [...r.querySelectorAll("th,td")].map((c) => c.textContent));
  expect(rows[1]).toEqual(["AIOStreams", "10", "300"]);
  expect(rows[2]).toEqual(["Pre (annonces de releases)", "4", "40"]);
  expect(rows).toHaveLength(3);
  expect(screen.getByRole("link", { name: /Activer dans Indexation/ }).getAttribute("href")).toBe(
    "/remix/indexation?view=pre",
  );
});
