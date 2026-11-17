import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import { XperienceImport } from "./XperienceImport";
const fetch = vi.fn();
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => fetch(...args),
}));
const manifest = "https://xperience-app.com/manifest/test/private-fixture/manifest.json";
const catalogs = [
  {
    id: "animation",
    type: "movie",
    name: "Animated",
    extras: [{ name: "genre", isRequired: true, options: ["None", "Animation"] }],
    defaults: { genre: "None" },
  },
  {
    id: "animation",
    type: "series",
    name: "Animated",
    extras: [{ name: "genre", isRequired: true, options: ["None", "Animation"] }],
    defaults: { genre: "None" },
  },
  {
    id: "custom",
    type: "movie",
    name: "Custom",
    extras: [{ name: "query", isRequired: true }],
    defaults: {},
  },
];
beforeEach(() => {
  fetch.mockReset();
  fetch.mockImplementation(async (path: string) =>
    path.endsWith("preview") ? { catalogs } : { collections: ["1", "2"], libraries: [3, 4] },
  );
});
async function open(onCreated?: () => void) {
  render(
    <MemoryRouter>
      <XperienceImport onCreated={onCreated} />
    </MemoryRouter>,
  );
  expect(screen.getByRole("link", { name: "xperience-app.com" })).toHaveAttribute(
    "href",
    "https://xperience-app.com/",
  );
  fireEvent.click(screen.getByRole("button", { name: /Importer un manifeste|Import a manifest/ }));
  const field = screen.getByLabelText(/Manifeste Xperience|Xperience manifest/);
  expect(field).toHaveAttribute("type", "password");
  fireEvent.change(field, { target: { value: manifest } });
  fireEvent.click(screen.getByRole("button", { name: /Afficher les catalogues|Show catalogues/ }));
  await screen.findByLabelText(/Animated · (Films|Movies)/);
}
it("previews without installation, then imports distinct film and series selections with required parameters", async () => {
  const done = vi.fn();
  await open(done);
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(
    screen.getByRole("button", { name: /Créer la sélection|Create selection/ }),
  ).toBeDisabled();
  fireEvent.click(screen.getByLabelText(/Animated · (Films|Movies)/));
  fireEvent.click(screen.getByLabelText(/Animated · (Séries|Shows)/));
  fireEvent.change(
    screen.getByLabelText(/Nom de Animated · (Films|Movies)|Name for Animated · (Films|Movies)/),
    { target: { value: "Dessins animés" } },
  );
  fireEvent.click(screen.getByRole("button", { name: /Créer la sélection|Create selection/ }));
  await screen.findByRole("status");
  const req = JSON.parse(fetch.mock.calls.at(-1)![1].body);
  expect(req).toMatchObject({
    manifest,
    mode: "both",
    maxItems: 500,
    manifestRefreshHours: 1,
    collectionRefreshHours: 24,
  });
  expect(req.catalogs).toEqual([
    { id: "animation", type: "movie", name: "Dessins animés", params: { genre: "None" } },
    { id: "animation", type: "series", name: expect.any(String), params: { genre: "None" } },
  ]);
  expect(done).toHaveBeenCalledOnce();
});
it("requires free-text parameters and invalidates old preview when the manifest changes", async () => {
  await open();
  fireEvent.click(screen.getByLabelText(/Custom · (Films|Movies)/));
  expect(
    screen.getByRole("button", { name: /Créer la sélection|Create selection/ }),
  ).toBeDisabled();
  fireEvent.change(screen.getByLabelText(/query · Custom/), { target: { value: "family" } });
  expect(screen.getByRole("button", { name: /Créer la sélection|Create selection/ })).toBeEnabled();
  fireEvent.change(screen.getByLabelText(/Manifeste Xperience|Xperience manifest/), {
    target: { value: manifest.replace("test", "other") },
  });
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: /Créer la sélection|Create selection/ }),
  ).not.toBeInTheDocument();
});
it("supports collections-only and retries a failed import without losing choices", async () => {
  await open();
  fireEvent.click(screen.getByLabelText(/Animated · (Films|Movies)/));
  fireEvent.click(screen.getByRole("button", { name: /^(Créer|Create)$/ }));
  fireEvent.click(
    await screen.findByRole("option", { name: /Collections seulement|Collections only/ }),
  );
  fetch.mockRejectedValueOnce(new Error("Réessayez"));
  fireEvent.click(screen.getByRole("button", { name: /Créer la sélection|Create selection/ }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Réessayez");
  expect(screen.getByRole("checkbox", { name: /Animated · (Films|Movies)/ })).toBeChecked();
  fireEvent.click(screen.getByRole("button", { name: /Créer la sélection|Create selection/ }));
  await screen.findByRole("status");
  expect(JSON.parse(fetch.mock.calls.at(-1)![1].body).mode).toBe("collections");
});
it("validates item limits and sends the selected genre", async () => {
  await open();
  fireEvent.click(screen.getByLabelText(/Animated · (Films|Movies)/));
  fireEvent.click(screen.getByRole("button", { name: /genre · Animated/ }));
  fireEvent.click(await screen.findByRole("option", { name: "Animation" }));
  fireEvent.change(screen.getByRole("spinbutton", { name: /Titres maximum|Maximum titles/ }), {
    target: { value: "2001" },
  });
  expect(
    screen.getByRole("button", { name: /Créer la sélection|Create selection/ }),
  ).toBeDisabled();
  fireEvent.change(screen.getByRole("spinbutton", { name: /Titres maximum|Maximum titles/ }), {
    target: { value: "1000" },
  });
  fireEvent.click(screen.getByRole("button", { name: /Créer la sélection|Create selection/ }));
  await waitFor(() =>
    expect(JSON.parse(fetch.mock.calls.at(-1)![1].body)).toMatchObject({
      maxItems: 1000,
      catalogs: [{ params: { genre: "Animation" } }],
    }),
  );
});

it("validates and sends independent manifest and collection intervals", async () => {
  await open();
  fireEvent.click(screen.getByLabelText(/Animated · (Films|Movies)/));
  const manifestInterval = screen.getByLabelText(/Actualiser le manifeste|Refresh manifest/);
  const collectionInterval = screen.getByLabelText(
    /Actualiser les nouvelles collections|Refresh new collections/,
  );
  fireEvent.change(manifestInterval, { target: { value: "0" } });
  expect(
    screen.getByRole("button", { name: /Créer la sélection|Create selection/ }),
  ).toBeDisabled();
  fireEvent.change(manifestInterval, { target: { value: "6" } });
  fireEvent.change(collectionInterval, { target: { value: "12" } });
  fireEvent.click(screen.getByRole("button", { name: /Créer la sélection|Create selection/ }));
  await screen.findByRole("status");
  expect(JSON.parse(fetch.mock.calls.at(-1)![1].body)).toMatchObject({
    manifestRefreshHours: 6,
    collectionRefreshHours: 12,
  });
});
