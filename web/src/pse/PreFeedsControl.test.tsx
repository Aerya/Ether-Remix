import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { PreFeedsControl } from "./PreFeedsControl";
import { PreTest } from "./PreTest";

const apiFetch = vi.fn();
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => apiFetch(...args),
}));

vi.mock("../i18n", async (original) => ({
  ...(await original<typeof import("../i18n")>()),
  getLocale: () => "fr",
}));

beforeEach(() => apiFetch.mockReset());

it("permet d'ajouter plusieurs sites puis d'en retirer un", () => {
  const onChange = vi.fn();
  render(<PreFeedsControl value="" onChange={onChange} label="Autres sites" />);
  const add = screen.getByRole("button", { name: /Ajouter un site/i });
  fireEvent.click(add);
  fireEvent.click(add);
  fireEvent.change(screen.getByLabelText("Adresse du site 1"), {
    target: { value: "https://a.example/rss" },
  });
  fireEvent.change(screen.getByLabelText("Adresse du site 2"), {
    target: { value: "https://b.example/api" },
  });
  fireEvent.change(screen.getByLabelText("Clé d’API du site 2"), { target: { value: "k2" } });
  expect(JSON.parse(onChange.mock.lastCall![0])).toEqual([
    { url: "https://a.example/rss" },
    { url: "https://b.example/api", key: "k2" },
  ]);
  fireEvent.click(screen.getByRole("button", { name: "Retirer le site 1" }));
  expect(JSON.parse(onChange.mock.lastCall![0])).toEqual([
    { url: "https://b.example/api", key: "k2" },
  ]);
});

it("n'enregistre pas les lignes vides et garde une clé masquée telle quelle", () => {
  const onChange = vi.fn();
  render(
    <PreFeedsControl
      value={JSON.stringify([{ name: "T", url: "https://t.example/rss", key: "••••••" }])}
      onChange={onChange}
      label="Autres sites"
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: /Ajouter un site/i }));
  fireEvent.change(screen.getByLabelText("Nom du site 1"), { target: { value: "T2" } });
  expect(JSON.parse(onChange.mock.lastCall![0])).toEqual([
    { name: "T2", url: "https://t.example/rss", key: "••••••" },
  ]);
  fireEvent.change(screen.getByLabelText("Nom du site 1"), { target: { value: "T" } });
  fireEvent.click(screen.getByRole("button", { name: /Ajouter un site/i }));
  expect(JSON.parse(onChange.mock.lastCall![0])).toHaveLength(1);
});

it("le bouton Tester envoie les réglages saisis et affiche le résultat par flux", async () => {
  apiFetch.mockResolvedValue({
    ok: true,
    result: {
      proxy: "proxy inaccessible",
      runs: [
        { feed: "perso:1", label: "Mon site", state: "ok", fetched: 20, kept: 3, sample: ["Film.2026.MULTi.1080p-GRP"] },
        { feed: "perso:2", state: "error", error: "flux refusé (403)" },
      ],
    },
  });
  render(<PreTest values={{ PSE_PREDB_PROXY_URL: "https://m.example", AUTRE: "x" }} />);
  fireEvent.click(screen.getByRole("button", { name: /Tester les flux/i }));
  await waitFor(() => expect(screen.getByText("Mon site")).toBeTruthy());
  const [path, init] = apiFetch.mock.calls[0]!;
  expect(path).toBe("/pse/predb/test");
  expect(JSON.parse((init as { body: string }).body).values).toEqual({
    PSE_PREDB_PROXY_URL: "https://m.example",
  });
  expect(screen.getByText("1 flux sur 2 lisible(s).")).toBeTruthy();
  expect(screen.getByText(/flux refusé \(403\)/)).toBeTruthy();
  expect(screen.getByText(/Film\.2026\.MULTi/)).toBeTruthy();
  expect(screen.getByText(/proxy inaccessible/)).toBeTruthy();
});

it("affiche l'erreur quand le test échoue", async () => {
  apiFetch.mockResolvedValue({ ok: false, message: "réseau coupé" });
  render(<PreTest values={{}} />);
  fireEvent.click(screen.getByRole("button", { name: /Tester les flux/i }));
  await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("réseau coupé"));
});
