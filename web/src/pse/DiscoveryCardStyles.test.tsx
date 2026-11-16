import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { DiscoveryCardStyles, styleMatches } from "./DiscoveryCardStyles";
const fetch = vi.fn();
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch: (...args: unknown[]) => fetch(...args),
}));
it("matches creator and repository folder metadata without inventing a style", () => {
  expect(
    styleMatches({ name: "Netflix", url: "x", source: "services/flat/Netflix.png" }, "flat"),
  ).toBe(true);
  expect(
    styleMatches({ name: "Netflix", url: "x", source: "ThePosterDB", creator: "Artist" }, "artist"),
  ).toBe(true);
  expect(styleMatches({ name: "Netflix", url: "x", source: "ACdb" }, "flat")).toBe(false);
});
it("applies only reviewed unique matches and preserves missing or ambiguous cards", async () => {
  fetch.mockImplementation((url: string) =>
    Promise.resolve({
      images: url.includes("Netflix")
        ? [{ name: "Netflix", url: "https://example.com/n.png", source: "flat" }]
        : url.includes("Disney")
          ? [
              { name: "Disney", url: "https://example.com/d1.png", source: "flat" },
              { name: "Disney", url: "https://example.com/d2.png", source: "flat" },
            ]
          : [],
    }),
  );
  const apply = vi.fn();
  render(
    <DiscoveryCardStyles
      disabled={false}
      targets={["Netflix", "Disney", "Missing"].map((name, i) => ({
        key: name,
        name,
        group: "services",
        entity: i,
      }))}
      onApply={apply}
    />,
  );
  fireEvent.click(screen.getByText(/Style for multiple|Style pour plusieurs/));
  fireEvent.click(
    screen.getByRole("button", { name: /Search group artwork|Chercher les visuels du groupe/ }),
  );
  await waitFor(() => expect(screen.queryByText(/Searching…|Recherche en cours…/)).toBeNull());
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "flat" } });
  fireEvent.click(
    screen.getByRole("button", {
      name: /Select unique matches|Sélectionner les correspondances uniques/,
    }),
  );
  expect(apply).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: /Use selected artwork|Utiliser les visuels choisis/ }),
  );
  expect(apply).toHaveBeenCalledWith({ Netflix: "https://example.com/n.png" });
  expect(fetch.mock.calls.every((c) => c.length === 1)).toBe(true);
});
