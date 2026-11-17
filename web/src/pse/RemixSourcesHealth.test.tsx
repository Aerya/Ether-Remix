import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { RemixSources } from "./RemixSources";

const apiFetch = vi.hoisted(() => vi.fn());
vi.mock("../lib/api", async (original) => ({
  ...(await original<typeof import("../lib/api")>()),
  apiFetch,
}));
vi.mock("./OwnedMedia", () => ({ LocalMedia: () => null }));
beforeEach(() => {
  apiFetch.mockReset();
  apiFetch.mockResolvedValue({ checked: 0, roots: [] });
});
afterEach(() => window.history.replaceState(null, "", "/"));
function renderLocal() {
  const { container } = render(
    <MemoryRouter initialEntries={["/remix/sources?source=local"]}>
      <RemixSources music={null} />
    </MemoryRouter>,
  );
  return container.querySelector<HTMLDetailsElement>("#local-media-health")!;
}
it("loads the diagnostic only on first opening and retains it until explicitly refreshed", async () => {
  const section = renderLocal();
  expect(section.open).toBe(false);
  expect(apiFetch).not.toHaveBeenCalled();
  fireEvent.click(section.querySelector("summary")!);
  await waitFor(() => expect(apiFetch).toHaveBeenCalledWith("/pse/owned/local/health"));
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: /Refresh diagnostics|Actualiser le diagnostic/ }),
    ).toBeEnabled(),
  );
  fireEvent.click(section.querySelector("summary")!);
  expect(section.open).toBe(false);
  fireEvent.click(section.querySelector("summary")!);
  expect(section.open).toBe(true);
  expect(apiFetch).toHaveBeenCalledTimes(1);
  fireEvent.click(
    screen.getByRole("button", { name: /Refresh diagnostics|Actualiser le diagnostic/ }),
  );
  await waitFor(() => expect(apiFetch).toHaveBeenCalledTimes(2));
});
it("opens and loads the diagnostic when arriving through its anchor", async () => {
  window.history.replaceState(null, "", "/remix/sources?source=local#local-media-health");
  const section = renderLocal();
  expect(section.open).toBe(true);
  await waitFor(() => expect(apiFetch).toHaveBeenCalledWith("/pse/owned/local/health"));
});
