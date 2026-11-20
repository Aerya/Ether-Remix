import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import type { DashboardData } from "../lib/types";
import PsePage from "./PsePage";
import { buildDiagnostic } from "./RemixDiagnostic";
import { simpleTabHref, simpleTabOf } from "./navigation";

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

it("ouvre l’accueil et affiche les quatre onglets du mode simple", async () => {
  window.localStorage.setItem("remix.setup.wizard", "1");
  render(page("/remix"));
  expect(await screen.findByText("3 étapes pour regarder un film")).toBeTruthy();
  expect(screen.getAllByRole("tab").map((t) => t.textContent)).toEqual([
    "Accueil",
    "Sources",
    "Langues et qualité",
    "Bibliothèque",
  ]);
  expect(screen.getByRole("button", { name: /Diagnostic/ })).toBeTruthy();
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

it("bascule en mode expert, mémorise le choix et revient au mode simple", async () => {
  window.localStorage.setItem("remix.setup.wizard", "1");
  apiFetch.mockResolvedValue({ features: [] });
  render(page("/remix/accueil"));
  fireEvent.click(await screen.findByLabelText("Mode expert"));
  expect(await screen.findByRole("tab", { name: "Vue d’ensemble" })).toBeTruthy();
  expect(window.localStorage.getItem("remix.mode")).toBe("expert");
  fireEvent.click(screen.getByLabelText("Mode expert"));
  expect(await screen.findByText("3 étapes pour regarder un film")).toBeTruthy();
  expect(window.localStorage.getItem("remix.mode")).toBe("simple");
});

it("affiche les onglets experts sur une adresse experte sans changer le mode mémorisé", async () => {
  render(page("/remix/indexation"));
  expect(await screen.findByRole("tab", { name: "Indexation" })).toBeTruthy();
  expect(window.localStorage.getItem("remix.mode")).toBeNull();
});

it("relie les onglets simples aux pages existantes", () => {
  expect(simpleTabHref("langues")).toBe("/remix/lecture?view=qualite");
  expect(simpleTabHref("bibliotheque")).toBe("/remix/contenus");
  expect(simpleTabOf("lecture", "qualite")).toBe("langues");
  expect(simpleTabOf("lecture", "priorites")).toBeNull();
  expect(simpleTabOf("contenus", null)).toBe("bibliotheque");
  expect(simpleTabOf("diagnostic", null)).toBe("");
  expect(simpleTabOf("indexation", null)).toBeNull();
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
