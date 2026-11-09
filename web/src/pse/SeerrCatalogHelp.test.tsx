import { render, screen, fireEvent } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { apiFetch } from "../lib/api";
vi.mock("../lib/api", () => ({ apiFetch: vi.fn() }));
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

it("tests saved settings and announces success", async () => {
  vi.mocked(apiFetch).mockResolvedValueOnce({ code: "ok" });
  render(<SeerrCatalogHelp />);
  fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
  expect(apiFetch).toHaveBeenCalledWith("/pse/seerrcatalog/test", { method: "POST" });
  expect(await screen.findByText(/Connection successful/)).toBeTruthy();
});

it("prevents duplicate clicks while waiting and reports rejected credentials", async () => {
  let finish!: (value: unknown) => void;
  vi.mocked(apiFetch).mockReturnValueOnce(
    new Promise((resolve) => {
      finish = resolve;
    }),
  );
  render(<SeerrCatalogHelp />);
  fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
  expect(screen.getByRole("button", { name: "Testing…" })).toBeDisabled();
  finish({ code: "unauthorized" });
  expect(await screen.findByText(/Access denied/)).toBeTruthy();
  expect(screen.getByRole("button", { name: "Test connection" })).toBeEnabled();
});

it("handles console request failure without showing raw errors", async () => {
  vi.mocked(apiFetch).mockRejectedValueOnce(new Error("private details"));
  render(<SeerrCatalogHelp />);
  fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
  expect(await screen.findByText(/check your Remix console connection/)).toBeTruthy();
  expect(screen.queryByText(/private details/)).toBeNull();
});
