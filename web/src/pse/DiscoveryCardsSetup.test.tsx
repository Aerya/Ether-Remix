import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, expect, it, vi } from "vitest";
import { DiscoveryCardsSetup } from "./DiscoveryCardsSetup";
const fetch = vi.fn();
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => fetch(...args),
}));
const a24 = {
  id: "a24",
  name: "A24",
  group: "studios",
  source: "tmdbCompany",
  entity: 41077,
  accent: "#354352",
};
const service = {
  id: "services-50",
  name: "Test Service",
  group: "services",
  source: "watchProvider",
  entity: 50,
  accent: "#354352",
};
beforeEach(() => {
  fetch.mockReset();
});
function mount() {
  render(
    <MemoryRouter>
      <DiscoveryCardsSetup />
    </MemoryRouter>,
  );
  fireEvent.click(screen.getByRole("button", { name: /Configuration rapide|Quick setup/ }));
}
it("labels beta and applies only on explicit action, preserving artwork unless edited", async () => {
  let enabled = false;
  fetch.mockImplementation(async (_path: string, options?: { method: string }) => {
    if (options?.method === "POST") enabled = true;
    if (options?.method === "DELETE") enabled = false;
    return {
      enabled,
      cards: [a24],
      choices: [
        {
          card: a24,
          region: "FR",
          artwork: { mode: "custom", url: "https://example.com/old.jpg" },
        },
      ],
    };
  });
  mount();
  await screen.findByText(/1 \/ 200/);
  expect(screen.getByText(/Bêta|Beta/)).toBeInTheDocument();
  expect(fetch.mock.calls.some((c) => c[1]?.method === "POST")).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: /Appliquer les cartes|Apply cards/ }));
  await screen.findByRole("status");
  const body = JSON.parse(fetch.mock.calls.find((c) => c[1]?.method === "POST")![1].body);
  expect(body.choices).toEqual([{ card: a24, region: "FR" }]);
  fireEvent.click(screen.getByRole("button", { name: /Rétablir|Restore/ }));
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: /Rétablir|Restore/ })).not.toBeInTheDocument(),
  );
});
it("keeps country selections distinct and sends both to the server", async () => {
  fetch.mockImplementation(async (path: string) =>
    path.includes("catalog?")
      ? { cards: [service], logos: {} }
      : { enabled: false, cards: [], choices: [] },
  );
  mount();
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  await waitFor(() => expect(screen.getByRole("checkbox", { name: "Test Service" })).toBeEnabled());
  fireEvent.click(screen.getByRole("checkbox", { name: "Test Service" }));
  fireEvent.change(screen.getByLabelText(/Pays des services|Services country/), {
    target: { value: "US" },
  });
  await waitFor(() => expect(screen.getByRole("checkbox", { name: "Test Service" })).toBeEnabled());
  fireEvent.click(screen.getByRole("checkbox", { name: "Test Service" }));
  await screen.findByText(/2 \/ 200/);
  fireEvent.click(screen.getByRole("button", { name: /Appliquer les cartes|Apply cards/ }));
  await waitFor(() => expect(fetch.mock.calls.some((c) => c[1]?.method === "POST")).toBe(true));
  const body = JSON.parse(fetch.mock.calls.find((c) => c[1]?.method === "POST")![1].body);
  expect(body.groupCountries).toBe(true);
  expect(body.choices.map((c: { region: string }) => c.region)).toEqual(["FR", "US"]);
});
it.each(["github", "ACdb", "ThePosterDB"])(
  "previews %s gallery artwork and explicitly saves that artwork",
  async (provider) => {
    fetch.mockImplementation(async (path: string) =>
      path.includes("gallery?")
        ? {
            images: [
              { name: "A24 art", url: "https://example.com/new.jpg", source: "posters/A24.jpg" },
            ],
          }
        : { enabled: false, cards: [a24], choices: [{ card: a24, region: "FR" }] },
    );
    mount();
    await screen.findByText(/1 \/ 200/);
    fireEvent.click(screen.getByRole("button", { name: "A24" }));
    const editor = await screen.findByRole("region", { name: /Choix du visuel|Artwork picker/ });
    fireEvent.change(within(editor).getByLabelText(/Source du visuel|Artwork source/), {
      target: { value: provider },
    });
    fireEvent.click(
      within(editor).getByRole("button", { name: /Chercher des visuels|Search artwork/ }),
    );
    fireEvent.click(await within(editor).findByRole("button", { name: /A24 art/ }));
    expect(within(editor).getByAltText("A24")).toHaveAttribute(
      "src",
      "https://example.com/new.jpg",
    );
    fireEvent.click(screen.getByRole("button", { name: /Appliquer les cartes|Apply cards/ }));
    await waitFor(() => expect(fetch.mock.calls.some((c) => c[1]?.method === "POST")).toBe(true));
    expect(fetch.mock.calls.some((c) => c[0].includes("provider=" + provider))).toBe(true);
    const body = JSON.parse(fetch.mock.calls.find((c) => c[1]?.method === "POST")![1].body);
    expect(body.choices[0].artwork).toEqual({ mode: "custom", url: "https://example.com/new.jpg" });
  },
);

it("offers integrated galleries directly under the selected card", async () => {
  fetch.mockImplementation(async (path: string) =>
    path.includes("catalog?")
      ? { cards: [service], logos: {} }
      : { enabled: false, cards: [], choices: [] },
  );
  mount();
  const check = await screen.findByRole("checkbox", { name: "Test Service" });
  await waitFor(() => expect(check).toBeEnabled());
  fireEvent.click(check);
  fireEvent.click(screen.getByRole("button", { name: /Personnaliser une carte|Customize a card/ }));
  const editor = screen.getByRole("region", { name: /Choix du visuel|Artwork picker/ });
  expect(editor.parentElement).toContainElement(check);
  for (const name of ["ThePosterDB", "ACdb"]) {
    fireEvent.change(within(editor).getByLabelText(/Source du visuel|Artwork source/), {
      target: { value: name },
    });
    expect(within(editor).getByRole("link", { name })).toBeInTheDocument();
    expect(
      within(editor).getByRole("button", { name: /Chercher des visuels|Search artwork/ }),
    ).toBeInTheDocument();
  }
});
