import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
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
  RemixSources: ({ indexing }: { indexing: React.ReactNode }) => indexing,
}));
vi.mock("./SearchPriority", () => ({ SearchPriority: () => null }));
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
    <MemoryRouter initialEntries={["/accent/sources?source=indexation"]}>
      <Routes>
        <Route path="/accent/:tab" element={<PsePage />} />
      </Routes>
    </MemoryRouter>,
  );
  await screen.findByLabelText("Prowlarr");
  const tabs = screen.getAllByRole("tab").map((tab) => tab.textContent);
  expect(tabs).toEqual([
    "Sources",
    "Langues et qualité",
    "VOSTFR à la demande",
    "Contenus et bibliothèques",
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
    <MemoryRouter initialEntries={["/accent/demande"]}>
      <Routes>
        <Route path="/accent/:tab" element={<PsePage />} />
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
    <MemoryRouter initialEntries={["/accent/films#pse-indexer"]}>
      <Routes>
        <Route path="/accent/:tab" element={<PsePage />} />
      </Routes>
    </MemoryRouter>,
  );
  expect(await screen.findByRole("heading", { name: "Indexation" })).toBeTruthy();
});
