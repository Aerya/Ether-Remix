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
vi.mock("./VostfrTitles", () => ({ VostfrTitles: () => null }));
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
    <MemoryRouter initialEntries={["/accent/films"]}>
      <Routes>
        <Route path="/accent/:tab" element={<PsePage />} />
      </Routes>
    </MemoryRouter>,
  );
  await screen.findByLabelText("Prowlarr");
  const tabs = screen.getAllByRole("tab").map((tab) => tab.textContent);
  expect(tabs).toEqual(["Films & Séries", "Musique", "Bibliothèques", "Stats", "Notif & suivi"]);
  const label = screen.getByLabelText("Prowlarr").closest("label")!;
  expect(label.parentElement!.className).toContain("flex-col");
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
