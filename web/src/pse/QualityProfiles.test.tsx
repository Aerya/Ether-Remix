import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { QualityProfiles } from "./QualityProfiles";
const apiFetch = vi.fn();
vi.mock("../lib/api", () => ({ apiFetch: (...args: unknown[]) => apiFetch(...args) }));
it("keeps saved profiles as the discard baseline and does not save import previews", async () => {
  apiFetch.mockImplementation(async (path: string, opts?: { method: string; body: string }) => {
    if (path.endsWith("manifest")) return [];
    if (path.includes("themes")) return { libraries: [], collections: [] };
    if (path.endsWith("import")) return { rules: [], warnings: ["Unsupported filter"] };
    if (opts?.method === "PUT") return JSON.parse(opts.body);
    return { profiles: [] };
  });
  const onMode = vi.fn();
  render(<QualityProfiles onMode={onMode} />);
  fireEvent.click(await screen.findByRole("button", { name: "Add profile" }));
  fireEvent.change(screen.getByLabelText("Profile name"), { target: { value: "My profile" } });
  fireEvent.click(screen.getByRole("button", { name: "Save profiles" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Save profiles" })).toBeNull());
  expect(onMode).toHaveBeenLastCalledWith(true);
  fireEvent.change(screen.getByLabelText("Profile name"), { target: { value: "Draft" } });
  fireEvent.click(screen.getByRole("button", { name: "Discard changes" }));
  expect(screen.getByLabelText("Profile name")).toHaveValue("My profile");
  fireEvent.click(screen.getByText("Import AIOStreams filters"));
  fireEvent.change(screen.getByLabelText("JSON AIOStreams"), {
    target: { value: '{"requiredQualities":["Unknown"]}' },
  });
  fireEvent.click(screen.getByRole("button", { name: "Preview import" }));
  expect(await screen.findByText("Unsupported filter")).toBeInTheDocument();
  expect(apiFetch.mock.calls.filter((c) => c[1]?.method === "PUT")).toHaveLength(1);
});
