import { ConfirmRoot } from "@braedonsaunders/appkit-ui";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { invalidateRegistry } from "../lib/extend";
import { createFakeServer } from "../mock/server";
import { SetupWizard } from "../pages/libraries/SetupWizard";
import Plugins from "../pages/plugins/Plugins";

let fakeServer = createFakeServer({ latencyMs: 0 });
let customIntercept:
  ((url: URL, init?: RequestInit) => Promise<Response | null> | Response | null) | null = null;

function renderPlugins(initialEntry = "/plugins") {
  return render(
    <MemoryRouter initialEntries={[initialEntry]}>
      <ConfirmRoot />
      <Routes>
        <Route path="/plugins/:tab?/*" element={<Plugins />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("Server › Plugins (WS10a)", () => {
  const realFetch = window.fetch.bind(window);

  beforeEach(() => {
    invalidateRegistry();
    customIntercept = null;
    fakeServer = createFakeServer({ latencyMs: 0 });
    window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(
        input instanceof Request ? input.url : String(input),
        window.location.origin,
      );
      if (customIntercept) {
        const res = await customIntercept(url, init);
        if (res) return res;
      }
      if (url.origin === window.location.origin && url.pathname.startsWith("/ether/api/")) {
        return fakeServer.fetch(url.toString(), init);
      }
      return realFetch(input, init);
    };
  });

  afterEach(() => {
    window.fetch = realFetch;
    cleanup();
  });

  it("renders installed plugins with kind, health, version, update, Unverified, and shadow badges and toggles enable/disable", async () => {
    renderPlugins("/plugins");

    expect(await screen.findByText("Pluto TV")).toBeTruthy();
    expect(screen.getByText("Dub language")).toBeTruthy();
    expect(screen.getByText("Ether Wrapped")).toBeTruthy();

    // An installed Stremio add-on is listed with the plugins.
    expect(screen.getByText("Stream Addon")).toBeTruthy();

    expect(screen.getByText("Update v1.3.0")).toBeTruthy();
    expect(
      within(screen.getByRole("article", { name: "Dub language" })).getByText("Unverified"),
    ).toBeVisible();
    expect(screen.getByText("Trying out", { selector: "span *, span" })).toBeTruthy();

    const plutoSwitch = screen.getByRole("switch", { name: "Enable Pluto TV" }) as HTMLInputElement;
    expect(plutoSwitch.checked).toBe(true);

    fireEvent.click(plutoSwitch);
    await waitFor(() => {
      expect(plutoSwitch.checked).toBe(false);
    });

    fireEvent.click(plutoSwitch);
    await waitFor(() => {
      expect(plutoSwitch.checked).toBe(true);
    });
  });

  it("renders the Installed empty state explaining plugins in one sentence and offering the marketplace and Studio", async () => {
    customIntercept = (url, init) => {
      if (url.pathname === "/ether/api/plugins" && (!init?.method || init.method === "GET")) {
        return new Response(JSON.stringify({ plugins: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return null;
    };

    renderPlugins("/plugins");

    expect(await screen.findByText("No plugins installed")).toBeTruthy();
    expect(
      screen.getByText(
        "Plugins add sources, channels, subtitle providers and custom pages to Ether.",
      ),
    ).toBeTruthy();

    expect(screen.getByRole("button", { name: "Open the marketplace" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Add" })).toBeTruthy();
    expect(screen.getByRole("link", { name: /Build a plugin/i })).toBeTruthy();
  });

  it("opens the plugin drawer with Overview, what it adds, Settings and Activity, and keeps updates, undo and removal in the header", async () => {
    renderPlugins("/plugins");

    fireEvent.click(await screen.findByText("Pluto TV"));

    // Header and tabs: three tabs plus one named for what the plugin adds.
    const tabs = await screen.findByRole("tablist", { name: "Plugin sections" });
    expect(
      within(tabs)
        .getAllByRole("tab")
        .map((t) => t.textContent),
    ).toEqual(["Overview", "Channels", "Settings", "Activity"]);

    // Health tiles open the Activity tab, not the Overview.
    expect(screen.queryByText("Responding")).toBeNull();
    fireEvent.click(within(tabs).getByRole("tab", { name: "Activity" }));
    expect(await screen.findByText("Responding")).toBeTruthy();
    expect(screen.getByText("180 ms")).toBeTruthy();
    expect(screen.getByText("Slowest 720 ms")).toBeTruthy();

    // Update banner: new access is approved in the confirm step.
    expect(screen.getByText("Version 1.3.0 is available and asks for new access.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Update" }));
    fireEvent.click(await screen.findByRole("button", { name: "Allow and update" }));
    await waitFor(() =>
      expect(screen.queryByText("Version 1.3.0 is available and asks for new access.")).toBeNull(),
    );

    // Setup: settings with validation and Test.
    fireEvent.click(within(tabs).getByRole("tab", { name: "Settings" }));
    expect(await screen.findByText("Region")).toBeTruthy();
    expect(
      screen.getByText("Saved. Leave as is to keep it, or type a new value to replace it."),
    ).toBeTruthy();
    customIntercept = (url, init) => {
      if (
        url.pathname === "/ether/api/plugins/community.plutotv.ether/test" &&
        init?.method === "POST"
      ) {
        return new Response(
          JSON.stringify({
            ok: true,
            message: "No account configured: anonymous downloads are rate-limited.",
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      return null;
    };
    fireEvent.click(screen.getByRole("button", { name: /Test/i }));
    expect(await screen.findByText(/OK · \d+ ms/)).toBeTruthy();
    expect(
      screen.getByText("No account configured: anonymous downloads are rate-limited."),
    ).toBeTruthy();
    customIntercept = null;

    customIntercept = (url, init) => {
      if (
        url.pathname === "/ether/api/plugins/community.plutotv.ether/settings" &&
        init?.method === "PUT"
      ) {
        return fakeServer.fetch(url.toString(), {
          ...init,
          body: JSON.stringify({ settings: { region: "invalid", token: "Saved" } }),
        });
      }
      return null;
    };
    fireEvent.click(screen.getByRole("button", { name: /Save settings/i }));
    expect(
      (await screen.findAllByText(/This value is not supported\./)).length,
    ).toBeGreaterThanOrEqual(1);
    customIntercept = null;
    fireEvent.click(screen.getByRole("button", { name: /Save settings/i }));
    await waitFor(() => {
      expect(screen.queryAllByText(/This value is not supported\./)).toHaveLength(0);
    });

    // More options: auto-update and network access, out of the way.
    fireEvent.click(screen.getByRole("button", { name: "More options" }));
    const autoUpdate = screen.getByRole("switch", { name: "Auto-update" }) as HTMLInputElement;
    const before = autoUpdate.checked;
    fireEvent.click(autoUpdate);
    await waitFor(() => expect(autoUpdate.checked).toBe(!before));
    const network = screen.getByRole("switch", {
      name: "Allow: Connects to api.pluto.tv, *.pluto.tv",
    }) as HTMLInputElement;
    expect(network.checked).toBe(true);
    fireEvent.click(network);
    await waitFor(() => expect(network.checked).toBe(false));

    // The ⋯ menu offers roll back.
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    expect(await screen.findByRole("menuitem", { name: /Roll back to v/ })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));

    // Activity on Ether Wrapped: changes undo back to a point.
    fireEvent.click(screen.getByText("Ether Wrapped"));
    fireEvent.click(await screen.findByRole("tab", { name: "Activity" }));
    expect(await screen.findByText("Changes it made")).toBeTruthy();
    expect(screen.getAllByText(/Your year in viewing/).length).toBeGreaterThanOrEqual(1);
    fireEvent.click(screen.getAllByRole("button", { name: /Undo back to here/ })[0]!);
    fireEvent.click(await screen.findByRole("button", { name: "Undo" }));
    expect((await screen.findAllByText("Undone")).length).toBeGreaterThanOrEqual(1);

    // Removal lives in the ⋯ menu behind a confirm dialog.
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Remove/ }));
    expect(await screen.findByText("Remove Ether Wrapped?")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /^Remove$/ }));
    await waitFor(() => expect(screen.queryByText("Changes it made")).toBeNull());
  });

  it("installs a plugin from a link after previewing its permission sheet and opens the installed drawer", async () => {
    renderPlugins("/plugins");
    await screen.findByText("Pluto TV");

    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /From a link/i }));
    const urlInput = await screen.findByLabelText("Plugin address");
    fireEvent.change(urlInput, {
      target: { value: "https://plugins.example.test/iptv/manifest.json" },
    });

    fireEvent.click(screen.getByRole("button", { name: /Review permissions/i }));

    // PermissionSheet appears with Unverified warning and plain-language permissions
    const sheet = await screen.findByTestId("permission-sheet");
    expect(within(sheet).getByText("Not reviewed by Ether")).toBeTruthy();
    expect(within(sheet).getByText("Connects to plugins.example.test")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /^Install$/i }));

    // Installing closes InstallDrawer, adds Preview plugin to the list, and opens its drawer
    expect(await screen.findByRole("tablist", { name: "Plugin sections" })).toBeTruthy();
    expect(screen.getAllByText("Preview plugin").length).toBeGreaterThanOrEqual(1);
  });

  it("shows blocked local network hosts with an Allow button and lets the owner list, add, and remove local hosts in Settings", async () => {
    let blocked = ["tuliprox:8901"];
    const savedBodies: { localHosts?: string[] }[] = [];
    customIntercept = async (url, init) => {
      const base = "/ether/api/plugins/community.plutotv.ether";
      if (url.pathname === base && (!init?.method || init.method === "GET")) {
        const response = await fakeServer.fetch(url.toString(), init);
        const detail = await response.json();
        detail.blockedLocalHosts = blocked;
        return Response.json(detail);
      }
      if (url.pathname === `${base}/grants` && init?.method === "PUT") {
        const body = JSON.parse(String(init.body ?? "{}")) as { localHosts?: string[] };
        savedBodies.push(body);
        if (body.localHosts) {
          const allowed = new Set(body.localHosts.map((h) => h.toLowerCase()));
          blocked = blocked.filter(
            (h) => !allowed.has(h.toLowerCase()) && !allowed.has(h.toLowerCase().split(":")[0]!),
          );
        }
      }
      if (url.pathname === "/ether/api/plugins/install/preview" && init?.method === "POST") {
        const response = await fakeServer.fetch(url.toString(), init);
        const preview = await response.json();
        preview.permissions = [...preview.permissions, "May reach on your network: tuliprox:8901"];
        return Response.json(preview);
      }
      return null;
    };

    renderPlugins("/plugins");
    fireEvent.click(await screen.findByText("Pluto TV"));

    // Blocked private host surfaces right in the drawer with a one-click Allow button.
    expect(await screen.findByText("Blocked tuliprox:8901 (on your network)")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Allow" }));

    await waitFor(() => {
      expect(screen.queryByText("Blocked tuliprox:8901 (on your network)")).toBeNull();
    });
    expect(savedBodies.at(-1)?.localHosts).toEqual(["tuliprox:8901"]);

    // Settings -> "On your network": list, add, remove.
    const tabs = screen.getByRole("tablist", { name: "Plugin sections" });
    fireEvent.click(within(tabs).getByRole("tab", { name: "Settings" }));

    expect(await screen.findByText("On your network")).toBeTruthy();
    expect(
      screen.getByText(
        "Lets this plugin reach these addresses on your network, such as tuliprox or 192.168.1.20.",
      ),
    ).toBeTruthy();

    const allowedList = screen.getByRole("list", { name: "Allowed network addresses" });
    expect(within(allowedList).getByText("tuliprox:8901")).toBeTruthy();

    const hostInput = screen.getByLabelText("Network address");
    fireEvent.change(hostInput, { target: { value: "192.168.1.20" } });
    fireEvent.keyDown(hostInput, { key: "Enter" });

    await waitFor(() => {
      expect(within(allowedList).getByText("192.168.1.20")).toBeTruthy();
    });
    expect(savedBodies.at(-1)?.localHosts).toEqual(["tuliprox:8901", "192.168.1.20"]);

    fireEvent.click(screen.getByRole("button", { name: "Remove tuliprox:8901" }));
    await waitFor(() => {
      expect(within(allowedList).queryByText("tuliprox:8901")).toBeNull();
    });
    expect(savedBodies.at(-1)?.localHosts).toEqual(["192.168.1.20"]);

    // Also verify the install permission sheet renders "May reach on your network: …".
    cleanup();
    renderPlugins("/plugins");
    await screen.findByText("Pluto TV");
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /From a link/i }));
    fireEvent.change(await screen.findByLabelText("Plugin address"), {
      target: { value: "https://plugins.example.test/iptv/manifest.json" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Review permissions/i }));
    const sheet = await screen.findByTestId("permission-sheet");
    expect(within(sheet).getByText("May reach on your network: tuliprox:8901")).toBeTruthy();
  });

  it("browses, filters, refreshes, and installs from the Marketplace tab using the real catalogue contract", async () => {
    let refreshed = false;
    let installBody: Record<string, unknown> | null = null;
    customIntercept = async (url, init) => {
      if (url.pathname === "/ether/api/plugins/catalogue/refresh" && init?.method === "POST") {
        refreshed = true;
      }
      if (url.pathname === "/ether/api/plugins/install" && init?.method === "POST") {
        installBody = JSON.parse(String(init.body ?? "{}")) as Record<string, unknown>;
      }
      return null;
    };

    renderPlugins("/plugins/marketplace");

    expect(await screen.findByText("SubDL Subtitles")).toBeTruthy();
    expect(screen.getByText("Local folders")).toBeTruthy();
    expect(screen.getByText("Pluto TV")).toBeTruthy();

    // Every catalogue entry has a Reviewed badge, and Pluto TV shows Installed + Update available.
    expect(screen.getAllByText("Reviewed").length).toBeGreaterThanOrEqual(4);
    expect(screen.getByText("Update available")).toBeTruthy();

    // Submit link and Refresh action live in the section header.
    const submitLink = screen.getByRole("link", { name: /Submit a plugin/i });
    expect(submitLink.getAttribute("href")).toBe(
      "https://github.com/braedonsaunders/ether-plugins#submit-a-plugin",
    );
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(refreshed).toBe(true));

    // Real kind filters (no fake webhook/folder/composite filters).
    const kindGroup = screen.getByRole("group", { name: "Filter by kind" });
    expect(
      within(kindGroup)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual([
      "All kinds",
      "Source",
      "Integration",
      "Notification",
      "Live TV",
      "Extension",
      "Content",
    ]);

    // Search filter
    const searchInput = screen.getByLabelText("Search catalogue");
    fireEvent.change(searchInput, { target: { value: "SubDL" } });
    expect(screen.getByText("SubDL Subtitles")).toBeTruthy();
    expect(screen.queryByText("Local folders")).toBeNull();

    // Open SubDL Subtitles card drawer and install via {kind:"catalogue", id}
    fireEvent.click(screen.getByText("SubDL Subtitles"));
    const sheet = await screen.findByTestId("permission-sheet");
    expect(within(sheet).getByText("Connects to api.subdl.com")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /^Install$/i }));

    // Navigates to Installed tab and opens the new plugin's drawer
    expect(await screen.findByRole("tablist", { name: "Plugin sections" })).toBeTruthy();
    expect(installBody).toMatchObject({
      kind: "catalogue",
      id: "community.subdl.subtitles",
    });
  });

  it("pairs and revokes a remote sidecar", async () => {
    let revoked = false;
    customIntercept = async (url, init) => {
      const base = "/ether/api/plugins/community.plutotv.ether";
      if (url.pathname === base && (!init?.method || init.method === "GET")) {
        const response = await fakeServer.fetch(url.toString(), init);
        const detail = await response.json();
        detail.manifest.ether.runtime = { kind: "remote" };
        return Response.json(detail);
      }
      if (url.pathname === `${base}/pairings` && (!init?.method || init.method === "GET")) {
        return Response.json({
          pairings: revoked
            ? []
            : [{ id: "sidecar-1", created: 1700000000000, lastUsed: 0, expires: 1900000000000 }],
        });
      }
      if (url.pathname === `${base}/pairings/sidecar-1` && init?.method === "DELETE") {
        revoked = true;
        return Response.json({ status: "revoked" });
      }
      return null;
    };
    renderPlugins();
    fireEvent.click(await screen.findByText("Pluto TV"));
    fireEvent.click(await screen.findByRole("button", { name: "Connect a sidecar" }));
    expect(await screen.findByText(/PAIR-1234/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
    expect(await screen.findByText("No sidecars connected.")).toBeTruthy();
  });

  it("shows 'No plugins in the marketplace yet' with a submit link when configured with zero entries, and unavailable only when unconfigured or unreachable", async () => {
    let mode: "empty" | "unconfigured" | "unreachable" = "empty";
    customIntercept = (url) => {
      if (url.pathname === "/ether/api/plugins/catalogue") {
        if (mode === "empty") {
          return Response.json({ entries: [], configured: true, fetchedAt: 1700000000 });
        }
        if (mode === "unconfigured") {
          return Response.json({ entries: [], configured: false, fetchedAt: 0 });
        }
        return new Response(JSON.stringify({ error: { message: "bad gateway" } }), {
          status: 502,
          headers: { "Content-Type": "application/json" },
        });
      }
      return null;
    };

    renderPlugins("/plugins/marketplace");
    expect(await screen.findByText("No plugins in the marketplace yet")).toBeTruthy();
    expect(screen.getAllByRole("link", { name: /Submit a plugin/i }).length).toBeGreaterThanOrEqual(
      1,
    );
    expect(screen.queryByText(/not available/i)).toBeNull();
    cleanup();

    mode = "unconfigured";
    renderPlugins("/plugins/marketplace");
    expect(await screen.findByText("No marketplace configured")).toBeTruthy();
    cleanup();

    mode = "unreachable";
    renderPlugins("/plugins/marketplace");
    expect(await screen.findByText("Could not reach the marketplace")).toBeTruthy();
  });

  it("replaces card Open in Studio with Configure, keeps Open in Studio in ⋯, moves Limits behind More options, and avoids sliced initials on missing icons", async () => {
    customIntercept = async (url, init) => {
      if (url.pathname === "/ether/api/plugins" && (!init?.method || init.method === "GET")) {
        const res = await fakeServer.fetch(url.toString(), init);
        const data = (await res.json()) as { plugins: Record<string, unknown>[] };
        data.plugins = data.plugins.map((p) =>
          p.id === "community.plutotv.ether"
            ? { ...p, icon: "https://broken.example.invalid/404.png" }
            : p,
        );
        return Response.json(data);
      }
      return null;
    };

    renderPlugins("/plugins");
    const plutoCard = await screen.findByRole("article", { name: "Pluto TV" });
    const dubCard = screen.getByRole("article", { name: "Dub language" });

    // Card faces show Configure, never Open in Studio.
    expect(screen.queryByRole("button", { name: /Open in Studio/i })).toBeNull();
    expect(within(plutoCard).getByRole("button", { name: "Configure" })).toBeTruthy();
    const dubConfigure = within(dubCard).getByRole("button", { name: "Configure" });

    // A 404 icon falls back to the centred glyph without two-letter initials ("PT"), and cards without icons show no initials ("DL").
    const brokenImg = plutoCard.querySelector("img");
    expect(brokenImg).not.toBeNull();
    fireEvent.error(brokenImg!);
    expect(within(plutoCard).queryByText("PT")).toBeNull();
    expect(within(dubCard).queryByText("DL")).toBeNull();

    // Clicking Configure on a hosted plugin opens its drawer; Overview has no Limits table; Open in Studio lives in ⋯.
    fireEvent.click(dubConfigure);
    const tabs = await screen.findByRole("tablist", { name: "Plugin sections" });
    expect(screen.queryByRole("button", { name: "Open in Studio" })).toBeNull();
    expect(screen.queryByText("Memory (MiB)")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    expect(await screen.findByRole("menuitem", { name: "Open in Studio" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));

    // Limits appear in Settings only after opening More options.
    fireEvent.click(within(tabs).getByRole("tab", { name: "Settings" }));
    expect(await screen.findByRole("button", { name: "More options" })).toBeTruthy();
    expect(screen.queryByText("Memory (MiB)")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "More options" }));
    expect(await screen.findByText("Memory (MiB)")).toBeTruthy();

    // Marketplace cards without icons show no two-letter initials.
    cleanup();
    customIntercept = null;
    renderPlugins("/plugins/marketplace");
    expect(await screen.findByText("Local folders")).toBeTruthy();
    expect(screen.queryByText("LF")).toBeNull();
  });

  it("preserves passing setup step details per step in SetupWizard", async () => {
    let stepStatus: "pending" | "ok" = "pending";
    let stepDetail = "";
    customIntercept = (url, init) => {
      if (url.pathname === "/ether/api/plugins-setup/community.archive.ether") {
        return new Response(
          JSON.stringify({
            pluginId: "community.archive.ether",
            pluginName: "Archive",
            schema: { fields: [{ key: "email", label: "Email" }] },
            settings: {},
            blockers: stepStatus === "ok" ? [] : ["account"],
            steps: [
              {
                key: "account",
                label: "Account",
                required: true,
                fields: ["email"],
                test: "/ether/v1/setup/account/test",
                status: stepStatus,
                detail: stepDetail,
              },
              {
                key: "catalogue",
                label: "Catalogue",
                required: false,
                fields: [],
                status: "pending",
              },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      if (
        url.pathname === "/ether/api/plugins/community.archive.ether/settings" &&
        init?.method === "PUT"
      ) {
        return new Response(JSON.stringify({ settings: {} }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (
        url.pathname === "/ether/api/plugins-setup/community.archive.ether/account/test" &&
        init?.method === "POST"
      ) {
        stepStatus = "ok";
        stepDetail = "Anonymous access works and is rate-limited.";
        return new Response(JSON.stringify({ ok: true, detail: stepDetail }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return null;
    };

    render(
      <MemoryRouter>
        <SetupWizard pluginId="community.archive.ether" onClose={() => {}} />
      </MemoryRouter>,
    );

    fireEvent.click(await screen.findByRole("button", { name: "Save and test" }));
    // Advances to step 2 without leaking step 1's detail onto step 2.
    expect(await screen.findByRole("heading", { name: "Catalogue" })).toBeTruthy();
    expect(screen.queryByText("Anonymous access works and is rate-limited.")).toBeNull();

    // Selecting step 1 shows its passing test detail.
    fireEvent.click(screen.getByRole("button", { name: /Account/ }));
    expect(await screen.findByText("Anonymous access works and is rate-limited.")).toBeTruthy();
  });

  it("reviews, approves, and revokes operator-configured server addresses at install and in plugin settings", async () => {
    renderPlugins("/plugins");
    await screen.findByText("Pluto TV");

    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /From a link/i }));
    fireEvent.change(await screen.findByLabelText("Plugin address"), {
      target: { value: "https://plugins.example.test/operator-default/manifest.json" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Review permissions/i }));

    const sheet = await screen.findByTestId("permission-sheet");
    expect(within(sheet).getByText("Connects to addresses you approve")).toBeTruthy();
    expect(
      within(sheet).getByText("Needs approval to connect to library.example.com"),
    ).toBeTruthy();

    // Wildcard input is ignored in the address picker.
    const addrInput = within(sheet).getByLabelText("Server address");
    fireEvent.change(addrInput, { target: { value: "*" } });
    fireEvent.click(within(sheet).getByRole("button", { name: "Approve address" }));
    expect(within(sheet).queryByLabelText("Approved addresses")).toBeNull();

    // Approve the pending default address during install review.
    fireEvent.click(within(sheet).getByRole("button", { name: "Approve" }));
    const approvedAtInstall = within(sheet).getByRole("list", { name: "Approved addresses" });
    expect(within(approvedAtInstall).getByText("library.example.com")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: /^Install$/i }));

    // Installed drawer opens for Self-hosted connector.
    const tabs = await screen.findByRole("tablist", { name: "Plugin sections" });
    fireEvent.click(within(tabs).getByRole("tab", { name: "Settings" }));

    // Save a new server URL in settings -> prompts for approval immediately.
    const serverUrlInput = await screen.findByLabelText("Server URL");
    fireEvent.change(serverUrlInput, {
      target: { value: "https://books.example.com:8443/api" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save settings" }));

    expect(
      await screen.findByText("Needs approval to connect to books.example.com:8443"),
    ).toBeTruthy();

    // Overview tab also shows the approval banner until approved.
    fireEvent.click(within(tabs).getByRole("tab", { name: "Overview" }));
    expect(
      await screen.findByText("Needs approval to connect to books.example.com:8443"),
    ).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await waitFor(() => {
      expect(screen.queryByText("Needs approval to connect to books.example.com:8443")).toBeNull();
    });

    // Back in Settings: both addresses are listed and revocable.
    fireEvent.click(within(tabs).getByRole("tab", { name: "Settings" }));
    const approvedList = await screen.findByRole("list", { name: "Approved addresses" });
    expect(within(approvedList).getByText("library.example.com")).toBeTruthy();
    expect(within(approvedList).getByText("books.example.com:8443")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Revoke library.example.com" }));
    await waitFor(() => {
      expect(within(approvedList).queryByText("library.example.com")).toBeNull();
    });
  });
});
