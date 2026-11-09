import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { collectionsFor, ThemeLibraries, type ThemeCollection } from "./ThemeLibraries";

const apiFetch = vi.fn();
vi.mock("../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => apiFetch(...args),
}));

const cols: ThemeCollection[] = [
  { id: "1", name: "Netflix", mediaType: "movie", items: 120, builtIn: false },
  { id: "2", name: "HBO", mediaType: "show", items: 40, builtIn: false },
  { id: "3", name: "Concerts", mediaType: "mixed", items: 12, builtIn: false },
];

it("offers collections of the library's type", () => {
  expect(collectionsFor(cols, "movie", ["1"]).map((c) => c.id)).toEqual(["3"]);
  expect(collectionsFor(cols, "show", []).map((c) => c.id)).toEqual(["2", "3"]);
  expect(collectionsFor(cols, "mixed", ["3"]).map((c) => c.id)).toEqual(["1", "2"]);
});

it("lists libraries, applies a preset and creates a library", async () => {
  const calls: { path: string; init?: RequestInit }[] = [];
  apiFetch.mockImplementation(async (path: string, init?: RequestInit) => {
    calls.push({ path, init });
    return {
      libraries: [
        { id: 1, name: "Services", type: "movie", collections: ["1"], position: 0, section: 5101 },
      ],
      collections: cols,
      presets: [
        {
          id: "studios-movie",
          name: "Studios",
          type: "movie",
          summary: "Une rangée par studio",
          rows: ["Pixar"],
        },
      ],
      max: 99,
    };
  });
  render(<ThemeLibraries />);
  expect(await screen.findByText(/1\. Netflix/)).toBeTruthy();

  fireEvent.click(screen.getByRole("button", { name: /^(Create|Créer)$/ }));
  await waitFor(() => expect(calls.some((c) => c.path === "/pse/themes/preset")).toBe(true));
  const preset = calls.find((c) => c.path === "/pse/themes/preset")!;
  expect(JSON.parse(String(preset.init!.body))).toEqual({ preset: "studios-movie", name: "" });

  const names = screen.getAllByLabelText(/Library name|Nom de la bibliothèque/);
  fireEvent.change(names[names.length - 1]!, { target: { value: "Concerts" } });
  const adds = screen.getAllByLabelText(/^(Add a collection|Ajouter une collection)$/);
  fireEvent.click(adds[adds.length - 1]!);
  fireEvent.click(await screen.findByRole("option", { name: /Concerts/ }));
  fireEvent.click(screen.getByRole("button", { name: /Create library|Créer la bibliothèque/ }));
  await waitFor(() =>
    expect(calls.some((c) => c.path === "/pse/themes/" && c.init?.method === "POST")).toBe(true),
  );
  const created = calls.find((c) => c.path === "/pse/themes/" && c.init?.method === "POST")!;
  expect(JSON.parse(String(created.init!.body))).toEqual({
    name: "Concerts",
    type: "movie",
    collections: ["3"],
  });
});

it("renames a library and turns it into a Movies & shows library", async () => {
  const calls: { path: string; init?: RequestInit }[] = [];
  apiFetch.mockImplementation(async (path: string, init?: RequestInit) => {
    calls.push({ path, init });
    return {
      libraries: [
        { id: 1, name: "Services", type: "movie", collections: ["1"], position: 0, section: 5101 },
      ],
      collections: cols,
      presets: [],
      max: 99,
    };
  });
  render(<ThemeLibraries />);
  expect(await screen.findByText(/1\. Netflix/)).toBeTruthy();
  const names = screen.getAllByLabelText(/Library name|Nom de la bibliothèque/);
  fireEvent.change(names[0]!, { target: { value: "Plateformes" } });
  fireEvent.click(screen.getAllByLabelText(/^Type$/)[0]!);
  fireEvent.click(await screen.findByRole("option", { name: /Movies & shows|Films & séries/ }));
  const adds = screen.getAllByLabelText(/^(Add a collection|Ajouter une collection)$/);
  fireEvent.click(adds[0]!);
  fireEvent.click(await screen.findByRole("option", { name: /HBO/ }));
  fireEvent.click(screen.getAllByRole("button", { name: /^(Save|Enregistrer)$/ })[0]!);
  await waitFor(() => expect(calls.some((c) => c.init?.method === "PUT")).toBe(true));
  const put = calls.find((c) => c.init?.method === "PUT")!;
  expect(put.path).toBe("/pse/themes/1");
  expect(JSON.parse(String(put.init!.body))).toEqual({
    name: "Plateformes",
    type: "mixed",
    collections: ["1", "2"],
    allLanguages: false,
  });
});

it("saves language exceptions", async () => {
  const calls: { path: string; init?: RequestInit }[] = [];
  apiFetch.mockImplementation(async (path: string, init?: RequestInit) => {
    calls.push({ path, init });
    return { libraries: [], collections: cols, presets: [], max: 99, exceptions: ["2"] };
  });
  render(<ThemeLibraries exceptionsOnly />);
  expect(await screen.findByText("HBO")).toBeTruthy();
  fireEvent.click(screen.getByLabelText(/^(Add an exception|Ajouter une exception)$/));
  fireEvent.click(await screen.findByRole("option", { name: /Concerts/ }));
  const saves = screen.getAllByRole("button", { name: /^(Save|Enregistrer)$/ });
  fireEvent.click(saves[saves.length - 1]!);
  await waitFor(() => expect(calls.some((c) => c.path === "/pse/themes/exceptions")).toBe(true));
  const put = calls.find((c) => c.path === "/pse/themes/exceptions")!;
  expect(put.init?.method).toBe("PUT");
  expect(JSON.parse(String(put.init!.body))).toEqual({ collections: ["2", "3"] });
});

it("keeps renamed anime libraries in the anime tab and excludes other libraries", async () => {
  apiFetch.mockResolvedValue({
    libraries: [
      {
        id: 1,
        name: "Mes séries japonaises",
        preset: "anime",
        type: "mixed",
        collections: ["2"],
        position: 0,
      },
      {
        id: 2,
        name: "Services",
        preset: "services",
        type: "movie",
        collections: ["1"],
        position: 1,
      },
    ],
    collections: cols,
    presets: [],
    max: 99,
  });
  render(<ThemeLibraries preset="anime" />);
  expect(await screen.findByDisplayValue("Mes séries japonaises")).toBeTruthy();
  expect(screen.queryByDisplayValue("Services")).toBeNull();
  const adds = screen.getAllByLabelText(/^(Add a collection|Ajouter une collection)$/);
  fireEvent.click(adds[adds.length - 1]!);
  fireEvent.click(await screen.findByRole("option", { name: /Concerts/ }));
  fireEvent.click(screen.getByRole("button", { name: /Create library|Créer la bibliothèque/ }));
  await waitFor(() =>
    expect(
      apiFetch.mock.calls.some(
        ([path, init]) =>
          path === "/pse/themes/" &&
          init?.method === "POST" &&
          JSON.parse(init.body).preset === "anime",
      ),
    ).toBe(true),
  );
});
