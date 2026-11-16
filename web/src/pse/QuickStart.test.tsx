import { render, screen, cleanup } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { QuickStart, QuickStartHint } from "./QuickStart";
import { REMIX_TABS } from "./navigation";
vi.mock("../i18n", () => ({ getLocale: () => "fr" }));
afterEach(cleanup);
it("exposes the guide and directs users to the actual indexing settings", () => {
  render(
    <MemoryRouter>
      <QuickStart indexing={false} />
    </MemoryRouter>,
  );
  expect(REMIX_TABS.some((t) => t.key === "demarrage")).toBe(true);
  expect(screen.getAllByRole("listitem")).toHaveLength(6);
  expect(screen.getByText("Réglage enregistré : désactivée")).toBeVisible();
  expect(screen.getByRole("link", { name: "Régler l’indexation" })).toHaveAttribute(
    "href",
    "/remix/sources?source=indexation#pse-pse_indexer",
  );
  expect(screen.getByRole("link", { name: "Connecter un client" })).toHaveAttribute(
    "href",
    "/apps",
  );
});
it("reports the saved enabled setting without claiming successful searches", () => {
  render(
    <MemoryRouter>
      <QuickStart indexing />
    </MemoryRouter>,
  );
  expect(screen.getByText("Réglage enregistré : activée")).toBeVisible();
  expect(screen.queryByText("Réglage enregistré : désactivée")).toBeNull();
});
it("links the disabled-indexing reminder to the guide", () => {
  render(
    <MemoryRouter>
      <QuickStartHint />
    </MemoryRouter>,
  );
  expect(screen.getByRole("link", { name: "Démarrage rapide" })).toHaveAttribute(
    "href",
    "/remix/demarrage",
  );
});
