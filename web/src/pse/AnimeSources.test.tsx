import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AnimeSources } from "./AnimeSources";
const editor = vi.fn();
vi.mock("../pages/sources/Indexers", () => ({
  IndexerList: (props: unknown) => {
    editor(props);
    return null;
  },
}));
it("delegates UwU and arbitrary manifests to the same shared Stremio editor", () => {
  render(<AnimeSources />);
  expect(screen.getByRole("link", { name: /UwU/ })).toHaveAttribute(
    "href",
    "https://uwu.nipva.com/",
  );
  fireEvent.click(screen.getByRole("button", { name: /^(Add|Ajouter) UwU/ }));
  expect(editor.mock.lastCall?.[0]).toMatchObject({
    view: "stremio",
    addRequest: { type: "stremio", name: "UwU+" },
  });
  fireEvent.click(screen.getByRole("button", { name: /another|autre/ }));
  expect(editor.mock.lastCall?.[0].addRequest).not.toHaveProperty("name");
});
