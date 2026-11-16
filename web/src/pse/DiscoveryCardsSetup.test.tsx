import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { expect, it, vi } from "vitest";
import { DiscoveryCardsSetup } from "./DiscoveryCardsSetup";
const fetch = vi.fn();
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => fetch(...args),
}));
it("only applies on explicit action, sends the selection and can restore without deleting collections", async () => {
  let enabled = false;
  fetch.mockImplementation(async (_path: string, options?: { method: string }) => {
    if (options?.method === "POST") enabled = true;
    if (options?.method === "DELETE") enabled = false;
    return {
      enabled,
      cards: [
        { id: "a24", name: "A24", group: "studios", accent: "#354352" },
        { id: "action", name: "Action", group: "genres", accent: "#a34825" },
      ],
    };
  });
  render(
    <MemoryRouter>
      <DiscoveryCardsSetup />
    </MemoryRouter>,
  );
  expect(fetch).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: /Configuration rapide|Quick setup/ }));
  await screen.findByLabelText("A24");
  fireEvent.click(screen.getByLabelText("Action"));
  fireEvent.click(screen.getByRole("button", { name: /Appliquer les cartes|Apply cards/ }));
  await screen.findByRole("status");
  const call = fetch.mock.calls.find((c) => c[1]?.method === "POST");
  expect(JSON.parse(call![1].body)).toEqual({ cards: ["a24"], region: "FR" });
  fireEvent.click(await screen.findByRole("button", { name: /Rétablir|Restore/ }));
  await waitFor(() => expect(fetch.mock.calls.some((c) => c[1]?.method === "DELETE")).toBe(true));
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: /Rétablir|Restore/ })).not.toBeInTheDocument(),
  );
});
