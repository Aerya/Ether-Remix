import { MemoryRouter } from "react-router-dom";
import { SettingAnchor, SettingFocus, settingHref } from "./SettingFocus";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { useState } from "react";
import { CollapsibleSections, SettingsSection } from "./CollapsibleSections";
import { Section } from "./layout";

function Draft() {
  const [value, setValue] = useState("");
  return (
    <input aria-label="Adresse" value={value} onChange={(event) => setValue(event.target.value)} />
  );
}

it("replie indépendamment les blocs et conserve les saisies et actions", async () => {
  const user = userEvent.setup();
  const action = vi.fn();
  render(
    <CollapsibleSections.Provider value={true}>
      <SettingsSection title="Réglages">
        <Draft />
      </SettingsSection>
      <Section title="Indexeurs" actions={<button onClick={action}>Ajouter</button>}>
        <p>Liste</p>
      </Section>
    </CollapsibleSections.Provider>,
  );
  const settings = screen.getByText("Réglages").closest("details")!;
  const indexers = screen.getByText("Indexeurs").closest("details")!;
  await user.type(screen.getByRole("textbox", { name: "Adresse" }), "https://example.test");
  await user.click(screen.getByText("Réglages"));
  expect(settings.open).toBe(false);
  expect(indexers.open).toBe(true);
  expect(screen.getByLabelText("Adresse")).toHaveValue("https://example.test");
  await user.click(screen.getByText("Réglages"));
  expect(settings.open).toBe(true);
  expect(screen.getByRole("textbox")).toHaveValue("https://example.test");
  await user.click(screen.getByRole("button", { name: "Ajouter" }));
  expect(action).toHaveBeenCalledOnce();
  expect(indexers.open).toBe(true);
});

it("conserve les blocs ordinaires hors des onglets concernés et dans les sous-réglages", () => {
  const { container } = render(
    <>
      <SettingsSection title="Musique">
        <p>Réglages musique</p>
      </SettingsSection>
      <Section title="Statistiques">
        <p>Graphique</p>
      </Section>
      <CollapsibleSections.Provider value={true}>
        <SettingsSection title="Bloc">
          <SettingsSection title="Sous-réglages">
            <Draft />
          </SettingsSection>
        </SettingsSection>
      </CollapsibleSections.Provider>
    </>,
  );
  expect(container.querySelectorAll("details")).toHaveLength(1);
  expect(screen.getByText("Musique").closest("details")).toBeNull();
  expect(screen.getByText("Statistiques").closest("details")).toBeNull();
});

it("ouvre le bloc lorsqu’un lien cible un réglage masqué", () => {
  render(
    <CollapsibleSections.Provider value={true}>
      <SettingsSection title="Indexation">
        <input id="pse-indexer-test" aria-label="Limite" />
      </SettingsSection>
    </CollapsibleSections.Provider>,
  );
  const block = screen.getByText("Indexation").closest("details")!;
  block.open = false;
  window.history.replaceState(null, "", "#pse-indexer-test");
  fireEvent(window, new Event("hashchange"));
  expect(block.open).toBe(true);
  window.history.replaceState(null, "", window.location.pathname);
});

it("révèle un réglage depuis la recherche même si son bloc est replié", async () => {
  Element.prototype.scrollIntoView ??= () => {};
  render(
    <MemoryRouter initialEntries={[settingHref("/accent/sources?source=indexation", "limit")]}>
      <CollapsibleSections.Provider value={true}>
        <SettingsSection title="Indexation">
          <SettingAnchor id="limit">
            <input aria-label="Limite" />
          </SettingAnchor>
        </SettingsSection>
      </CollapsibleSections.Provider>
      <SettingFocus />
    </MemoryRouter>,
  );
  const block = screen.getByText("Indexation").closest("details")!;
  block.open = false;
  await waitFor(() => expect(block.open).toBe(true));
  expect(document.querySelector(".setting-focus")).toContainElement(
    screen.getByLabelText("Limite"),
  );
});
