import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { LocalMediaHealth } from "./LocalMediaHealth";
const apiFetch = vi.fn();
vi.mock("../lib/api", () => ({ apiFetch: (...args: unknown[]) => apiFetch(...args) }));
afterEach(() => {
  cleanup();
  apiFetch.mockReset();
});
const fixture = {
  checked: 1000,
  roots: [
    {
      id: "r",
      name: "Movies",
      kind: "video",
      state: "available",
      bytes: 30,
      knownBytes: 40,
      files: [
        {
          id: 1,
          name: "Small",
          path: "small.mp4",
          size: 10,
          state: "available",
          unidentified: true,
          duplicate: false,
        },
        {
          id: 2,
          name: "Large",
          path: "large.mp4",
          size: 20,
          state: "available",
          unidentified: false,
          duplicate: true,
        },
        {
          id: 3,
          name: "Gone",
          path: "gone.mp4",
          size: 10,
          state: "missing",
          unidentified: false,
          duplicate: false,
        },
      ],
    },
  ],
};
it("sorts by size, filters missing files and links to local sources", async () => {
  apiFetch.mockResolvedValue(fixture);
  render(
    <MemoryRouter>
      <LocalMediaHealth />
    </MemoryRouter>,
  );
  await screen.findByText("Large");
  const rows = screen.getAllByRole("row");
  expect(rows[1]).toHaveTextContent("Large");
  expect(screen.getByRole("link")).toHaveAttribute("href", "/remix/sources?source=local");
  fireEvent.change(screen.getByRole("combobox", { name: /État|State/ }), {
    target: { value: "missing" },
  });
  expect(screen.queryByText("Large")).toBeNull();
  expect(screen.getByText("Gone")).toBeTruthy();
  fireEvent.change(screen.getByRole("combobox", { name: /État|State/ }), {
    target: { value: "unidentified" },
  });
  expect(screen.getByText("Small")).toBeTruthy();
  expect(screen.queryByText("Gone")).toBeNull();
  expect(apiFetch).toHaveBeenCalledTimes(1);
});
it("retains the dated snapshot when a manual refresh fails", async () => {
  apiFetch.mockResolvedValueOnce(fixture).mockRejectedValueOnce(new Error("offline"));
  render(
    <MemoryRouter>
      <LocalMediaHealth />
    </MemoryRouter>,
  );
  await screen.findByText("Large");
  fireEvent.click(
    screen.getByRole("button", { name: /Actualiser le diagnostic|Refresh diagnostics/ }),
  );
  await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("offline"));
  expect(screen.getByText("Large")).toBeTruthy();
  expect(apiFetch).toHaveBeenCalledTimes(2);
});
it("paginates large inventories and resets the page on search", async () => {
  apiFetch.mockResolvedValue({
    ...fixture,
    roots: [
      {
        ...fixture.roots[0],
        files: Array.from({ length: 51 }, (_, id) => ({
          id,
          name: `Title ${id}`,
          path: `${id}.mp4`,
          size: id,
          state: "available",
          unidentified: false,
          duplicate: false,
        })),
      },
    ],
  });
  render(
    <MemoryRouter>
      <LocalMediaHealth />
    </MemoryRouter>,
  );
  await screen.findByText("Title 50");
  expect(screen.queryByText("Title 0")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /Suivant|Next/ }));
  expect(screen.getByText("Title 0")).toBeTruthy();
  fireEvent.change(screen.getByRole("textbox", { name: /Rechercher|Search/ }), {
    target: { value: "50.mp4" },
  });
  expect(screen.getByText("Title 50")).toBeTruthy();
  expect(screen.queryByText("Title 0")).toBeNull();
});
