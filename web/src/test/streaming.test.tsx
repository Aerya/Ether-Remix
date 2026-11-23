import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { decisionErrorSummary } from "../lib/quality-fallback";
import type { EtherConfig, UsenetProvider } from "../lib/types";
import {
  choiceLabel,
  normalizeTraces,
  resolutionCeilings,
  TraceDetail,
} from "../pages/releases/History";
import { calls, renderApp } from "./harness";

const WAIT = { timeout: 4000 };
async function choose(label: string, option: string) {
  fireEvent.click(screen.getByRole("button", { name: label }));
  fireEvent.click(await screen.findByRole("option", { name: option }));
}

async function pick(scope: HTMLElement, label: string, ...options: string[]) {
  fireEvent.click(within(scope).getByRole("button", { name: label }));
  for (const option of options) {
    fireEvent.click(await screen.findByRole("option", { name: option, hidden: true }));
  }
}

describe("Sources", () => {
  it("enables bounded repair without changing provider credentials", async () => {
    const { fake } = renderApp("/sources/usenet");
    fireEvent.click(await screen.findByRole("button", { name: "Configure repair" }, WAIT));
    const enabled = await screen.findByRole("switch", { name: "Enable repair" });
    expect(enabled).not.toBeChecked();
    fireEvent.click(enabled);
    fireEvent.change(screen.getByLabelText("Extra download budget"), { target: { value: "256" } });
    fireEvent.click(screen.getByRole("button", { name: "Save repair settings" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/usenet")).toHaveLength(1), WAIT);
    const body = calls(fake, "PUT", "/config/usenet")[0]!.body as EtherConfig["usenet"];
    expect(body.repair).toMatchObject({ enabled: true, maxDownloadBytes: 256 * 1024 * 1024 });
    expect(body.providers).toHaveLength(2);
    expect(body.providers?.[0]?.password).toBe("••••");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("discards unsaved provider edits when its drawer closes", async () => {
    const { fake } = renderApp("/sources/usenet");
    fireEvent.click(await screen.findByRole("button", { name: "Edit Primary news" }, WAIT));
    fireEvent.change(screen.getByLabelText("Host"), { target: { value: "draft.example" } });
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Edit Primary news" }));
    expect(await screen.findByLabelText("Host")).toHaveValue("news.example.com");
    expect(calls(fake, "PUT", "/config/usenet")).toHaveLength(0);
  });

  it("saves one provider from its drawer and keeps redacted secrets", async () => {
    const { fake } = renderApp("/sources/usenet");
    fireEvent.click(await screen.findByRole("button", { name: "Edit Primary news" }, WAIT));
    fireEvent.change(screen.getByLabelText("Host"), { target: { value: "eu.news.example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/usenet")).toHaveLength(1), WAIT);
    const body = calls(fake, "PUT", "/config/usenet")[0]!.body as { providers: UsenetProvider[] };
    expect(body.providers[0]).toMatchObject({
      host: "eu.news.example.com",
      password: "••••",
      priority: 0,
    });
    expect(body.providers[1]).toMatchObject({ password: "••••", priority: 1, backfill: true });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("persists provider removal from its drawer and retains remaining credentials", async () => {
    const { fake } = renderApp("/sources/usenet");
    fireEvent.click(await screen.findByRole("button", { name: "Edit Primary news" }, WAIT));
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    fireEvent.click(
      within(await screen.findByRole("dialog", { name: "Remove provider?" })).getByRole("button", {
        name: "Remove",
      }),
    );
    await waitFor(() => expect(calls(fake, "PUT", "/config/usenet")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/usenet")[0]!.body).toMatchObject({
      providers: [{ id: "p2", priority: 0, password: "••••" }],
    });
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Edit Primary news" })).not.toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: "Edit Block account" })).toBeInTheDocument();
  });

  it("reorders providers and saves the new priorities", async () => {
    const { fake } = renderApp("/sources/usenet");
    fireEvent.click(await screen.findByRole("button", { name: "Move Block account up" }, WAIT));
    await waitFor(() => expect(calls(fake, "PUT", "/config/usenet")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/usenet")[0]!.body).toMatchObject({
      providers: [
        { id: "p2", priority: 0 },
        { id: "p1", priority: 1 },
      ],
    });
  });

  it("imports indexers from Prowlarr in a drawer", async () => {
    renderApp("/sources/indexers");
    expect(
      await screen.findByRole("button", { name: "Edit Indexer One" }, WAIT),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Add a source/ }));
    fireEvent.click(await screen.findByRole("button", { name: /^Prowlarr/ }));
    const drawer = await screen.findByRole("dialog", { name: "Import from Prowlarr" });
    expect(within(drawer).getByLabelText("Prowlarr URL")).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: "Preview indexers" })).toBeDisabled();
  });
});

describe("Playback devices", () => {
  it("identifies copied app edits and saves only the edited app", async () => {
    const shared = { product: "Plex for Android", productMatch: "contains", maxHeight: 1080 };
    const phone = {
      product: "Plex for Android (Mobile)",
      productExact: true,
      copiedFrom: shared.product,
      maxHeight: 1080,
    };
    const tv = {
      product: "Plex for Android (TV)",
      productExact: true,
      copiedFrom: shared.product,
      maxHeight: 1080,
    };
    const { fake } = renderApp("/playback/devices", { capabilityOverrides: [shared, phone, tv] });
    const phoneButton = await screen.findByRole(
      "button",
      { name: "Edit Plex for Android phones" },
      WAIT,
    );
    const row = phoneButton.closest("tr")!;
    expect(within(row).getByText("Override")).toBeInTheDocument();
    expect(within(row).getByText("Copied from a shared setting")).toHaveAttribute(
      "title",
      "Plex for Android",
    );
    fireEvent.click(phoneButton);
    const drawer = await screen.findByRole("dialog", { name: "Edit Plex for Android phones" });
    fireEvent.click(within(drawer).getByRole("button", { name: "Highest resolution it can show" }));
    fireEvent.click(await screen.findByRole("option", { name: "4K" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/clients")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/clients")[0]!.body).toEqual({
      overrides: [shared, tv, { ...phone, maxHeight: 2160 }],
    });
  });

  it("saves the exact product of the edited app", async () => {
    const { fake } = renderApp("/playback/devices");
    fireEvent.click(
      await screen.findByRole("button", { name: "Edit Plex for Android phones" }, WAIT),
    );
    const drawer = await screen.findByRole("dialog", { name: "Edit Plex for Android phones" });
    fireEvent.click(within(drawer).getByRole("button", { name: "Highest resolution it can show" }));
    fireEvent.click(await screen.findByRole("option", { name: "4K" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/clients")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/clients")[0]!.body).toEqual({
      overrides: [{ product: "Plex for Android (Mobile)", productExact: true, maxHeight: 2160 }],
    });
  });

  it("filters app compatibility across Apps, Device profiles, and Seen devices", async () => {
    const { fake } = renderApp("/playback/devices");
    expect(await screen.findByText("Plezy", {}, WAIT)).toBeInTheDocument();
    expect(screen.getByText("Plex Web")).toBeInTheDocument();
    expect(screen.getByText("Jellyfin TV")).toBeInTheDocument();
    expect(screen.getByText("Swiftfin")).toBeInTheDocument();
    expect(screen.getByText("VLC")).toBeInTheDocument();
    expect(screen.getAllByText("Built in").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Reports its own").length).toBeGreaterThan(0);
    expect(screen.queryByRole("heading", { name: "Seen devices" })).not.toBeInTheDocument();

    const plezyRow = screen.getByRole("button", { name: "Edit Plezy" }).closest("tr")!;
    expect(within(plezyRow).getByText("4K")).toBeInTheDocument();
    expect(within(plezyRow).getByText("H.265/HEVC")).toBeInTheDocument();
    expect(within(plezyRow).getByText("HDR")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Protocol" }));
    fireEvent.click(
      within(await screen.findByRole("listbox", { hidden: true })).getByText("Jellyfin"),
    );
    expect(screen.queryByText("Plex Web")).not.toBeInTheDocument();
    expect(screen.getByText("Jellyfin TV")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Device" }));
    fireEvent.click(
      within(await screen.findByRole("listbox", { hidden: true })).getByText("Apple TV"),
    );
    expect(screen.queryByText("Jellyfin TV")).not.toBeInTheDocument();
    expect(screen.getByText("Swiftfin")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Device profiles" }));
    expect((await screen.findAllByText(/Dolby Vision 5/, {}, WAIT)).length).toBeGreaterThan(0);
    expect(screen.getAllByText("H.265/HEVC").length).toBeGreaterThan(0);
    expect(screen.queryByText("Living room TV")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Seen devices" }));
    expect(await screen.findByText("Living room TV", {}, WAIT)).toBeInTheDocument();
    const seenRow = screen.getByText("Living room TV").closest("tr")!;
    fireEvent.click(within(seenRow).getByRole("button", { name: "Create override" }));
    const drawer = await screen.findByRole("dialog", { name: "Edit Living room TV" });
    expect(within(drawer).getByLabelText("Match")).toHaveValue("Xiaomi TV Box");
    expect(within(drawer).getByLabelText("Platform")).toHaveValue("Android");
    expect(
      within(drawer).getByRole("switch", {
        name: "Use these settings even when the app reports otherwise",
      }),
    ).toBeInTheDocument();
    fireEvent.click(within(drawer).getByRole("button", { name: "Highest resolution it can show" }));
    fireEvent.click(await screen.findByRole("option", { name: "4K" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/clients")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/clients")[0]!.body).toEqual({
      overrides: [{ device: "Xiaomi TV Box", platform: "Android", maxHeight: 2160 }],
    });
  });

  it("saves an app compatibility override from a drawer", async () => {
    const { fake } = renderApp("/playback/devices");
    fireEvent.click(await screen.findByRole("tab", { name: "Device profiles" }, WAIT));
    fireEvent.click(await screen.findByRole("button", { name: "Edit Apple TV" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "Edit Apple TV" });
    expect(within(drawer).getAllByText("Profile 5").length).toBeGreaterThan(0);
    expect(within(drawer).getAllByText("Built in: 4K").length).toBeGreaterThan(0);
    // The built-in profiles start ticked; adding one keeps them.
    await pick(drawer, "Dolby Vision profiles", "Profile 7");
    fireEvent.click(within(drawer).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/clients")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/clients")[0]!.body).toMatchObject({
      overrides: [{ id: "apple-tv", dolbyVisionProfiles: [5, 8, 7] }],
    });
  });

  it("adds an app override from a typed match", async () => {
    const { fake } = renderApp("/playback/devices");
    fireEvent.click(await screen.findByRole("button", { name: "Add" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "Add an app" });
    fireEvent.change(within(drawer).getByLabelText("Match"), { target: { value: "Infuse" } });
    await pick(drawer, "Video codecs", "H.265/HEVC");
    fireEvent.click(within(drawer).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/clients")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/clients")[0]!.body).toMatchObject({
      overrides: [{ product: "Infuse", productExact: true, videoCodecs: ["hevc"] }],
    });
  });

  it("keeps seen devices distinct, renders empty lastSeen cleanly, and uses compact override buttons", async () => {
    renderApp("/playback/devices/seen", {}, (fake) => {
      const original = fake.fetch;
      fake.fetch = async (input, init) => {
        const res = await original(input, init);
        const url =
          typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
        if (url.includes("/playback/policy") && (!init?.method || init.method === "GET")) {
          const data = await res.json();
          data.overrides = [{ device: "Bedroom Apple TV", platform: "tvOS", maxHeight: 1080 }];
          data.seenDevices = [
            {
              clientIdentifier: "atv-living",
              name: "Living room Apple TV",
              platform: "tvOS",
              device: "Apple TV",
              app: "Swiftfin",
              viewer: "You",
              lastSeen: "",
            },
            {
              clientIdentifier: "atv-bedroom",
              name: "Bedroom Apple TV",
              platform: "tvOS",
              device: "Apple TV",
              app: "Swiftfin",
              viewer: "You",
              lastSeen: new Date(Date.now() - 600_000).toISOString(),
              overrideIndex: 0,
            },
          ];
          return new Response(JSON.stringify(data), {
            status: res.status,
            headers: res.headers,
          });
        }
        return res;
      };
    });

    const living = await screen.findByText("Living room Apple TV", {}, WAIT);
    const bedroom = screen.getByText("Bedroom Apple TV");
    const livingRow = living.closest("tr")!;
    const bedroomRow = bedroom.closest("tr")!;
    expect(livingRow).not.toBe(bedroomRow);

    // Empty lastSeen renders an em-dash rather than "never", and hover styling paints every td.
    expect(within(livingRow).getByText("—")).toBeInTheDocument();
    expect(within(livingRow).queryByText("never")).not.toBeInTheDocument();
    const tableWrapper = livingRow.closest("table")!.parentElement!.parentElement!.parentElement!;
    expect(tableWrapper.className).toContain("[&_tbody_tr:hover>td]:bg-surface-hover");

    // Compact action buttons show short text while keeping descriptive accessible labels.
    const createBtn = within(livingRow).getByRole("button", { name: "Create override" });
    expect(createBtn).toHaveTextContent("Override");
    const editBtn = within(bedroomRow).getByRole("button", { name: "Edit override" });
    expect(editBtn).toHaveTextContent("Edit");

    // Creating an override from a seen device pre-fills Match from row.device (runtime field),
    // not the friendly device name.
    fireEvent.click(createBtn);
    const createDrawer = await screen.findByRole("dialog", { name: "Edit Living room Apple TV" });
    expect(within(createDrawer).getByLabelText("Match")).toHaveValue("Apple TV");
    expect(within(createDrawer).getByLabelText("Platform")).toHaveValue("tvOS");
  });

  it("preserves an existing override's empty platform and falls back to name when device is empty", async () => {
    renderApp("/playback/devices/seen", {}, (fake) => {
      const original = fake.fetch;
      fake.fetch = async (input, init) => {
        const res = await original(input, init);
        const url =
          typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
        if (url.includes("/playback/policy") && (!init?.method || init.method === "GET")) {
          const data = await res.json();
          data.overrides = [{ product: "PlxNative", device: "PlxNative (LG TV)", maxHeight: 2160 }];
          data.seenDevices = [
            {
              clientIdentifier: "plx-lg",
              name: "PlxNative (LG TV)",
              platform: "webOS",
              device: "LG webOS TV",
              app: "PlxNative",
              viewer: "You",
              lastSeen: new Date(Date.now() - 600_000).toISOString(),
              overrideIndex: 0,
            },
            {
              clientIdentifier: "legacy-dev",
              name: "Legacy Box",
              platform: "Android",
              device: "",
              app: "",
              viewer: "You",
              lastSeen: new Date(Date.now() - 1200_000).toISOString(),
            },
          ];
          return new Response(JSON.stringify(data), {
            status: res.status,
            headers: res.headers,
          });
        }
        return res;
      };
    });

    const plxRow = (await screen.findByText("PlxNative (LG TV)", {}, WAIT)).closest("tr")!;
    fireEvent.click(within(plxRow).getByRole("button", { name: "Edit override" }));
    const editDrawer = await screen.findByRole("dialog", { name: "Edit PlxNative (LG TV)" });
    expect(within(editDrawer).getByLabelText("Match")).toHaveValue("PlxNative (LG TV)");
    expect(within(editDrawer).getByLabelText("Platform")).toHaveValue("");
    fireEvent.click(within(editDrawer).getByRole("button", { name: "Cancel" }));

    const legacyRow = screen.getByText("Legacy Box").closest("tr")!;
    fireEvent.click(within(legacyRow).getByRole("button", { name: "Create override" }));
    const legacyDrawer = await screen.findByRole("dialog", { name: "Edit Legacy Box" });
    expect(within(legacyDrawer).getByLabelText("Match")).toHaveValue("Legacy Box");
  });

  it("toggles Always convert video in the app compatibility drawer and persists cleanly", async () => {
    const { fake } = renderApp("/playback/devices");
    fireEvent.click(await screen.findByRole("button", { name: "Edit Plezy" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "Edit Plezy" });

    const toggle = within(drawer).getByRole("switch", { name: "Always convert video" });
    expect(toggle).not.toBeChecked();
    expect(
      within(drawer).getByText(
        /^Encode video instead of copying it when streaming\. Direct play of the original file is unaffected\./,
      ),
    ).toBeInTheDocument();

    fireEvent.click(toggle);
    expect(toggle).toBeChecked();
    fireEvent.click(within(drawer).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/clients")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/clients")[0]!.body).toMatchObject({
      overrides: [{ product: "Plezy", productExact: true, alwaysEncodeVideo: true }],
    });

    // Reopen and toggle back off: removes the now-empty override cleanly.
    fireEvent.click(await screen.findByRole("button", { name: "Edit Plezy" }, WAIT));
    const reopenDrawer = await screen.findByRole("dialog", { name: "Edit Plezy" });
    const reopenToggle = within(reopenDrawer).getByRole("switch", { name: "Always convert video" });
    expect(reopenToggle).toBeChecked();
    fireEvent.click(reopenToggle);
    expect(reopenToggle).not.toBeChecked();
    fireEvent.click(within(reopenDrawer).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/clients")).toHaveLength(2));
    expect(calls(fake, "PUT", "/config/clients")[1]!.body).toEqual({ overrides: [] });
  });
});

describe("Releases", () => {
  it("edits client rules in a drawer using a device and named viewer", async () => {
    const { fake } = renderApp("/quality/rules");
    expect(await screen.findByRole("button", { name: "Edit rule 1" }, WAIT)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Device" })).not.toBeInTheDocument();
    expect(
      screen.queryByRole("switch", { name: "Convert Dolby and DTS audio to AAC" }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Edit rule 1" }));
    const drawer = await screen.findByRole("dialog", { name: "Rule 1" });
    expect(within(drawer).getByRole("button", { name: "Device" })).toBeInTheDocument();
    const surroundSwitch = within(drawer).getByRole("switch", {
      name: "Convert Dolby and DTS audio to AAC",
    });
    expect(surroundSwitch).not.toBeChecked();
    expect(
      within(drawer).getByText(
        "For free Infuse and other players that can't play surround formats.",
      ),
    ).toBeInTheDocument();
    fireEvent.click(surroundSwitch);
    await choose("Protocol", "Plex");
    await choose("Device", "Plex Web");
    await choose("Viewer", "The Kids");
    await choose("Profile", "1080p");
    await choose("Quality limit", "1080p");
    fireEvent.click(screen.getByRole("button", { name: "Save rule" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/quality")[0]!.body).toMatchObject({
      rules: [
        {
          protocol: "plex",
          product: "Plex Web",
          userId: 3,
          profile: "1080p",
          maxHeight: 1080,
          convertSurroundAudio: true,
        },
      ],
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Edit rule 1" }));
    const reopened = await screen.findByRole("dialog", { name: "Rule 1" });
    expect(
      within(reopened).getByRole("switch", { name: "Convert Dolby and DTS audio to AAC" }),
    ).toBeChecked();
  });

  it("organizes the profile drawer into sub-tabs, deduplicates copy, and hides TV size overrides on movies", async () => {
    renderApp("/quality/profiles");
    fireEvent.click(await screen.findByRole("button", { name: "Edit 4K" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "4K" });
    expect(within(drawer).getByRole("tab", { name: "Tiers" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(within(drawer).getByText("Qualities")).toBeInTheDocument();

    fireEvent.click(within(drawer).getByRole("tab", { name: "Limits" }));
    expect(
      within(drawer).getAllByText(
        "Maximum limits and blocked releases always apply. Minimum resolution is a preference.",
      ),
    ).toHaveLength(1);
    expect(within(drawer).getByRole("group", { name: "Movies" })).toBeInTheDocument();
    expect(within(drawer).queryByRole("group", { name: "TV episodes" })).not.toBeInTheDocument();
    expect(within(drawer).getByLabelText("Maximum MB per minute")).toHaveValue(1000);

    fireEvent.click(within(drawer).getByRole("tab", { name: "Scoring" }));
    expect(within(drawer).getByText("Custom formats")).toBeInTheDocument();
    expect(within(drawer).getByLabelText("Score for Remux")).toHaveValue(6000);
    expect(
      within(drawer).getByRole("button", { name: "More options: Custom format definitions" }),
    ).toBeInTheDocument();
    fireEvent.click(within(drawer).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "4K" })).toBeNull());

    // Shows profiles still offer the TV episodes size override.
    fireEvent.click(screen.getByRole("radio", { name: "Shows" }));
    fireEvent.click(await screen.findByRole("button", { name: "Edit 1080p" }, WAIT));
    const showDrawer = await screen.findByRole("dialog", { name: "1080p" });
    fireEvent.click(within(showDrawer).getByRole("tab", { name: "Limits" }));
    expect(within(showDrawer).getByRole("group", { name: "TV episodes" })).toBeInTheDocument();
    expect(within(showDrawer).getByLabelText("Episode maximum MB per minute")).toHaveValue(0);
  });

  it("reorders the quality order and changes a format score", async () => {
    const { fake } = renderApp("/quality/profiles");
    fireEvent.click(await screen.findByRole("button", { name: "Edit 4K" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "4K" });
    fireEvent.click(within(drawer).getByRole("button", { name: "Move 1080p up" }));
    fireEvent.click(within(drawer).getByRole("tab", { name: "Scoring" }));
    fireEvent.change(within(drawer).getByLabelText("Score for Remux"), {
      target: { value: "7000" },
    });
    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
    const body = calls(fake, "PUT", "/config/quality")[0]!.body as EtherConfig["quality"];
    const saved4k = body.profiles?.find((p) => p.name === "4K");
    expect(saved4k?.qualityOrder?.map((i) => i.name)).toEqual(["1080p", "2160p"]);
    expect(saved4k?.customFormatScores?.["ether:remux"]).toBe(7000);
  });

  it("resets merged resolution groups to the shipped grouping in one click", async () => {
    const { fake } = renderApp("/quality/profiles");
    fireEvent.click(await screen.findByRole("button", { name: "Edit 4K" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "4K" });
    expect(within(drawer).getByText("Audio language")).toBeInTheDocument();
    fireEvent.click(
      within(drawer).getByRole("button", { name: "Rank 1080p the same as the row above" }),
    );
    fireEvent.click(within(drawer).getByRole("button", { name: "Reset resolution groups" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
    const body = calls(fake, "PUT", "/config/quality")[0]!.body as EtherConfig["quality"];
    const saved4k = body.profiles?.find((p) => p.name === "4K");
    expect(saved4k?.qualityOrder?.map((i) => i.name)).toEqual(["2160p", "1080p"]);
    expect(saved4k?.qualityOrder?.every((i) => i.qualities.length > 1)).toBe(true);
  });

  it("shows Preferred / Required audio language mode only for specific languages and keeps existing profiles Required", async () => {
    const { fake } = renderApp("/quality/profiles");
    // Simulate an existing profile that has a specific language and no saved audioLanguageMode.
    const existing = fake.state.config.quality.profiles?.find((p) => p.name === "4K");
    if (existing) {
      existing.language = "en";
      delete existing.audioLanguageMode;
    }
    fireEvent.click(await screen.findByRole("button", { name: "Edit 4K" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "4K" });
    const modeGroup = within(drawer).getByRole("radiogroup", { name: "Audio language mode" });
    expect(within(modeGroup).getByRole("radio", { name: "Required" })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    fireEvent.click(within(modeGroup).getByRole("radio", { name: "Preferred" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
    const body = calls(fake, "PUT", "/config/quality")[0]!.body as EtherConfig["quality"];
    expect(body.profiles?.find((p) => p.name === "4K")?.audioLanguageMode).toBe("preferred");

    // 1080p has no specific language (Original) so the mode toggle stays hidden.
    fireEvent.click(await screen.findByRole("button", { name: "Edit 1080p" }, WAIT));
    const drawer1080 = await screen.findByRole("dialog", { name: "1080p" });
    expect(
      within(drawer1080).queryByRole("radiogroup", { name: "Audio language mode" }),
    ).toBeNull();
  });

  it("shows Default at home and Default away badges and updates them from the profile drawer", async () => {
    const { fake } = renderApp("/quality/profiles");
    expect(await screen.findByText("Default at home", {}, WAIT)).toBeInTheDocument();
    expect(screen.getByText("Default away")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Edit 4K" }));
    const drawer = await screen.findByRole("dialog", { name: "4K" });
    const defaultsGroup = within(drawer).getByRole("group", { name: "Default for" });
    fireEvent.click(within(defaultsGroup).getByRole("button", { name: "At home" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/quality")[0]!.body).toMatchObject({
      defaultMovie: "4K",
      default: "1080p",
    });
  });

  it("renames a profile in its drawer and keeps rules pointing at it", async () => {
    const { fake } = renderApp("/quality/profiles");
    fireEvent.click(await screen.findByRole("button", { name: "Edit 4K" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "4K" });
    fireEvent.change(within(drawer).getByLabelText("Name"), { target: { value: "UHD" } });
    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
    const body = calls(fake, "PUT", "/config/quality")[0]!.body as EtherConfig["quality"];
    expect(body.profiles?.[0]?.name).toBe("UHD");
    expect(body.rules?.[0]).toMatchObject({ product: "Plex for Apple TV", profile: "UHD" });
  });

  it("removes a profile a client rule uses, with its rules (#9)", async () => {
    const { fake } = renderApp("/quality/profiles");
    fireEvent.click(await screen.findByRole("button", { name: "Edit 4K" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "4K" });
    fireEvent.click(within(drawer).getByRole("button", { name: "Remove profile" }));
    expect(await screen.findByText(/The device rule using it will be removed/)).toBeInTheDocument();
    fireEvent.click(await screen.findByRole("button", { name: "Remove" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
    const body = calls(fake, "PUT", "/config/quality")[0]!.body as EtherConfig["quality"];
    expect(body.profiles?.map((p) => `${p.mediaType}:${p.name}`)).toEqual([
      "movie:1080p",
      "tv:1080p",
    ]);
    expect(body.rules).toEqual([]);
    expect(body).toMatchObject({ default: "1080p", remote: "1080p" });
  });

  it("rejects invalid custom-format JSON inside an imported profile", async () => {
    const { fake } = renderApp("/quality/profiles", {}, (f) => {
      const p = f.state.config.quality.profiles?.find((x) => x.name === "1080p");
      if (p) p.minCustomFormatScore = 0;
    });
    fireEvent.click(await screen.findByRole("button", { name: "Edit 1080p" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "1080p" });
    fireEvent.click(within(drawer).getByRole("tab", { name: "Scoring" }));
    fireEvent.click(
      within(drawer).getByRole("button", { name: "More options: Custom format definitions" }),
    );
    const defs = await within(drawer).findByLabelText("Custom format definitions");
    fireEvent.change(defs, { target: { value: "unfinished JSON" } });
    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    expect(await screen.findByText(/aren't valid JSON/)).toBeInTheDocument();
    expect(calls(fake, "PUT", "/config/quality")).toHaveLength(0);
    expect(calls(fake, "PUT", "/config/resolver")).toHaveLength(0);
  });

  it("sets a minimum score and removes a format from the profile", async () => {
    const { fake } = renderApp("/quality/profiles");
    fireEvent.click(await screen.findByRole("button", { name: "Edit 1080p" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "1080p" });
    fireEvent.click(within(drawer).getByRole("tab", { name: "Scoring" }));
    fireEvent.click(within(drawer).getByRole("switch", { name: "Set a minimum score" }));
    fireEvent.change(within(drawer).getByLabelText("Minimum score"), { target: { value: "100" } });
    fireEvent.click(within(drawer).getByRole("button", { name: "Remove Blu-ray" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
    const body = calls(fake, "PUT", "/config/quality")[0]!.body as EtherConfig["quality"];
    const saved = body.profiles?.find((p) => p.name === "1080p");
    expect(saved?.minCustomFormatScore).toBe(100);
    expect(saved?.customFormatScores?.["ether:bluray"]).toBeUndefined();
    expect(saved?.customFormatScores?.["ether:webdl"]).toBe(2000);
  });
});

describe("Source routing", () => {
  it("keeps which source goes first on the Profiles page, with measured startup, and saves it with the save bar", async () => {
    const { fake } = renderApp("/quality/profiles");
    const section = (
      await screen.findByRole("heading", { name: "Which source goes first" }, WAIT)
    ).closest("section")!;
    expect(
      await within(section).findByText(/debrid 410 ms \(22 plays\)/, {}, WAIT),
    ).toBeInTheDocument();
    expect(within(section).getByText(/Usenet 2\.1 s \(37 plays\)/)).toBeInTheDocument();
    fireEvent.click(await screen.findByRole("button", { name: "Edit 1080p" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "1080p" }, WAIT);
    expect(
      within(drawer).queryByRole("button", { name: "Which source goes first" }),
    ).not.toBeInTheDocument();
    fireEvent.click(within(drawer).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "1080p" })).toBeNull());

    fireEvent.click(within(section).getByRole("button", { name: "Which source goes first" }));
    fireEvent.click(await screen.findByRole("option", { name: "Usenet first" }));
    expect(calls(fake, "PUT", "/config/resolver")).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/resolver")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/resolver")[0]!.body).toEqual({
      sourcePreference: "usenet",
      sourceAlwaysFirst: false,
    });
    // "Always cached debrid" saves debrid with the always flag.
    fireEvent.click(within(section).getByRole("button", { name: "Which source goes first" }));
    fireEvent.click(await screen.findByRole("option", { name: "Always cached debrid" }));
    fireEvent.click(await screen.findByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/resolver")).toHaveLength(2));
    expect(calls(fake, "PUT", "/config/resolver")[1]!.body).toEqual({
      sourcePreference: "debrid",
      sourceAlwaysFirst: true,
    });
  });
});

describe("Release selection", () => {
  it("lets the owner set how many same-quality versions apps list on the Profiles page", async () => {
    const { fake } = renderApp("/quality/profiles");
    const field = await screen.findByLabelText("Same-quality versions to offer", {}, WAIT);
    expect(field).toHaveValue(1);
    expect(screen.getByLabelText("Offer higher versions")).not.toBeChecked();
    fireEvent.change(field, { target: { value: "3" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/resolver")).toHaveLength(1));
    // Only the fields the form shows, so other resolver settings are never undone.
    expect(calls(fake, "PUT", "/config/resolver")[0]!.body).toEqual({
      sameQualityAlternatives: 3,
      offerHigherVersions: false,
    });
  });

  it("shows all four search controls without a disclosure", async () => {
    renderApp("/quality/searching");
    expect(await screen.findByRole("heading", { name: "How Ether searches" }, WAIT)).toBeVisible();
    expect(screen.queryByLabelText("Same-quality versions to offer")).toBeNull();
    expect(screen.queryByRole("button", { name: "More options: How Ether searches" })).toBeNull();
    expect(screen.getByLabelText("Releases to keep ready")).toHaveValue(3);
    expect(screen.getByLabelText("Health check samples")).toHaveValue(12);
    expect(screen.getByLabelText("Remember searches for")).toHaveValue(6);
    expect(screen.getByLabelText("Retry unavailable titles after")).toHaveValue(1);
  });

  it("edits blocked words next to blocked releases", async () => {
    renderApp("/quality/blocklist");
    expect(await screen.findByLabelText("Blocked release groups", {}, WAIT)).toBeInTheDocument();
    expect(screen.getByLabelText("Blocked terms")).toBeInTheDocument();
  });

  it("starts playback sooner from Quality › Searching", async () => {
    const { fake } = renderApp("/quality/searching");
    const start = await screen.findByRole(
      "switch",
      { name: "Start the top release before its health check completes" },
      WAIT,
    );
    expect(screen.queryByRole("button", { name: "Concurrent checks" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "More options: Start speed" }));
    expect(screen.getByRole("button", { name: "Concurrent checks" })).toBeInTheDocument();
    fireEvent.click(start);
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/resolver")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/resolver")[0]!.body).toEqual({
      optimisticStart: true,
      healthRace: 0,
    });
  });
});

describe("Release traces", () => {
  it("shows discovery and limits, preserving unknown for older history", () => {
    const [oldTrace, newTrace] = normalizeTraces([
      { titleName: "Old" },
      {
        titleName: "New",
        discovery: [
          { path: "live-early", count: 2 },
          { path: "cache", count: 1, rescue: true },
          { path: "feed", count: 0 },
          { path: "live-complete", count: 3 },
        ],
        limits: {
          profileMinHeight: 720,
          profileMaxHeight: 1080,
          clientMaxHeight: 2160,
          clientMaxBitrateKbps: 5000,
        },
      },
    ]);
    const view = render(<TraceDetail trace={oldTrace!} />);
    expect(screen.getByText("Discovery: unknown")).toBeInTheDocument();
    expect(screen.getByText("Effective playback limits: unknown")).toBeInTheDocument();
    view.rerender(<TraceDetail trace={newTrace!} />);
    expect(
      screen.getByText(
        /Live search ended early \(2 releases\).*Rescue: Cached search \(1 release\).*Feed results \(0 releases\).*Live search completed \(3 releases\)/,
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Profile: 720p–1080p.*Resolution: 2160p.*Bitrate: 5000 kbps/),
    ).toBeInTheDocument();
    for (const [min, max, label] of [
      [0, 0, "Any resolution"],
      [1080, 0, "At least 1080p"],
      [0, 2160, "Up to 2160p"],
    ] as const) {
      view.rerender(
        <TraceDetail
          trace={{
            ...newTrace!,
            limits: {
              profileMinHeight: min,
              profileMaxHeight: max,
              clientMaxHeight: 0,
              clientMaxBitrateKbps: 0,
            },
          }}
        />,
      );
      expect(
        screen.getByText(
          `Effective playback limits: Profile: ${label} · Resolution: unlimited · Bitrate: unlimited`,
        ),
      ).toBeInTheDocument();
    }
  });
  it("normalizes resolver traces using their JSON field names", () => {
    const t = normalizeTraces([
      {
        titleKey: "movie:438631",
        titleName: "Dune",
        candidates: [
          {
            releaseId: "release-1",
            name: "Dune.1080p",
            score: 5,
            rejected: true,
            reason: "too big",
          },
        ],
        err: "no luck",
        pinStatus: "unhealthy: health: unhealthy",
      },
      "junk",
    ]);
    expect(t).toHaveLength(1);
    expect(t[0]).toMatchObject({
      titleName: "Dune",
      err: "no luck",
      pinStatus: "unhealthy: health: unhealthy",
    });
    expect(t[0]!.candidates![0]).toMatchObject({
      releaseId: "release-1",
      name: "Dune.1080p",
      rejected: true,
      reason: "too big",
    });
  });

  it("maps max-height ceilings from the trace and from its history record", () => {
    const t = normalizeTraces([
      {
        titleName: "Dune",
        profileMaxHeight: 2160,
        clientMaxHeight: 1080,
        effectiveMaxHeight: 1080,
        candidates: [],
      },
      // As a history entry delivers them: on the record's nested trace.
      { titleName: "Old", trace: { profileMaxHeight: 1080, effectiveMaxHeight: 1080 } },
      { titleName: "Legacy" },
      // The real pre-PR shape: limits without profileConfiguredMaxHeight.
      {
        titleName: "LegacyLimits",
        limits: {
          profileMinHeight: 1080,
          profileMaxHeight: 1080,
          clientMaxHeight: 1080,
          clientMaxBitrateKbps: 0,
        },
      },
      // A new explicitly unlimited profile: configured recorded as 0.
      {
        titleName: "AnyProfile",
        limits: {
          profileMinHeight: 0,
          profileMaxHeight: 1080,
          profileConfiguredMaxHeight: 0,
          clientMaxHeight: 1080,
          clientMaxBitrateKbps: 0,
        },
      },
    ]);
    expect(t[0]).toMatchObject({
      profileMaxHeight: 2160,
      clientMaxHeight: 1080,
      effectiveMaxHeight: 1080,
    });
    expect(t[1]).toMatchObject({
      profileMaxHeight: 1080,
      clientMaxHeight: 0,
      effectiveMaxHeight: 1080,
    });
    expect(t[2]!.profileMaxHeight).toBeUndefined();
    expect(t[2]).toMatchObject({ clientMaxHeight: 0, effectiveMaxHeight: 0 });
    expect(resolutionCeilings(t[0]!)).toBe("Profile ≤2160 · Client ≤1080 · Effective ≤1080");
    expect(resolutionCeilings(t[1]!)).toBe("Profile ≤1080 · Client Any · Effective ≤1080");
    expect(resolutionCeilings(t[2]!)).toBeNull();
    // Legacy limits never recorded the configured ceiling: unknown,
    // so no ceilings line at all (not an invented "Profile Any").
    expect(t[3]!.profileMaxHeight).toBeUndefined();
    expect(t[3]).toMatchObject({ clientMaxHeight: 1080, effectiveMaxHeight: 1080 });
    expect(resolutionCeilings(t[3]!)).toBeNull();
    // Explicitly unlimited is present 0 and does render as "Any".
    expect(t[4]!.profileMaxHeight).toBe(0);
    expect(resolutionCeilings(t[4]!)).toBe("Profile Any · Client ≤1080 · Effective ≤1080");
    const chosen = normalizeTraces([
      { titleName: "Picked", chooser: "auto" },
      { titleName: "Version", chooser: "viewer", chosenVersion: "Movie.2160p" },
      { titleName: "Legacy" },
    ]);
    expect(choiceLabel(chosen[0]!.chooser, chosen[0]!.chosenVersion)).toBe("Auto");
    expect(choiceLabel(chosen[1]!.chooser, chosen[1]!.chosenVersion)).toBe(
      "Viewer/client picked version Movie.2160p",
    );
    expect(choiceLabel(chosen[2]!.chooser, chosen[2]!.chosenVersion)).toBeNull();
  });

  it("shows the ceilings in the trace detail", () => {
    render(
      <MemoryRouter>
        <TraceDetail
          trace={{
            titleKey: "603",
            titleName: "Dune",
            profile: "4K",
            profileMaxHeight: 2160,
            clientMaxHeight: 1080,
            effectiveMaxHeight: 1080,
            at: "",
            candidates: [],
            searchDuration: 0,
          }}
        />
      </MemoryRouter>,
    );
    expect(screen.getByText("Profile ≤2160 · Client ≤1080 · Effective ≤1080")).toBeInTheDocument();
  });

  it("omits the ceilings for a legacy trace without them", () => {
    render(
      <MemoryRouter>
        <TraceDetail
          trace={{
            titleKey: "603",
            titleName: "Dune",
            profile: "1080p",
            at: "",
            candidates: [],
            searchDuration: 0,
          }}
        />
      </MemoryRouter>,
    );
    expect(screen.queryByText(/Profile ≤|Profile Any/)).not.toBeInTheDocument();
  });

  it("opens the release history tab", async () => {
    renderApp("/quality/history");
    expect(await screen.findByRole("heading", { name: "Quality" }, WAIT)).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "History" })).toHaveAttribute("aria-selected", "true");
  });

  it("summarizes raw multi-source Go decision errors in plain English while keeping technical details in the drawer", async () => {
    const joined = [
      "TorBox: debrid: not supported by this provider: background check cannot decide this source",
      "Real-Debrid: debrid: not supported by this provider: background check cannot decide this source",
      "newznab: held back by local budget: newznab: discovery share reached: indexer daily budget exhausted",
      "newznab: held back by local budget: newznab: discovery share reached: indexer daily budget exhausted",
    ].join("\n");
    expect(decisionErrorSummary(joined)).toBe(
      "Background check skipped live-only sources · Search budget saved for playback",
    );
    expect(decisionErrorSummary("context canceled")).toBe("Cancelled before finishing");
    expect(decisionErrorSummary(`"All Her Fault — S01E05": not available yet`)).toBe(
      "No playable release available yet",
    );
    expect(
      decisionErrorSummary(
        `not cached on the debrid service: "Movie.2025" on TorBox: queued at TorBox behind 3 active downloads: sent to the debrid service to download: not cached by any debrid provider: not available yet`,
      ),
    ).toBe(
      "Not cached yet — queued to download on debrid (queued at TorBox behind 3 active downloads)",
    );
    expect(
      decisionErrorSummary(
        `not cached on the debrid service: "Movie.2025" on TorBox: sent to the debrid service to download: not cached by any debrid provider: not available yet`,
      ),
    ).toBe("Not cached yet — queued to download on debrid");

    renderApp("/quality/history");
    const summary = await screen.findByText(
      "Every matching release failed its health check",
      {},
      WAIT,
    );
    expect(summary).toBeInTheDocument();
    expect(screen.queryByText("no healthy candidates")).not.toBeInTheDocument();
    fireEvent.click(summary);
    const drawer = await screen.findByRole("dialog");
    expect(
      await within(drawer).findByText("Every matching release failed its health check", {}, WAIT),
    ).toBeInTheDocument();
    expect(within(drawer).getByText("Technical details")).toBeInTheDocument();
    expect(within(drawer).getByText("no healthy candidates")).toBeInTheDocument();
  });
});

describe("Playback", () => {
  it("edits cache policy from the cache page", async () => {
    const { fake } = renderApp("/cache");
    expect(await screen.findByRole("heading", { name: "Cache" }, WAIT)).toBeInTheDocument();
    expect(await screen.findByRole("tab", { name: "Overview" }, WAIT)).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.queryByRole("button", { name: /Edit cache/i })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Edit policy" }));
    fireEvent.change(screen.getByLabelText("Expire after inactivity"), {
      target: { value: "14" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save policy" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/cache")).toHaveLength(1));
  });

  it("keeps stream limits on Streams & transcoding behind More options", async () => {
    const { fake } = renderApp("/playback/transcoding");
    expect(
      await screen.findByRole("heading", { name: "Who can watch at once" }, WAIT),
    ).toBeVisible();
    expect(screen.queryByLabelText("Streams per viewer")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "More options: Who can watch at once" }));
    fireEvent.change(screen.getByLabelText("Streams per viewer"), { target: { value: "3" } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/server")).toHaveLength(1));
    expect(calls(fake, "PUT", "/config/server")[0]!.body).toEqual({
      defaultStreamLimit: 3,
      maxStreams: expect.any(Number),
    });
    expect(screen.queryByLabelText("Remote bitrate cap")).toBeNull();
  });

  it("shows detected hardware above the transcoding settings and hides Windows/Linux-only controls on Mac", async () => {
    renderApp("/playback/transcoding");
    expect(await screen.findByRole("tab", { name: "Streams & transcoding" }, WAIT)).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(await screen.findByText("Apple VideoToolbox", {}, WAIT)).toBeInTheDocument();
    expect(screen.getByText("H.264")).toBeInTheDocument();
    expect(screen.getByText("H.265/HEVC")).toBeInTheDocument();
    expect(screen.getByText("HDR10")).toBeInTheDocument();
    expect(
      await screen.findByRole("button", { name: "Hardware acceleration" }, WAIT),
    ).toBeInTheDocument();
    expect(screen.queryByText(/RTX Video:/)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "RTX Video Super Resolution" }),
    ).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "More options: Converting for the app" }));
    expect(screen.getByLabelText("Simultaneous transcodes")).toBeInTheDocument();
    expect(screen.queryByLabelText("VA-API device")).not.toBeInTheDocument();
  });

  it("shows VA-API device on Linux VA-API hosts without showing RTX Video", async () => {
    renderApp("/playback/transcoding", {
      hwaccel: {
        os: "linux",
        arch: "amd64",
        vendor: "intel",
        ffmpeg: "8.0.1",
        filtersProbed: true,
        toneMap: ["vaapi"],
        colour: ["scale"],
        hwaccel: {
          name: "vaapi",
          encoders: ["h264_vaapi", "hevc_vaapi"],
          device: "/dev/dri/renderD128",
        },
        enhancement: {
          backend: "nvenc-ngx",
          setupState: "off",
          available: false,
          active: 0,
        },
      },
    });
    expect(await screen.findByText("Intel VA-API", {}, WAIT)).toBeInTheDocument();
    expect(screen.queryByText(/RTX Video:/)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "RTX Video Super Resolution" }),
    ).not.toBeInTheDocument();
    const more = await screen.findByRole(
      "button",
      { name: "More options: Converting for the app" },
      WAIT,
    );
    fireEvent.click(more);
    expect(screen.getByLabelText("VA-API device")).toBeInTheDocument();
  });

  it("displays 20-second trailer timeouts in Seconds and toggles Fewer options", async () => {
    renderApp("/playback/prerolls");
    const more = await screen.findByRole("button", { name: "More options: Trailers" }, WAIT);
    fireEvent.click(more);
    expect(more).toHaveTextContent("Fewer options");
    expect(screen.getByLabelText("Give up after")).toHaveValue(20);
    expect(screen.getByRole("button", { name: "Give up after unit" })).toHaveTextContent("Seconds");
  });

  it("identifies cached subtitles by real title, episode and download age, with explicit unknown titles", async () => {
    renderApp("/playback/subtitles");
    expect(
      await screen.findByText("Some Show · S01E02 · The Episode", {}, WAIT),
    ).toBeInTheDocument();
    expect(screen.getByText("Some.Show.S01E02.720p.WEB-DL-GROUP")).toBeInTheDocument();
    expect(screen.getByText("Unknown title")).toBeInTheDocument();
    expect(screen.getByText(/3 hours ago/)).toBeInTheDocument();
    expect(screen.getByText(/yesterday/)).toBeInTheDocument();
    // No cache hash or raw rating key is shown as identification.
    expect(screen.queryByText(/abc123|orphan99/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Adjust timing for Some Show/ }));
    expect(await screen.findByRole("dialog", {}, WAIT)).toBeInTheDocument();
    expect(screen.getByLabelText("Offset (milliseconds)")).toBeInTheDocument();
  });

  it("turns the intro skip button off and names IntroDB", async () => {
    const { fake } = renderApp("/playback/extras");
    expect(
      await screen.findByText("Intro and credit times from IntroDB.", {}, WAIT),
    ).toBeInTheDocument();
    expect(
      screen
        .getAllByRole("link", { name: "Integrations" })
        .some((link) => link.getAttribute("href")?.endsWith("/services/introdb")),
    ).toBe(true);
    expect(screen.getByText("Chapter titles")).toBeInTheDocument();
    expect(screen.getByText("Subtitle dialogue")).toBeInTheDocument();
    const intro = screen.getByRole("switch", { name: "Skip intro" });
    expect(intro).toBeChecked();
    fireEvent.click(intro);
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/markers")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/markers")[0]!.body).toMatchObject({
      theIntroDb: true,
      skipIntro: false,
      skipCredits: true,
    });
  });

  it("saves nested server enhancement settings without losing transcoding preferences", async () => {
    const { fake } = renderApp("/playback/transcoding", {
      hwaccel: {
        os: "windows",
        arch: "amd64",
        vendor: "nvidia",
        ffmpeg: "8.0.1",
        filtersProbed: true,
        toneMap: ["cuda"],
        colour: ["zscale"],
        hwaccel: { name: "nvenc", encoders: ["h264_nvenc", "hevc_nvenc"] },
        enhancement: {
          backend: "nvenc-ngx",
          setupState: "off",
          available: false,
          active: 0,
        },
      },
    });
    await screen.findByRole("button", { name: "RTX Video Super Resolution" }, WAIT);
    expect(screen.queryByLabelText("ffmpeg path")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Enhancement quality")).not.toBeInTheDocument();
    await choose("RTX Video Super Resolution", "Automatic · local streams");
    fireEvent.click(screen.getByRole("button", { name: "More options: Sharper picture" }));
    await choose("Enhancement quality", "Streaming ultra");
    fireEvent.change(screen.getByLabelText("Enhanced resolution limit"), {
      target: { value: "1440" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/transcode")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/transcode")[0]!.body).toMatchObject({
      videoTranscoding: "on",
      hwAccel: "auto",
      rtxVideo: {
        mode: "auto",
        targetMaxHeight: 1440,
        sourceMaxHeight: 720,
        quality: 23,
        maxConcurrent: 1,
      },
    });
  });

  it("greys out hardware and transcoding-quality controls when video transcoding is turned off, preserving their values", async () => {
    const { fake } = renderApp("/playback/transcoding", {
      hwaccel: {
        os: "windows",
        arch: "amd64",
        vendor: "nvidia",
        ffmpeg: "8.0.1",
        filtersProbed: true,
        toneMap: ["cuda"],
        colour: ["zscale"],
        hwaccel: { name: "nvenc", encoders: ["h264_nvenc", "hevc_nvenc"] },
        enhancement: {
          backend: "nvenc-ngx",
          setupState: "off",
          available: false,
          active: 0,
        },
      },
    });
    const toggle = await screen.findByRole("button", { name: "Video transcoding" }, WAIT);
    expect(toggle).toHaveTextContent("On");
    expect(screen.getByText(/Live TV is unaffected/)).toBeInTheDocument();

    const hwBtn = screen.getByRole("button", { name: "Hardware acceleration" });
    const rtxBtn = screen.getByRole("button", { name: "RTX Video Super Resolution" });
    expect(hwBtn).toBeEnabled();
    expect(rtxBtn).toBeEnabled();

    await choose("Video transcoding", "Off");
    expect(hwBtn).toBeDisabled();
    expect(rtxBtn).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "More options: Converting for the app" }));
    const maxTranscodes = screen.getByLabelText("Simultaneous transcodes");
    expect(maxTranscodes).toBeDisabled();
    expect(maxTranscodes).toHaveValue(2);

    fireEvent.click(screen.getByRole("button", { name: "More options: Sharper picture" }));
    expect(screen.getByRole("button", { name: "Enhancement quality" })).toBeDisabled();
    expect(screen.getByLabelText("Enhanced resolution limit")).toBeDisabled();

    // Converter programs still apply for probing, remuxing, audio conversion, and Live TV.
    fireEvent.click(screen.getByRole("button", { name: "More options: Converter programs" }));
    expect(screen.getByLabelText("ffmpeg path")).toBeEnabled();

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/transcode")).toHaveLength(1), WAIT);
    expect(calls(fake, "PUT", "/config/transcode")[0]!.body).toMatchObject({
      videoTranscoding: "off",
      hwAccel: "auto",
      maxTranscodes: 2,
      rtxVideo: {
        mode: "off",
        sourceMaxHeight: 720,
        targetMaxHeight: 1080,
        quality: 2,
        maxConcurrent: 1,
      },
    });
  });
});
