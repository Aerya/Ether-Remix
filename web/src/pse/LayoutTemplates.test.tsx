import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { LayoutTemplates } from "./LayoutTemplates";
const fetch = vi.fn();
vi.mock("../lib/api", () => ({ apiFetch: (...args: unknown[]) => fetch(...args) }));
it("saves the current layout and duplicates a stored template without applying it", async () => {
  fetch.mockResolvedValue([{ id: "a", name: "Family", createdAt: 1 }]);
  render(<LayoutTemplates />);
  fireEvent.click(screen.getByText("Layout templates"));
  fireEvent.change(screen.getByLabelText("Template name"), { target: { value: "A" } });
  fireEvent.click(screen.getByRole("button", { name: "Save current layout" }));
  await screen.findByText("Template saved.");
  expect(fetch).toHaveBeenCalledWith(
    "/pse/layouts/",
    expect.objectContaining({ method: "POST", body: JSON.stringify({ name: "A", from: "" }) }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Duplicate" }));
  fireEvent.change(screen.getByLabelText("Template name"), { target: { value: "B" } });
  fireEvent.click(screen.getByRole("button", { name: "Save copy" }));
  await waitFor(() =>
    expect(fetch).toHaveBeenCalledWith(
      "/pse/layouts/",
      expect.objectContaining({ body: JSON.stringify({ name: "B", from: "a" }) }),
    ),
  );
  expect(fetch.mock.calls.some((c) => String(c[0]).endsWith("/apply"))).toBe(false);
});
it("protects an unsaved Home draft", async () => {
  fetch.mockResolvedValue([{ id: "a", name: "Family", createdAt: 1 }]);
  render(<LayoutTemplates dirty />);
  fireEvent.click(screen.getByText("Layout templates"));
  expect(await screen.findByRole("button", { name: "Apply" })).toBeDisabled();
  fireEvent.change(screen.getByLabelText("Template name"), { target: { value: "A" } });
  expect(screen.getByRole("button", { name: "Save current layout" })).toBeDisabled();
});
