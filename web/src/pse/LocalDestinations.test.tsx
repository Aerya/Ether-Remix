import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { expect, it, vi } from "vitest";
import { LocalDestinations } from "./LocalDestinations";
const apiFetch = vi.fn();
vi.mock("../lib/api", () => ({ apiFetch: (...args: unknown[]) => apiFetch(...args) }));
it("loads destinations on demand and saves one link without altering sharing", async () => {
  let linked = false;
  apiFetch.mockImplementation(async (_path: string, opts?: { method: string }) => {
    if (opts?.method === "PUT") {
      linked = true;
      return {};
    }
    return [{ id: "3", name: "Cartoons", kind: "library", mediaType: "movie", linked }];
  });
  render(
    <MemoryRouter>
      <LocalDestinations rootId="root1" />
    </MemoryRouter>,
  );
  expect(apiFetch).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText(/Libraries and collections|Bibliothèques et collections/));
  const box = await screen.findByRole("checkbox", { name: /Cartoons/ });
  fireEvent.click(box);
  await waitFor(() =>
    expect(apiFetch).toHaveBeenCalledWith("/pse/owned/local/destinations", {
      method: "PUT",
      body: JSON.stringify({ rootId: "root1", kind: "library", id: "3", linked: true }),
    }),
  );
  await waitFor(() => expect(box).toBeChecked());
  expect(apiFetch.mock.calls.some((c) => c[0].includes("sharing"))).toBe(false);
});
