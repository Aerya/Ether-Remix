import { render, screen } from "@testing-library/react";
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
vi.mock("./ThemeLibraries", () => ({ ThemeLibraries: () => null }));

const num = (key: string, fr: string, en: string, value: string, min: number, max: number) => ({
  key,
  kind: "number",
  label: { fr, en },
  help: { fr: `Aide ${fr}`, en: `Help ${en}` },
  default: value,
  min,
  max,
  value,
  source: "",
});

const data = {
  features: [
    {
      id: "continuewatch",
      title: { fr: "Continuer à regarder", en: "Continue watching" },
      description: { fr: "Rangée Continuer à regarder", en: "Continue Watching row" },
      fields: [
        num("PSE_CONTINUE_SCAN_LIMIT", "Séries examinées", "Shows examined", "200", 20, 1000),
        num(
          "PSE_CONTINUE_WATCH_DAYS",
          "Période du dernier visionnage (jours)",
          "Last-watched period (days)",
          "0",
          0,
          3650,
        ),
        num(
          "PSE_CONTINUE_NEW_DAYS",
          "Fenêtre des nouveaux épisodes (jours)",
          "New episodes window (days)",
          "60",
          0,
          365,
        ),
        num("PSE_CONTINUE_MAX_MOVIES", "Films au maximum", "Maximum movies", "0", 0, 200),
        num("PSE_CONTINUE_MAX_SHOWS", "Séries au maximum", "Maximum shows", "0", 0, 200),
      ],
    },
  ],
};

it("affiche la carte Continuer à regarder dans la vue Continuer à regarder de l’onglet Lecture, avec ses valeurs par défaut", async () => {
  apiFetch.mockResolvedValue(data);
  render(
    <MemoryRouter initialEntries={["/remix/lecture?view=continuer"]}>
      <Routes>
        <Route path="/remix/:tab" element={<PsePage />} />
      </Routes>
    </MemoryRouter>,
  );
  expect((await screen.findAllByText("Continuer à regarder")).length).toBe(2);
  const input = (label: string) => screen.getByLabelText(label) as HTMLInputElement;
  expect(input("Séries examinées").value).toBe("200");
  expect(input("Séries examinées").min).toBe("20");
  expect(input("Séries examinées").max).toBe("1000");
  expect(input("Période du dernier visionnage (jours)").value).toBe("0");
  expect(input("Fenêtre des nouveaux épisodes (jours)").value).toBe("60");
  expect(input("Films au maximum").value).toBe("0");
  expect(input("Séries au maximum").value).toBe("0");
});
