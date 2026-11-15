import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ScrobLinks } from "../pse/ScrobLinks";
vi.mock("../lib/api", () => ({
  apiFetch: vi.fn(async (url: string) =>
    url.includes("/apps/")
      ? { url: "https://example.com/jellyfin", password: "test-token", userId: "viewer-id" }
      : { enabled: true, viewers: [{ id: 1, name: "Viewer", url: "", configured: false }] },
  ),
}));
it("copies the real generated token while keeping the input masked", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
  render(<ScrobLinks />);
  fireEvent.click(
    await screen.findByRole("button", { name: "Create Jellyfin credentials for Scrob" }),
  );
  const copy = await screen.findByRole("button", { name: "Copy Token" });
  expect(screen.getByLabelText("Token")).toHaveAttribute("type", "password");
  fireEvent.click(copy);
  await waitFor(() => expect(writeText).toHaveBeenCalledWith("test-token"));
});
