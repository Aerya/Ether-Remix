import { confirmDialog } from "@braedonsaunders/appkit-ui";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/api";
import Apps from "../pages/apps/Apps";

const apiFetch = vi.fn();
vi.mock("@braedonsaunders/appkit-ui", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@braedonsaunders/appkit-ui")>()),
  confirmDialog: vi.fn(async () => true),
}));
vi.mock("../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/api")>();
  return {
    ...actual,
    apiFetch: (...args: unknown[]) => apiFetch(...args),
    apiBlob: () => Promise.reject(new Error("QR unavailable")),
  };
});
vi.mock("../app-context", () => ({
  useApp: () => ({
    dashboard: { sections: { remote: {} } },
    reloadBoot: vi.fn(),
    boot: { serverName: "Living Room Ether" },
  }),
}));

describe("Vela Skins console section", () => {
  beforeEach(() => {
    apiFetch.mockReset();
    vi.mocked(confirmDialog).mockClear();
    vi.mocked(confirmDialog).mockResolvedValue(true);
  });

  it("renders built-in and installed skins, saves default skin + force, validates installs, and removes installed skins", async () => {
    let savedSettings: Record<string, unknown> = { defaultSkinId: "cinematic", force: false };
    let skins = [
      {
        id: "cinematic-doc",
        name: "Cinematic",
        version: "1.0.0",
        kind: "document",
        builtin: true,
        swatches: ["#0b0d12", "#151923", "#e5a00d", "#f4f6fb"],
      },
      {
        id: "mochi",
        name: "Mochi",
        version: "1.0.0",
        kind: "document",
        builtin: true,
        swatches: ["#faf6f2", "#ffffff", "#e86a92", "#26222b"],
      },
      {
        id: "harbor",
        name: "Harbor",
        version: "1.0.0",
        kind: "pack",
        builtin: false,
        preview: "/vela/v0/appearance/packs/harbor/1.0.0/preview.svg",
        swatches: ["#0b1320", "#132238", "#4cc9f0", "#eef4fb"],
        installedAt: 1731900000,
      },
    ];

    apiFetch.mockImplementation(async (path: string, options?: RequestInit) => {
      if (path === "/frontends" && !options?.method) {
        return [
          {
            descriptor: { id: "vela", name: "Vela", settingsSchema: { sections: [] } },
            enabled: true,
            status: {
              webPlayerReady: true,
              addresses: ["http://192.168.68.65:32550/vela"],
              connectUrls: ["http://192.168.68.65:32550/vela"],
              lan: "reachable",
              wan: "unknown",
              clients24h: 0,
              clients24hKnown: true,
              liveStreams: 0,
              liveStreamsKnown: true,
              targetVersion: "1",
              conformance: "passing",
            },
          },
        ];
      }
      if (path === "/frontends/vela/settings" && !options?.method) return savedSettings;
      if (path === "/frontends/vela/settings" && options?.method === "PUT") {
        savedSettings = JSON.parse(String(options.body));
        return { ok: true };
      }
      if (path === "/vela/status" || path === "/vela/check") {
        return {
          installed: true,
          installedVersion: "0.4.0",
          availableVersion: "",
          rollbackAvailable: false,
          busy: false,
          phase: "",
          downloaded: 0,
          total: 0,
        };
      }
      if (path === "/web-player") {
        return {
          available: true,
          players: [{ id: "vela", url: "http://192.168.68.65:32550/vela/" }],
        };
      }
      if (path === "/vela/skins" && !options?.method) {
        return { skins };
      }
      if (path === "/vela/skins" && options?.method === "POST") {
        const parsed = JSON.parse(String(options.body)) as { url?: string; lan?: boolean };
        if (!parsed.lan && parsed.url?.includes("192.168.")) {
          throw new ApiError(
            400,
            "ssrf_blocked",
            "Private-network URLs are blocked unless local network access is allowed.",
          );
        }
        const installed = {
          id: "aurora",
          name: "Aurora",
          version: "1.0.0",
          kind: "pack",
          builtin: false,
          swatches: ["#090d16", "#131c2e", "#38bdf8", "#f8fafc"],
        };
        skins = [...skins, installed];
        return installed;
      }
      if (path === "/vela/skins/harbor" && options?.method === "DELETE") {
        skins = skins.filter((s) => s.id !== "harbor");
        return { ok: true };
      }
      throw new Error(`Unexpected request: ${path}`);
    });

    render(
      <MemoryRouter initialEntries={["/apps/vela"]}>
        <Routes>
          <Route path="/apps/:tab" element={<Apps />} />
        </Routes>
      </MemoryRouter>,
    );

    const grid = await screen.findByTestId("vela-skins-grid");
    const mochiCard = grid.querySelector('[data-skin-id="mochi"]') as HTMLElement;
    const harborCard = grid.querySelector('[data-skin-id="harbor"]') as HTMLElement;
    expect(within(mochiCard).getByText("Built-in")).toBeInTheDocument();
    expect(within(mochiCard).queryByRole("button", { name: /Remove/ })).not.toBeInTheDocument();
    expect(within(harborCard).getByText("v1.0.0")).toBeInTheDocument();
    expect(within(harborCard).queryByText("Built-in")).not.toBeInTheDocument();
    expect(within(harborCard).getByRole("button", { name: "Remove Harbor" })).toBeInTheDocument();

    // Select Harbor as default skin and turn on "Use this skin for everyone".
    fireEvent.click(screen.getByRole("button", { name: "Default skin" }));
    fireEvent.click(await screen.findByRole("option", { name: "Harbor" }));
    fireEvent.click(screen.getByRole("switch", { name: "Use this skin for everyone" }));
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() =>
      expect(savedSettings).toEqual({
        defaultSkinId: "harbor",
        force: true,
      }),
    );

    // Open Install skin drawer, trigger SSRF inline error, toggle LAN, and succeed.
    fireEvent.click(screen.getByRole("button", { name: "Install skin" }));
    const urlInput = await screen.findByLabelText("Package URL");
    fireEvent.change(urlInput, {
      target: { value: "http://192.168.68.10:8080/aurora.vskin" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Install" }));

    expect(
      await screen.findByText(
        "Private-network URLs are blocked unless local network access is allowed.",
      ),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("switch", { name: "Allow local network address" }));
    fireEvent.click(screen.getByRole("button", { name: "Install" }));

    await waitFor(() => {
      expect(grid.querySelector('[data-skin-id="aurora"]')).not.toBeNull();
    });

    // Remove Harbor with confirmation; active default skin falls back to cinematic.
    fireEvent.click(screen.getByRole("button", { name: "Remove Harbor" }));
    await waitFor(() => expect(confirmDialog).toHaveBeenCalledTimes(1));
    await waitFor(() => {
      expect(grid.querySelector('[data-skin-id="harbor"]')).toBeNull();
    });
  });
});
