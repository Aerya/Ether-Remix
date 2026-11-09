import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ImportedSources } from "./ImportedSources";
vi.mock("../lib/api", () => ({
  api: {
    getConfig: async () => ({
      indexers: [
        { id: "a", name: "Zeta", kind: "stremio", enabled: true },
        { id: "b", name: "Alpha", protocol: "torrent", enabled: true },
        { id: "c", name: "Beta", protocol: "usenet", enabled: true },
      ],
    }),
  },
}));
it("includes newly configured sources and keeps hidden selections when filtering", async () => {
  const change = vi.fn();
  render(
    <ImportedSources
      value="c"
      onChange={change}
      options={["a", "c"].map((value) => ({ value, label: { fr: value, en: value } }))}
    />,
  );
  await screen.findByText("Zeta");
  fireEvent.click(screen.getByLabelText(/Source category|Catégorie de source/));
  fireEvent.click(await screen.findByRole("option", { name: "BitTorrent" }));
  expect(screen.queryByText("Zeta")).toBeNull();
  fireEvent.click(screen.getByLabelText("Alpha"));
  expect(change).toHaveBeenLastCalledWith("b,c");
  fireEvent.click(screen.getByLabelText(/Source category|Catégorie de source/));
  expect(await screen.findByRole("option", { name: "Usenet" })).toBeTruthy();
});
