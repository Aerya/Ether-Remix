import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import { JellyfinLatestFilters } from "./JellyfinLatestFilters";

const apiFetch = vi.fn();
vi.mock("../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => apiFetch(...args),
}));

const genre = (v: string[]) => ({ field: "genre", op: "in", value: v });
const defaults = {
  movie: { exclude: [genre(["Animation"])] },
  show: { exclude: [genre(["News"])] },
};
const puts: unknown[] = [];

beforeEach(() => {
  puts.length = 0;
  apiFetch.mockReset();
  apiFetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if (path.startsWith("/catalog/facets")) return { values: [] };
    if (init?.method === "PUT") {
      const body = JSON.parse(String(init.body));
      puts.push(body);
      return { ...body, defaults };
    }
    return { movie: {}, show: { exclude: [genre(["Reality"])] }, defaults };
  });
});

it("shows the three starter rules when empty and saves only completed rules", async () => {
  render(
    <MemoryRouter>
      <JellyfinLatestFilters />
    </MemoryRouter>,
  );
  await screen.findByRole("button", { name: /Enregistrer|Save/ });
  // Films : 3 lignes de départ ; Séries : la règle enregistrée.
  expect(screen.getAllByRole("group", { name: /Exclusion/ })).toHaveLength(4);
  fireEvent.click(screen.getByRole("button", { name: /Enregistrer|Save/ }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]).toEqual({
    movie: { exclude: [] },
    show: { exclude: [genre(["Reality"])] },
  });
});

it("replaces a block with the default template after confirmation", async () => {
  vi.spyOn(window, "confirm").mockReturnValue(true);
  render(
    <MemoryRouter>
      <JellyfinLatestFilters />
    </MemoryRouter>,
  );
  await screen.findByRole("button", { name: /Enregistrer|Save/ });
  fireEvent.click(
    screen.getAllByRole("button", { name: /modèle par défaut|default template/ })[0]!,
  );
  fireEvent.click(screen.getByRole("button", { name: /Enregistrer|Save/ }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect((puts[0] as { movie: unknown }).movie).toEqual({ exclude: [genre(["Animation"])] });
});
