import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { BuildPanel } from "./BuildPanel";
import { blankSpec } from "../../lib/rules";

const { fetch } = vi.hoisted(() => ({ fetch: vi.fn() }));
vi.mock("../../lib/api", async (original) => ({
  ...(await original<typeof import("../../lib/api")>()),
  apiFetch: fetch,
}));
vi.mock("../../lib/extend", async (original) => ({
  ...(await original<typeof import("../../lib/extend")>()),
  useRegistry: () => ({ descriptors: [], loading: false, error: null, reload: vi.fn() }),
}));
vi.mock("./AddonCatalogDrawer", () => ({
  AddonCatalogDrawer: ({ onPick }: { onPick: (c: unknown) => void }) => (
    <button onClick={() => onPick({ kind: "plugin:xperience/adventure", mediaType: "show" })}>
      Pick Adventure
    </button>
  ),
}));

it("uses fresh catalogue defaults when creating from Home before registry reload", async () => {
  fetch.mockResolvedValue({
    descriptors: [
      {
        id: "plugin:xperience/adventure",
        meta: { editor: "plugin", defaults: { params: { genre: "None" } } },
      },
    ],
  });
  const update = vi.fn();
  render(<BuildPanel spec={blankSpec()} update={update} region="FR" pickAddonCatalog />);
  fireEvent.click(screen.getByText("Pick Adventure"));
  await waitFor(() =>
    expect(update).toHaveBeenCalledWith(
      expect.objectContaining({
        mediaType: "show",
        sources: [{ kind: "plugin:xperience/adventure", params: { genre: "None" } }],
      }),
    ),
  );
});
