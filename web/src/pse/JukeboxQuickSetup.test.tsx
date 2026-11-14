import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import { JukeboxQuickSetup } from "./JukeboxQuickSetup";
const fetch = vi.fn();
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => fetch(...args),
}));
const lists = [
  { slug: "les-plus-pirates-films", name: "Films · JukeBox", type: "movie" },
  { slug: "les-plus-populaires-de-la-semaine-series-tv", name: "Séries · JukeBox", type: "show" },
  { slug: "les-plus-populaires-de-la-semaine-anime", name: "Animes · JukeBox", type: "mixed" },
];
beforeEach(() => {
  fetch.mockReset();
  fetch.mockImplementation(async (_path: string, init?: RequestInit) =>
    init?.method === "POST"
      ? { collections: ["1", "2", "3", "4"], libraries: [3, 4, 5, 6] }
      : { lists },
  );
});
async function open(onCreated?: () => void) {
  render(
    <MemoryRouter>
      <JukeboxQuickSetup onCreated={onCreated} />
    </MemoryRouter>,
  );
  fireEvent.click(screen.getByRole("button", { name: /Configurer avec|Set up with/ }));
  await screen.findByLabelText("Films · JukeBox");
}
it("preselects the three lists and publishes libraries in one action", async () => {
  const done = vi.fn();
  await open(done);
  expect(screen.getAllByRole("checkbox").every((e) => (e as HTMLInputElement).checked)).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: /Créer ma sélection|Create my selection/ }));
  await screen.findByRole("status");
  const post = fetch.mock.calls.find((c) => c[1]?.method === "POST");
  expect(JSON.parse(post![1].body)).toEqual({ lists, libraries: true });
  expect(done).toHaveBeenCalledOnce();
});
it("adds a searched documentary and imports collections only", async () => {
  fetch.mockImplementation(async (path: string, init?: RequestInit) =>
    init?.method === "POST"
      ? { collections: ["1"], libraries: [] }
      : {
          lists: path.endsWith("catalog")
            ? [{ slug: "documentaire-films", name: "Documentaire (Films)", type: "movie" }]
            : lists,
        },
  );
  await open();
  fireEvent.click(screen.getByRole("button", { name: /Choisir parmi|Browse all/ }));
  await screen.findByLabelText(/Rechercher une liste|Search lists/);
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "Documentaire" } });
  fireEvent.click(screen.getByLabelText("Documentaire (Films)"));
  fireEvent.click(screen.getByRole("button", { name: /^(Créer|Create)$/ }));
  fireEvent.click(await screen.findByRole("option", { name: /seulement|only/ }));
  fireEvent.click(screen.getByRole("button", { name: /Créer ma sélection|Create my selection/ }));
  await screen.findByRole("status");
  const post = fetch.mock.calls.find((c) => c[1]?.method === "POST");
  const body = JSON.parse(post![1].body);
  expect(body.libraries).toBe(false);
  expect(body.lists).toHaveLength(4);
  expect(body.lists[3].slug).toBe("documentaire-films");
});
it("keeps starters usable after a catalogue error and supports retry", async () => {
  let failed = false;
  fetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if (path.endsWith("catalog")) throw new Error("MDBList indisponible");
    if (init?.method === "POST") {
      if (!failed) {
        failed = true;
        throw new Error("Réessayez");
      }
      return { collections: ["1"], libraries: [3] };
    }
    return { lists };
  });
  await open();
  fireEvent.click(screen.getByRole("button", { name: /Choisir parmi|Browse all/ }));
  expect(await screen.findByRole("alert")).toHaveTextContent("MDBList indisponible");
  fireEvent.click(screen.getByRole("button", { name: /Créer ma sélection|Create my selection/ }));
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Réessayez"));
  fireEvent.click(screen.getByRole("button", { name: /Créer ma sélection|Create my selection/ }));
  await screen.findByRole("status");
});
