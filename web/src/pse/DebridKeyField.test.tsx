import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { DebridKeyField } from "./DebridKeyField";
const apiFetch = vi.fn();
vi.mock("../lib/api", () => ({ apiFetch: (...args: unknown[]) => apiFetch(...args) }));
beforeEach(() => apiFetch.mockReset());
const props = { providerId: "rd", kind: "realdebrid", value: "••••", ariaLabel: "API key" };
it("only fetches the selected stored key after a click, and hides it without changing the draft", async () => {
  apiFetch.mockResolvedValue({ key: "fixture-key" });
  const change = vi.fn();
  render(<DebridKeyField {...props} onChange={change} />);
  expect(apiFetch).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Show API key" }));
  expect(await screen.findByDisplayValue("fixture-key")).toHaveAttribute("readonly");
  expect(apiFetch).toHaveBeenCalledWith(
    "/debrid/providers/rd/key",
    expect.objectContaining({ method: "POST", body: JSON.stringify({ kind: "realdebrid" }) }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Hide API key" }));
  expect(screen.queryByDisplayValue("fixture-key")).toBeNull();
  expect(change).not.toHaveBeenCalled();
});
it("discards a pending response when switching account", async () => {
  let resolve!: (value: { key: string }) => void;
  apiFetch.mockReturnValue(
    new Promise((r) => {
      resolve = r;
    }),
  );
  const view = render(<DebridKeyField {...props} onChange={() => {}} />);
  fireEvent.click(screen.getByRole("button", { name: "Show API key" }));
  view.rerender(<DebridKeyField {...props} providerId="another" onChange={() => {}} />);
  resolve({ key: "old-account-key" });
  await waitFor(() => expect(screen.getByRole("button", { name: "Show API key" })).toBeEnabled());
  expect(screen.queryByDisplayValue("old-account-key")).toBeNull();
});
