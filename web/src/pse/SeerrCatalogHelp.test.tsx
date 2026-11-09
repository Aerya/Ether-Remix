import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { SeerrCatalogHelp } from "./SeerrCatalogHelp";

it("explains setup and fallback, links both projects and reports synchronization", () => {
  render(<SeerrCatalogHelp status={{ sync: { items: 42, lastSync: "2026-11-09T19:00:00Z" } }} />);
  expect(screen.getByRole("link", { name: "SeerrCatalog · GitHub" })).toHaveAttribute(
    "href",
    "https://github.com/Aerya/Stremio-Seerr-Catalog",
  );
  expect(screen.getByRole("link", { name: "Ether Remix · GitHub" })).toHaveAttribute(
    "href",
    "https://github.com/Aerya/Ether-Remix",
  );
  expect(screen.getByText(/42 items/)).toBeTruthy();
  expect(screen.getByText(/falls back to normal discovery/)).toBeTruthy();
});
