import { ConfirmRoot } from "@braedonsaunders/appkit-ui";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import Apps from "../pages/apps/Apps";
import {
  classifyAppAddresses,
  ServerAddressPanel,
  type HubFrontendInfo,
} from "../pages/apps/ConnectionHub";
import { usableAddresses } from "../pages/apps/reachability";

const apiFetch = vi.fn();
const apiBlob = vi.fn();
vi.mock("../lib/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/api")>();
  return {
    ...actual,
    apiFetch: (...args: unknown[]) => apiFetch(...args),
    apiBlob: (...args: unknown[]) => apiBlob(...args),
  };
});
vi.mock("../app-context", () => ({
  useApp: () => ({
    dashboard: { sections: { remote: {} } },
    reloadBoot: vi.fn(),
    boot: { serverName: "Living Room Ether" },
  }),
}));

type AppRow = {
  descriptor: {
    id: string;
    name: string;
    defaultPort?: number;
    certifiedApps?: { name: string; steps: string[] }[];
    settingsSchema?: unknown;
  };
  enabled: boolean;
  status: {
    webPlayerReady?: boolean;
    addresses: string[];
    connectUrls?: string[];
    busyPort?: number;
    actualPort?: number;
    busyPortHolder?: string;
    warnings?: string[];
    observedAddresses?: string[];
    publishedAddresses?: string[];
    lan: string;
    wan: string;
    clients24h: number;
    clients24hKnown: boolean;
    liveStreams: number;
    liveStreamsKnown: boolean;
    targetVersion: string;
    conformance: string;
    error?: string;
  };
};

const servedSchema = {
  signInHelp: true,
  sections: [
    {
      key: "find",
      fields: [
        { key: "serverName", kind: "serverName" },
        { key: "port", kind: "port", min: 1, max: 65535 },
        { key: "discovery", kind: "bool", more: true },
      ],
    },
  ],
};

const defaultConfig = () => ({
  server: {
    port: 32400,
    friendlyName: "Living Room Ether",
    allowRemoteConsole: false,
    bindAddress: "0.0.0.0",
    machineIdentifier: "ether-machine-1",
    webFrameOrigins: [],
    trustedProxyCIDRs: [],
  },
  plex: {
    claimed: true,
    ownerUsername: "Braedon",
    remoteAccess: true,
  },
});

function row(
  id: string,
  name: string,
  enabled: boolean,
  extra?: {
    descriptor?: Partial<AppRow["descriptor"]>;
    status?: Partial<AppRow["status"]>;
  },
): AppRow {
  const defaultPort =
    id === "jellyfin"
      ? 8096
      : id === "emby"
        ? 8097
        : id === "silo"
          ? 8090
          : id === "plex"
            ? 32400
            : undefined;
  const addr = defaultPort ? `http://192.168.1.2:${defaultPort}` : `http://192.168.1.2:32400/${id}`;
  return {
    descriptor: {
      id,
      name,
      ...(defaultPort ? { defaultPort } : {}),
      settingsSchema: servedSchema,
      ...extra?.descriptor,
    },
    enabled,
    status: {
      addresses: enabled ? [addr] : [],
      connectUrls: enabled ? [addr] : [],
      lan: "reachable",
      wan: "unknown",
      clients24h: 1,
      clients24hKnown: true,
      liveStreams: 0,
      liveStreamsKnown: true,
      targetVersion: "1",
      conformance: "passing",
      ...extra?.status,
    },
  };
}

function LocationProbe() {
  const location = useLocation();
  return <div data-testid="loc">{`${location.pathname}${location.search}`}</div>;
}

function renderApps(entry: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <LocationProbe />
      <Routes>
        <Route path="/apps/:tab?/:section?" element={<Apps />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("Apps connection hub", () => {
  beforeEach(() => {
    apiFetch.mockReset();
    apiBlob.mockReset();
    apiBlob.mockRejectedValue(new Error("QR unavailable"));
  });

  it("always prioritizes dedicated listener ports over shared-port paths", () => {
    const status = {
      connectUrls: [
        "http://192.168.68.65:32400/jellyfin",
        "http://192.168.68.65:8096",
        "http://100.65.221.123:8096",
        "http://[fd79:f184::1]:8096",
        "https://jelly.example.com",
      ],
      addresses: ["http://192.168.68.65:8096"],
    };
    expect(usableAddresses(status)[0]).toBe("http://192.168.68.65:8096");
    const classified = classifyAppAddresses(status, { preferredPort: "8096" });
    expect(classified.primary).toBe("http://192.168.68.65:8096");
    expect(classified.others).toEqual([
      { address: "http://192.168.68.65:32400/jellyfin", label: "Also reachable at" },
      { address: "http://100.65.221.123:8096", label: "Tailscale" },
      { address: "https://jelly.example.com", label: "Remote" },
      { address: "http://[fd79:f184::1]:8096", label: "LAN IPv6" },
    ]);
  });

  it("renders the Server address panel and canonical app rows on Overview without inline port editors", async () => {
    let remoteConsole = false;
    apiFetch.mockImplementation(async (path: string, options?: RequestInit) => {
      if (path === "/config" && !options?.method) {
        const cfg = defaultConfig();
        cfg.server.allowRemoteConsole = remoteConsole;
        return cfg;
      }
      if (path === "/config/server" && options?.method === "PUT") {
        const body = JSON.parse(String(options.body));
        remoteConsole = Boolean(body.allowRemoteConsole);
        return { ok: true };
      }
      if (path === "/frontends" && !options?.method) {
        return [
          row("xtream", "IPTV apps (Xtream)", false),
          row("plex", "Plex", true, {
            status: {
              addresses: ["127.0.0.1:32400"],
              connectUrls: [
                "http://192.168.1.2:32400",
                "http://100.64.0.2:32400",
                "https://203-0-113-7.abc.plex.direct:32400",
              ],
              lan: "reachable",
              wan: "reachable",
              clients24h: 0,
              clients24hKnown: true,
              liveStreams: 0,
              liveStreamsKnown: true,
              targetVersion: "1",
              conformance: "passing",
            },
          }),
          row("vela", "Vela", true, {
            status: {
              webPlayerReady: true,
              addresses: ["http://192.168.1.2:32400/vela"],
              connectUrls: ["http://192.168.1.2:32400/vela"],
              lan: "reachable",
              wan: "unknown",
              clients24h: 0,
              clients24hKnown: true,
              liveStreams: 0,
              liveStreamsKnown: true,
              targetVersion: "1",
              conformance: "passing",
            },
          }),
          row("jellyfin", "Jellyfin", true),
          row("emby", "Emby", false),
          row("stremio", "Stremio", true),
          row("silo", "Silo", true),
          row("dlna", "DLNA", false),
          row("webdav", "Files (WebDAV)", true),
          row("m3u", "M3U & XMLTV", false),
        ];
      }
      throw new Error(`Unexpected request: ${path}`);
    });

    renderApps("/apps");
    await waitFor(() =>
      expect(document.querySelectorAll("[data-app-id]").length).toBeGreaterThan(0),
    );
    // Server address lives on General (Server › General), not Apps.
    expect(screen.queryByRole("heading", { name: "Server address" })).toBeNull();

    const rows = Array.from(document.querySelectorAll("[data-app-id]"));
    expect(rows.map((el) => el.getAttribute("data-app-id"))).toEqual([
      "vela",
      "plex",
      "jellyfin",
      "emby",
      "stremio",
      "silo",
      "dlna",
      "webdav",
      "m3u",
      "xtream",
    ]);

    const jellyfinRow = document.querySelector('[data-app-id="jellyfin"]') as HTMLElement;
    expect(within(jellyfinRow).getByText("Listening")).toBeInTheDocument();
    expect(within(jellyfinRow).getByText("http://192.168.1.2:8096")).toBeInTheDocument();
    expect(within(jellyfinRow).getByRole("link", { name: "Jellyfin" })).toHaveAttribute(
      "href",
      "/apps/jellyfin",
    );

    const embyRow = document.querySelector('[data-app-id="emby"]') as HTMLElement;
    expect(within(embyRow).getByText("Off")).toBeInTheDocument();
    expect(within(embyRow).getByRole("link", { name: "Emby" })).toHaveAttribute(
      "href",
      "/apps/emby",
    );
    cleanup();
    const frontends = (await apiFetch("/frontends")) as HubFrontendInfo[];
    render(
      <MemoryRouter>
        <ServerAddressPanel frontends={frontends} />
      </MemoryRouter>,
    );
    expect(await screen.findByRole("heading", { name: "Server address" })).toBeInTheDocument();
    expect(document.querySelector('[data-setting="server-port"]')).toHaveTextContent("32400");
    expect(screen.queryByRole("spinbutton", { name: "Port" })).not.toBeInTheDocument();
    expect(screen.getAllByText("http://192.168.1.2:32400").length).toBeGreaterThan(0);
    expect(screen.getByText("http://100.64.0.2:32400")).toBeInTheDocument();
    expect(screen.getByText("https://203-0-113-7.abc.plex.direct:32400")).toBeInTheDocument();

    const remoteSwitch = screen.getByRole("switch", {
      name: "Open this console from outside your network",
    });
    expect(remoteSwitch).not.toBeChecked();
    fireEvent.click(remoteSwitch);
    await waitFor(() =>
      expect(apiFetch).toHaveBeenCalledWith("/config/server", {
        method: "PUT",
        body: JSON.stringify({ allowRemoteConsole: true }),
      }),
    );
  });

  it("surfaces port conflicts on the Overview header chip, app row, and app page", async () => {
    apiFetch.mockImplementation(async (path: string) => {
      if (path === "/config") return defaultConfig();
      if (path === "/frontends") {
        return [
          row("jellyfin", "Jellyfin", true, {
            status: {
              addresses: ["127.0.0.1:8098"],
              connectUrls: ["http://192.168.1.2:8098", "http://192.168.1.2:32400/jellyfin"],
              busyPort: 8096,
              actualPort: 8098,
              lan: "reachable",
              wan: "unknown",
              clients24h: 0,
              clients24hKnown: true,
              liveStreams: 0,
              liveStreamsKnown: true,
              targetVersion: "1",
              conformance: "passing",
            },
          }),
        ];
      }
      if (path === "/frontends/jellyfin/settings") return { port: 8096 };
      throw new Error(`Unexpected request: ${path}`);
    });

    renderApps("/apps");
    const chip = await screen.findByTestId("overview-port-conflict-chip");
    expect(chip).toHaveTextContent("Port conflict · Jellyfin using 8098 (8096 in use)");
    expect(
      screen.getByText("Using 8098: port 8096 is taken by another program on this computer"),
    ).toBeInTheDocument();

    fireEvent.click(chip);
    await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/apps/jellyfin"));
    expect(
      screen.getAllByText("Using 8098: port 8096 is taken by another program on this computer")
        .length,
    ).toBeGreaterThanOrEqual(1);
    expect(screen.getByRole("link", { name: "http://192.168.1.2:8098" })).toBeInTheDocument();
  });

  it("edits and saves a dedicated app port on the app page alongside Connect a device", async () => {
    let savedPort = 8096;
    apiFetch.mockImplementation(async (path: string, options?: RequestInit) => {
      if (path === "/config") return defaultConfig();
      if (path === "/frontends" && !options?.method) {
        return [
          row("jellyfin", "Jellyfin", true, {
            status: {
              addresses: [`127.0.0.1:${savedPort}`],
              connectUrls: [
                `http://192.168.68.65:32400/jellyfin`,
                `http://192.168.68.65:${savedPort}`,
                `http://100.65.221.123:${savedPort}`,
                `http://[fd79:f184::1]:${savedPort}`,
              ],
              lan: "reachable",
              wan: "unknown",
              clients24h: 0,
              clients24hKnown: false,
              liveStreams: 0,
              liveStreamsKnown: false,
              targetVersion: "1",
              conformance: "passing",
            },
          }),
        ];
      }
      if (path === "/frontends/jellyfin/settings" && !options?.method) {
        return { serverName: "House", port: savedPort };
      }
      if (path === "/frontends/jellyfin/settings" && options?.method === "PUT") {
        const body = JSON.parse(String(options.body));
        savedPort = body.port;
        return { ok: true };
      }
      throw new Error(`Unexpected request: ${path}`);
    });

    renderApps("/apps/jellyfin");
    expect(
      await screen.findByRole("link", { name: "http://192.168.68.65:8096" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Also reachable at")).toBeInTheDocument();
    expect(screen.getByText("http://192.168.68.65:32400/jellyfin")).toBeInTheDocument();
    expect(screen.getByText("Tailscale")).toBeInTheDocument();
    expect(screen.getByText("LAN IPv6")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Connect a device" })).toBeInTheDocument();
    expect(screen.getByText("Jellyfin, Swiftfin, Streamyfin, Infuse")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Server name" })).not.toBeInTheDocument();

    const portInput = await screen.findByRole("spinbutton", { name: "Port" });
    expect(portInput).toHaveValue(8096);
    fireEvent.change(portInput, { target: { value: "8099" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() =>
      expect(apiFetch).toHaveBeenCalledWith("/frontends/jellyfin/settings", {
        method: "PUT",
        body: JSON.stringify({ port: 8099 }),
      }),
    );
    expect(
      await screen.findByRole("link", { name: "http://192.168.68.65:8099" }),
    ).toBeInTheDocument();
  });

  it("places Watch inside Vela's address card and shows Vela-specific setup steps", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ documents: [] }), { status: 200 }),
    );
    apiFetch.mockImplementation(async (path: string) => {
      if (path === "/config") return defaultConfig();
      if (path === "/frontends") {
        return [
          row("vela", "Vela", true, {
            status: {
              webPlayerReady: true,
              addresses: ["http://192.168.68.65:32550/vela"],
              connectUrls: ["http://192.168.68.65:32550/vela", "http://100.65.221.123:32550/vela"],
              lan: "reachable",
              wan: "unknown",
              clients24h: 0,
              clients24hKnown: true,
              liveStreams: 0,
              liveStreamsKnown: true,
              targetVersion: "1",
              conformance: "passing",
            },
          }),
        ];
      }
      if (path === "/frontends/vela/settings") return { defaultSkinId: "cinematic" };
      if (path === "/vela/skins") return { skins: [] };
      if (path === "/vela/status" || path === "/vela/check") {
        return {
          installed: true,
          installedVersion: "0.4.0",
          availableVersion: "0.4.0",
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
      throw new Error(`Unexpected request: ${path}`);
    });
    renderApps("/apps/vela");
    const watchBtn = await screen.findByRole("button", { name: "Open the web player" });
    expect(watchBtn).toHaveClass("border");
    expect(screen.getAllByRole("button", { name: "Open the web player" })).toHaveLength(1);
    expect(
      screen.getByText("Web browser, Vela for macOS, Windows, iPhone, iPad and Apple TV"),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Swiftfin/)).not.toBeInTheDocument();
    vi.restoreAllMocks();
  });

  it("keeps the previous switch state when enabling fails", async () => {
    apiFetch.mockImplementation(async (path: string, options?: RequestInit) => {
      if (path === "/config") return defaultConfig();
      if (path === "/frontends" && !options?.method) return [row("silo", "Silo", true)];
      if (path === "/frontends/silo/enabled") throw new Error("enable failed");
      throw new Error(`Unexpected request: ${path}`);
    });
    renderApps("/apps");
    const toggle = await screen.findByRole("switch", { name: "Silo enabled" });
    expect(toggle).toBeChecked();
    fireEvent.click(toggle);
    expect(await screen.findByText("enable failed")).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "Silo enabled" })).toBeChecked();
    expect(screen.getByRole("tab", { name: "Silo" })).toBeInTheDocument();
  });

  it("redirects an unknown app route back to the overview", async () => {
    apiFetch.mockImplementation(async (path: string) => {
      if (path === "/config") return defaultConfig();
      if (path === "/frontends") return [row("jellyfin", "Jellyfin", true)];
      throw new Error(`Unexpected request: ${path}`);
    });
    renderApps("/apps/not-an-app");
    await waitFor(() => expect(screen.getByTestId("loc")).toHaveTextContent("/apps/overview"));
  });

  it("edits Vela app name, uploads light and dark wordmarks, updates live preview, and saves trimmed branding", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ documents: [] }), { status: 200 }),
    );
    let savedSettings: Record<string, unknown> = { defaultSkinId: "cinematic" };
    let uploadCall = 0;
    apiFetch.mockImplementation(async (path: string, options?: RequestInit) => {
      if (path === "/config") return defaultConfig();
      if (path === "/frontends" && !options?.method) {
        return [
          row("vela", "Vela", true, {
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
          }),
        ];
      }
      if (path === "/vela/skins" && !options?.method) return { skins: [] };
      if (path === "/frontends/vela/settings" && !options?.method) return savedSettings;
      if (path === "/frontends/vela/settings" && options?.method === "PUT") {
        savedSettings = JSON.parse(String(options.body));
        return { ok: true };
      }
      if (path === "/frontends/vela/wordmark" && options?.method === "POST") {
        uploadCall += 1;
        return uploadCall === 1
          ? { url: "/vela/v0/branding/aaaa.svg", width: 240, height: 48 }
          : { url: "/vela/v0/branding/bbbb.svg", width: 240, height: 48 };
      }
      if (path === "/vela/status" || path === "/vela/check") {
        return {
          installed: true,
          installedVersion: "0.4.0",
          availableVersion: "0.4.0",
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
      throw new Error(`Unexpected request: ${path}`);
    });

    renderApps("/apps/vela");
    const nameInput = await screen.findByLabelText("App name");
    const publishedTitle = screen.getByText("Published addresses");
    const connectHeading = screen.getByRole("heading", { name: "Connect a device" });
    const brandingHeading = screen.getByRole("heading", { name: "Branding" });
    const skinsHeading = screen.getByRole("heading", { name: "Skins" });
    expect(
      Boolean(
        publishedTitle.compareDocumentPosition(connectHeading) & Node.DOCUMENT_POSITION_FOLLOWING,
      ),
    ).toBe(true);
    expect(
      Boolean(
        connectHeading.compareDocumentPosition(brandingHeading) & Node.DOCUMENT_POSITION_FOLLOWING,
      ),
    ).toBe(true);
    expect(
      Boolean(
        connectHeading.compareDocumentPosition(skinsHeading) & Node.DOCUMENT_POSITION_FOLLOWING,
      ),
    ).toBe(true);
    fireEvent.change(nameInput, { target: { value: "  Rialto House  " } });

    const lightPreview = screen.getByTestId("vela-preview-light");
    const darkPreview = screen.getByTestId("vela-preview-dark");
    expect(within(lightPreview).getAllByText("Rialto House")).toHaveLength(2);
    expect(within(darkPreview).getAllByText("Rialto House")).toHaveLength(2);

    const tooLarge = new File([new Uint8Array(513 * 1024)], "huge.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("Upload light wordmark"), {
      target: { files: [tooLarge] },
    });
    expect(await screen.findByText("Wordmark must be 512 KB or smaller.")).toBeInTheDocument();

    const lightSvg = new File(["<svg/>"], "light.svg", { type: "image/svg+xml" });
    fireEvent.change(screen.getByLabelText("Upload light wordmark"), {
      target: { files: [lightSvg] },
    });
    expect(await screen.findByText("240×48 px")).toBeInTheDocument();
    expect(within(lightPreview).getByRole("img", { name: "Rialto House" })).toHaveAttribute(
      "src",
      "/vela/v0/branding/aaaa.svg",
    );
    expect(within(darkPreview).getByRole("img", { name: "Rialto House" })).toHaveAttribute(
      "src",
      "/vela/v0/branding/aaaa.svg",
    );

    const darkSvg = new File(["<svg/>"], "dark.svg", { type: "image/svg+xml" });
    fireEvent.change(screen.getByLabelText("Upload dark wordmark"), {
      target: { files: [darkSvg] },
    });
    await waitFor(() =>
      expect(within(darkPreview).getByRole("img", { name: "Rialto House" })).toHaveAttribute(
        "src",
        "/vela/v0/branding/bbbb.svg",
      ),
    );

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() =>
      expect(savedSettings).toEqual({
        defaultSkinId: "cinematic",
        appName: "Rialto House",
        wordmark: {
          url: "/vela/v0/branding/aaaa.svg",
          width: 240,
          height: 48,
          dark: "/vela/v0/branding/bbbb.svg",
        },
      }),
    );

    fireEvent.click(screen.getByLabelText("Remove dark wordmark"));
    expect(within(darkPreview).getByRole("img", { name: "Rialto House" })).toHaveAttribute(
      "src",
      "/vela/v0/branding/aaaa.svg",
    );
    vi.restoreAllMocks();
  });

  it("renders and saves the Vela Live TV channel preview mode with plain-word labels", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ documents: [] }), { status: 200 }),
    );
    let savedSettings: Record<string, unknown> | undefined;
    let currentSettings: Record<string, unknown> = { defaultSkinId: "cinematic" };
    apiFetch.mockImplementation(async (path: string, options?: RequestInit) => {
      if (path === "/config") return defaultConfig();
      if (path === "/frontends" && !options?.method) {
        return [
          row("vela", "Vela", true, {
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
          }),
        ];
      }
      if (path === "/frontends/vela/settings" && options?.method === "PUT") {
        savedSettings = JSON.parse(String(options.body)) as Record<string, unknown>;
        currentSettings = { ...savedSettings };
        return { ok: true };
      }
      if (path === "/frontends/vela/settings") {
        return currentSettings;
      }
      if (path === "/vela/skins") {
        return { skins: [] };
      }
      if (path === "/vela/install") {
        return {
          installed: true,
          version: "0.2.0",
          channel: "https://example.invalid/channel.json",
          canRollback: false,
          state: "idle",
        };
      }
      if (path === "/web-player") {
        return {
          available: true,
          players: [{ id: "vela", url: "http://192.168.68.65:32550/vela/" }],
        };
      }
      throw new Error(`Unexpected request: ${path}`);
    });

    renderApps("/apps/vela");
    expect(await screen.findByRole("heading", { name: "Live TV" })).toBeInTheDocument();
    const trigger = screen.getByRole("button", { name: "Channel preview" });
    expect(trigger).toHaveTextContent("Play the channel when Live TV opens");

    fireEvent.click(trigger);
    expect(
      await screen.findByRole("option", { name: "Play the channel when Live TV opens" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("option", { name: "Only when you choose Preview" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("option", { name: "Never preview; Watch live still works" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("option", { name: "Only when you choose Preview" }));
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() =>
      expect(savedSettings).toEqual({
        defaultSkinId: "cinematic",
        livePreview: "on-click",
      }),
    );

    fireEvent.click(screen.getByRole("button", { name: "Channel preview" }));
    fireEvent.click(
      await screen.findByRole("option", { name: "Never preview; Watch live still works" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() =>
      expect(savedSettings).toEqual({
        defaultSkinId: "cinematic",
        livePreview: "off",
      }),
    );
    vi.restoreAllMocks();
  });

  it("uploads a Vela square app icon, toggles sign-in methods, and warns when all viewer methods are off", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ documents: [] }), { status: 200 }),
    );
    let savedSettings: Record<string, unknown> = { defaultSkinId: "cinematic" };
    apiFetch.mockImplementation(async (path: string, options?: RequestInit) => {
      if (path === "/config") return defaultConfig();
      if (path === "/frontends" && !options?.method) {
        return [
          row("vela", "Vela", true, {
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
          }),
        ];
      }
      if (path === "/vela/skins" && !options?.method) return { skins: [] };
      if (path === "/frontends/vela/settings" && !options?.method) return savedSettings;
      if (path === "/frontends/vela/settings" && options?.method === "PUT") {
        savedSettings = JSON.parse(String(options.body));
        return { ok: true };
      }
      if (path === "/frontends/vela/icon" && options?.method === "POST") {
        return { url: "/vela/v0/branding/icon-cccc.png", width: 512, height: 512 };
      }
      if (path === "/vela/status" || path === "/vela/check") {
        return {
          installed: true,
          installedVersion: "0.4.0",
          availableVersion: "0.4.0",
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
      throw new Error(`Unexpected request: ${path}`);
    });

    renderApps("/apps/vela");
    const passwordSwitch = (await screen.findByLabelText("Password sign-in")) as HTMLInputElement;
    const plexSwitch = screen.getByLabelText("Plex sign-in") as HTMLInputElement;
    const codeSwitch = screen.getByLabelText("Code sign-in") as HTMLInputElement;

    expect(passwordSwitch.checked).toBe(true);
    expect(plexSwitch.checked).toBe(true);
    expect(codeSwitch.checked).toBe(true);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    // Upload square app icon.
    const iconFile = new File(["png"], "icon.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("Upload app icon"), {
      target: { files: [iconFile] },
    });
    expect(await screen.findByText("512×512 px")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "App icon preview" })).toHaveAttribute(
      "src",
      "/vela/v0/branding/icon-cccc.png",
    );

    // Turning off two methods does not show the all-off warning; turning off all three does.
    fireEvent.click(passwordSwitch);
    fireEvent.click(plexSwitch);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    fireEvent.click(codeSwitch);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "All Vela sign-in methods are off. Viewers cannot sign in to Vela until you turn at least one back on. Your Ether console sign-in is unaffected.",
    );

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() =>
      expect(savedSettings).toEqual({
        defaultSkinId: "cinematic",
        appIcon: {
          url: "/vela/v0/branding/icon-cccc.png",
          width: 512,
          height: 512,
        },
        passwordSignIn: false,
        plexSignIn: false,
        quickConnect: false,
      }),
    );

    // Turning one back on clears the warning, and removing the icon clears appIcon.
    fireEvent.click(codeSwitch);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    fireEvent.click(screen.getByLabelText("Remove app icon"));
    expect(screen.queryByRole("img", { name: "App icon preview" })).not.toBeInTheDocument();
    vi.restoreAllMocks();
  });

  it("classifies observed and published addresses and detects remote access presence", () => {
    const localOnly = classifyAppAddresses(
      {
        connectUrls: ["http://192.168.1.20:8097", "http://192.168.1.20:32400/emby"],
      },
      { preferredPort: "8097" },
    );
    expect(localOnly.primary).toBe("http://192.168.1.20:8097");
    expect(localOnly.hasRemoteAddress).toBe(false);

    const withObservedAndPublished = classifyAppAddresses(
      {
        connectUrls: ["http://192.168.1.20:8096"],
        observedAddresses: ["https://jellyfin.example.com", "https://media.example.com/jellyfin"],
        publishedAddresses: ["https://jellyfin.example.com"],
      },
      { preferredPort: "8096" },
    );
    expect(withObservedAndPublished.primary).toBe("http://192.168.1.20:8096");
    expect(withObservedAndPublished.hasRemoteAddress).toBe(true);
    expect(withObservedAndPublished.others).toEqual([
      { address: "https://jellyfin.example.com", label: "Published" },
      { address: "https://media.example.com/jellyfin", label: "Seen recently" },
    ]);
  });

  it("shows a remote-access hint when only local addresses exist and supports observed suggestions in the Published addresses drawer", async () => {
    let savedSettings: Record<string, unknown> = { port: 8096 };
    apiFetch.mockImplementation(async (path: string, options?: RequestInit) => {
      if (path === "/config") return defaultConfig();
      if (path === "/frontends") {
        return [
          row("jellyfin", "Jellyfin", true, {
            descriptor: { defaultPort: 8096 },
            status: {
              addresses: ["127.0.0.1:8096"],
              connectUrls: ["http://192.168.1.20:8096", "http://192.168.1.20:32400/jellyfin"],
              observedAddresses: [
                "https://jellyfin.example.com",
                "https://media.example.com/jellyfin",
              ],
              publishedAddresses: (savedSettings.publishedAddresses as string[] | undefined) ?? [],
            },
          }),
          row("emby", "Emby", true, {
            descriptor: { defaultPort: 8097 },
            status: {
              addresses: ["127.0.0.1:8097"],
              connectUrls: ["http://192.168.1.20:8097"],
            },
          }),
        ];
      }
      if (path === "/frontends/jellyfin/settings" && !options?.method) return savedSettings;
      if (path === "/frontends/jellyfin/settings" && options?.method === "PUT") {
        savedSettings = JSON.parse(String(options.body));
        return { ok: true };
      }
      if (path === "/frontends/emby/settings" && !options?.method) return { port: 8097 };
      throw new Error(`Unexpected request: ${path}`);
    });

    // Emby has no observed or published remote addresses -> shows LAN address + remote access hint.
    const embyView = renderApps("/apps/emby");
    expect(
      await screen.findByRole("link", { name: "http://192.168.1.20:8097" }),
    ).toBeInTheDocument();
    const remoteHintLink = screen.getByRole("link", { name: "Set up remote access" });
    expect(remoteHintLink).toHaveAttribute("href", "/apps/overview");
    embyView.unmount();

    // Jellyfin has observed addresses -> shows them as "Seen recently" and offers one-click suggestions.
    renderApps("/apps/jellyfin");
    expect(await screen.findByText("https://jellyfin.example.com")).toBeInTheDocument();
    expect(screen.getAllByText("Seen recently")).toHaveLength(2);
    expect(screen.queryByRole("link", { name: "Set up remote access" })).not.toBeInTheDocument();

    // Open the Published addresses drawer.
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    const dialog = await screen.findByRole("dialog", { name: "Published addresses" });

    // Observed addresses are suggested, never applied silently.
    expect(
      within(dialog).getByText(
        "No published addresses yet. Ether will use your local address or the current request.",
      ),
    ).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "https://jellyfin.example.com" }));
    expect(
      within(dialog).getByRole("button", { name: "Remove https://jellyfin.example.com" }),
    ).toBeInTheDocument();

    // Reject loopback addresses with a plain-word validation message.
    const input = within(dialog).getByLabelText("Add an address");
    fireEvent.change(input, { target: { value: "http://localhost:8096" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent(/http:\/\/ or https:\/\//);

    // Add a path-style address and save.
    fireEvent.change(input, { target: { value: "https://example.com/jellyfin/" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Add" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(savedSettings).toEqual({
        port: 8096,
        publishedAddresses: ["https://jellyfin.example.com", "https://example.com/jellyfin"],
      }),
    );
    await waitFor(() =>
      expect(apiBlob).toHaveBeenCalledWith(
        "/ether/api/frontends/qr?url=https%3A%2F%2Fjellyfin.example.com",
        expect.any(AbortSignal),
      ),
    );
  });

  it("configures Vela dedicated port and main-port toggle via the Port drawer", async () => {
    let savedSettings: Record<string, unknown> = { appName: "Vela" };
    let velaPort: number | undefined;
    let serveOnMain = true;

    apiFetch.mockImplementation(async (path: string, options?: RequestInit) => {
      if (path === "/config") return defaultConfig();
      if (path === "/frontends") {
        const urls: string[] = [];
        if (velaPort) urls.push(`http://192.168.1.20:${velaPort}`);
        if (!velaPort || serveOnMain) urls.push("http://192.168.1.20:32400/vela");
        return [
          row("vela", "Vela", true, {
            descriptor: { defaultPort: 8098 },
            status: {
              webPlayerReady: true,
              addresses: velaPort ? [`127.0.0.1:${velaPort}`] : [],
              connectUrls: urls,
            },
          }),
        ];
      }
      if (path === "/frontends/vela/settings" && !options?.method) {
        return {
          ...savedSettings,
          ...(velaPort ? { port: velaPort, serveOnMainPort: serveOnMain } : {}),
        };
      }
      if (path === "/frontends/vela/settings" && options?.method === "PUT") {
        const payload = JSON.parse(String(options.body)) as Record<string, unknown>;
        savedSettings = payload;
        velaPort = typeof payload.port === "number" ? payload.port : undefined;
        serveOnMain = payload.serveOnMainPort !== false;
        return { ok: true };
      }
      if (path === "/vela/status") return { installed: true, version: "1.0.0" };
      if (path === "/vela/skins") return { skins: [] };
      if (path === "/web-player") return { available: true, players: [] };
      throw new Error(`Unexpected request: ${path}`);
    });

    renderApps("/apps/vela");
    fireEvent.click(await screen.findByRole("button", { name: "Set port" }));
    const dialog = await screen.findByRole("dialog", { name: "Vela port" });
    expect(
      within(dialog).getByText(
        "Use this if your reverse proxy gives Vela its own address. Saving switches the listener right away.",
      ),
    ).toBeInTheDocument();

    fireEvent.click(within(dialog).getByRole("switch", { name: "Dedicated port" }));
    const portInput = within(dialog).getByLabelText("Port");
    expect(portInput).toHaveValue(8098);

    // Reject colliding with Ether's main port.
    fireEvent.change(portInput, { target: { value: "32400" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent(/32400/);

    // Set 8098 and disable /vela on Ether's main port.
    fireEvent.change(portInput, { target: { value: "8098" } });
    fireEvent.click(
      within(dialog).getByRole("switch", { name: "Serve /vela on Ether's main port" }),
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(savedSettings).toMatchObject({
        appName: "Vela",
        port: 8098,
        serveOnMainPort: false,
      }),
    );
    expect(
      await screen.findByText(
        "Listening on dedicated port 8098. /vela is turned off on Ether's main port.",
      ),
    ).toBeInTheDocument();
  });

  it("lists and revokes Jellyfin API keys on Apps › Jellyfin", async () => {
    const keys = [
      {
        id: 7,
        appName: "Wizarr",
        createdAt: "2026-11-18T14:20:00Z",
        lastUsed: "2026-11-22T17:45:00Z",
      },
      {
        id: 8,
        appName: "Jellyseerr",
        createdAt: "2026-11-20T09:10:00Z",
      },
    ];
    let revokedId: number | undefined;
    vi.mocked(apiFetch).mockImplementation(async (path, options) => {
      if (path === "/frontends") return [row("jellyfin", "Jellyfin", true)];
      if (path === "/config") return { server: { port: 32400 } };
      if (path === "/frontends/jellyfin/settings") return { port: 8096 };
      if (path === "/frontends/jellyfin/api-keys") return [...keys];
      if (path === "/frontends/jellyfin/api-keys/7" && options?.method === "DELETE") {
        revokedId = 7;
        return { ok: true };
      }
      throw new Error(`Unexpected request: ${path}`);
    });

    renderApps("/apps/jellyfin");
    render(<ConfirmRoot />);
    expect(await screen.findByText("Wizarr")).toBeInTheDocument();
    expect(screen.getByText("Jellyseerr")).toBeInTheDocument();
    expect(screen.getByText(/Last used Never/)).toBeInTheDocument();

    const revokeButtons = screen.getAllByRole("button", { name: "Revoke" });
    fireEvent.click(revokeButtons[0]!);
    const dialog = await screen.findByRole("dialog", { name: "Revoke this API key?" });
    fireEvent.click(within(dialog).getByRole("button", { name: "Revoke" }));

    await waitFor(() => expect(revokedId).toBe(7));
    await waitFor(() => expect(screen.queryByText("Wizarr")).not.toBeInTheDocument());
    expect(screen.getByText("Jellyseerr")).toBeInTheDocument();
  });
});
