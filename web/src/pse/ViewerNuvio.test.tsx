import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { apiFetch } from "../lib/api";
import { safeNuvioUrl, ViewerNuvio } from "./ViewerNuvio";
vi.mock("../lib/api", () => ({ apiFetch: vi.fn(), errorMessage: (e: unknown) => String(e) }));
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

const linked = {
  linked: true,
  link: {
    profileIndex: 4,
    profileName: "Enfants",
    chosen: true,
    pull: true,
    push: true,
    lastCycle: 0,
    imported: 3,
    sent: 1,
    unknown: 2,
  },
  profiles: [
    { id: "a", profileIndex: 1, name: "Parents" },
    { id: "b", profileIndex: 4, name: "Enfants" },
  ],
};

it("only opens https nuvio.tv links", () => {
  expect(safeNuvioUrl("https://nuvio.tv/tv-login?code=A")).toContain("nuvio.tv");
  expect(safeNuvioUrl("http://nuvio.tv/x")).toBeNull();
  expect(safeNuvioUrl("https://evil.example/x")).toBeNull();
});

it("starts the link and shows the code", async () => {
  vi.mocked(apiFetch).mockImplementation(async (path: string) => {
    if (path.endsWith("/start")) {
      return {
        linked: false,
        login: {
          code: "ABC123",
          webUrl: "https://nuvio.tv/tv-login?code=ABC123",
          expiresAt: "",
          interval: 60,
        },
      };
    }
    return { linked: false };
  });
  render(<ViewerNuvio viewerId={7} />);
  fireEvent.click(await screen.findByRole("button", { name: /^(Relier|Link)$/ }));
  expect(await screen.findByText("ABC123")).toBeTruthy();
  expect(vi.mocked(apiFetch).mock.calls.some((c) => c[0] === "/pse/viewers/7/nuvio/start")).toBe(
    true,
  );
});

it("shows the linked state, counters and syncs on demand", async () => {
  vi.mocked(apiFetch).mockImplementation(async (path: string) => {
    if (path.endsWith("/sync")) return { summary: { imported: 2, unknown: 0 }, status: linked };
    return linked;
  });
  render(<ViewerNuvio viewerId={7} />);
  expect((await screen.findAllByText(/Enfants/)).length).toBeGreaterThan(0);
  expect(screen.getByRole("checkbox", { name: "Nuvio → Ether" })).toBeTruthy();
  expect(screen.getByRole("checkbox", { name: "Ether → Nuvio" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /Synchroniser maintenant|Sync now/ }));
  await waitFor(() =>
    expect(vi.mocked(apiFetch).mock.calls.some((c) => c[0] === "/pse/viewers/7/nuvio/sync")).toBe(
      true,
    ),
  );
});
