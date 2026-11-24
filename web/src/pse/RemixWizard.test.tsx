import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import { RemixWizard } from "./RemixWizard";
import { initialChoices, restoreChoices, visibleSetupSources } from "./setupModel";
const apiFetch = vi.hoisted(() => vi.fn());
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch,
}));
vi.mock("../i18n", async (original) => ({
  ...(await original<typeof import("../i18n")>()),
  getLocale: () => "fr",
}));
vi.mock("./ThemeLibraries", () => ({
  ThemeLibraries: () => <p>Éditeur de bibliothèques existant</p>,
}));
vi.mock("./JukeboxQuickSetup", () => ({
  JukeboxQuickSetup: () => <p>Éditeur JukeBox existant</p>,
}));
const state = {
  revision: "r1",
  fields: [
    {
      key: "PSE_FRENCH",
      value: "off",
      label: { fr: "Catalogue français", en: "French catalogue" },
    },
    {
      key: "PSE_FRENCH_RELEASES",
      value: "french,multi",
      label: { fr: "Langues", en: "Languages" },
    },
  ],
  warnings: [],
};
const mount = (step = 1) =>
  render(
    <MemoryRouter initialEntries={[`/remix/assistant?step=${step}`]}>
      <RemixWizard />
    </MemoryRouter>,
  );
beforeEach(() => {
  localStorage.clear();
  apiFetch.mockReset();
  apiFetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if (path === "/pse/setup") return state;
    if (path === "/pse/setup/preview") {
      const body = JSON.parse(String(init?.body));
      return {
        ...body,
        plan: "reviewed",
        changes: [
          { key: "PSE_FRENCH", before: "off", after: "on", label: { fr: "Activer", en: "Enable" } },
        ],
        warnings: [],
      };
    }
    if (path === "/pse/setup/apply") return { revision: "r2", undo: { PSE_FRENCH: null } };
    return {};
  });
});
it("ne modifie rien en choisissant des usages et propose le client torrent seulement quand pertinent", async () => {
  mount(4);
  await screen.findByRole("heading", { name: "Comment souhaitez-vous lire et conserver ?" });
  fireEvent.click(screen.getByRole("checkbox", { name: /Télécharger aussi/ }));
  fireEvent.click(screen.getByRole("button", { name: "Suivant" }));
  expect(await screen.findByRole("checkbox", { name: /Client BitTorrent direct/ })).toBeTruthy();
  fireEvent.click(screen.getByRole("checkbox", { name: /Client BitTorrent direct/ }));
  const link = screen.getByRole("link", { name: /Configurer \/ tester/ });
  expect(link).toHaveAttribute("href", "/remix/sources?source=torrent");
  expect(link).toHaveAttribute("target", "_blank");
  expect(apiFetch.mock.calls.every(([, init]) => !init?.method)).toBe(true);
  expect(JSON.parse(localStorage.getItem("remix.setup.intent.v1")!).sources).toContain("torrent");
});
it("exige un aperçu et une confirmation avant application et propose le retour", async () => {
  mount(2);
  const checkbox = await screen.findByRole("checkbox", {
    name: /Privilégier le catalogue français/,
  });
  await waitFor(() => expect(checkbox).not.toBeDisabled());
  fireEvent.click(checkbox);
  fireEvent.click(screen.getByRole("button", { name: /8\. Examiner et appliquer/ }));
  fireEvent.click(screen.getByRole("button", { name: "Prévisualiser les changements" }));
  const apply = await screen.findByRole("button", { name: "Appliquer ces changements" });
  expect(apply).toBeDisabled();
  fireEvent.click(screen.getByRole("checkbox", { name: /J’ai examiné/ }));
  fireEvent.click(apply);
  await screen.findByRole("button", { name: "Prévisualiser le retour aux réglages précédents" });
  const call = apiFetch.mock.calls.find(([path]) => path === "/pse/setup/apply");
  expect(JSON.parse(call![1].body)).toEqual({
    revision: "r1",
    plan: "reviewed",
    values: { PSE_FRENCH: "on" },
  });
});
it("un retour d’onglet invalide l’aperçu sans perdre les choix", async () => {
  mount(8);
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "Prévisualiser les changements" }),
    ).not.toBeDisabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Prévisualiser les changements" }));
  await screen.findByRole("button", { name: "Appliquer ces changements" });
  fireEvent(window, new Event("focus"));
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Appliquer ces changements" })).toBeNull(),
  );
  expect(apiFetch.mock.calls.filter(([path]) => path === "/pse/setup")).toHaveLength(2);
});
it("une panne de lecture de configuration empêche tout enregistrement", async () => {
  apiFetch.mockRejectedValue(new Error("indisponible"));
  mount(8);
  expect(await screen.findByRole("alert")).toHaveTextContent("indisponible");
  expect(screen.getByRole("button", { name: "Prévisualiser les changements" })).toBeDisabled();
});
it("une intention mémorisée ne transporte ni URL arbitraire ni secret", () => {
  const restored = restoreChoices({
    contents: ["music", "hostile", "music"],
    usages: ["stream"],
    sources: ["https://hostile.invalid", "music"],
    clients: ["plex", "admin"],
    token: "secret",
  });
  expect(restored.contents).toEqual(["music"]);
  expect(restored.sources).toEqual(["music"]);
  expect(JSON.stringify(restored)).not.toContain("secret");
  expect(visibleSetupSources(initialChoices).some((s) => s.id === "music")).toBe(false);
  expect(visibleSetupSources(restored).some((s) => s.id === "music")).toBe(true);
});
