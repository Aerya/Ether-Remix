import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AppProvider } from "../app-context";
import {
  deviceFromKey,
  deviceKey,
  defaultRulePosition,
  neverPicksProfile,
  profileUses,
  ruleLimits,
  ruleStanding,
  tidyRule,
  withProfileRenamed,
} from "../lib/qualityRules";
import type { EtherConfig, QualityRule } from "../lib/types";
import { MOCK_QUALITY_DEVICES } from "../mock/quality";
import { RuleDrawer } from "../pages/releases/RuleDrawer";
import { createFakeServer } from "../mock/server";
import { calls, renderApp } from "./harness";

const WAIT = { timeout: 10000 };

describe("qualityRules helpers", () => {
  it("round-trips device keys containing colons", () => {
    const match = deviceFromKey("app:jellyfin:Infuse", "Infuse");
    expect(match).toEqual({ app: "jellyfin:Infuse", label: "Infuse" });
    expect(deviceKey(match)).toBe("app:jellyfin:Infuse");
  });

  it("formats limits and tidies empty conditions", () => {
    expect(ruleLimits({ maxHeight: 1080, maxBitrateKbps: 8000, stereoAudio: true })).toEqual([
      "≤ 1080p",
      "≤ 8 Mbps",
      "Stereo",
    ]);
    expect(
      tidyRule({
        id: "r_1",
        name: "  ",
        libraries: [],
        exceptLibraries: true,
        where: "",
        kind: "",
        profile: "",
        maxHeight: 1080,
      }),
    ).toEqual({ id: "r_1", maxHeight: 1080 });
  });

  it("detects shadowed and overlapping rules using device groups", () => {
    const rules: QualityRule[] = [
      { id: "r_1", devices: [{ group: "tv", label: "TVs & streaming boxes" }], profile: "4K" },
      { id: "r_2", devices: [{ group: "mobile", label: "Phones & tablets" }], maxHeight: 1080 },
      {
        id: "r_3",
        devices: [{ client: "client-shield-lr", label: "Living room Shield" }],
        profile: "1080p",
      },
      { id: "r_4", libraries: [1], profile: "1080p" },
    ];
    // Rule 1 (TVs) completely covers rule 3 (Living room Shield, which is a TV).
    expect(neverPicksProfile(rules, 2, MOCK_QUALITY_DEVICES)).toBe(true);
    expect(ruleStanding(rules.slice(0, 2), rules[2], MOCK_QUALITY_DEVICES).shadowedBy).toEqual([0]);
    // Rule 2 is limits-only and never shadows a profile.
    expect(neverPicksProfile(rules, 3, MOCK_QUALITY_DEVICES)).toBe(false);
    // Rule 4 (a library, any device) overlaps rule 1 (TVs). Rule 3 never picks
    // a profile itself and gives the same one, so it is not named.
    expect(ruleStanding(rules.slice(0, 3), rules[3], MOCK_QUALITY_DEVICES)).toEqual({
      shadowedBy: [],
      overlaps: [0],
    });
  });

  it("only names earlier rules that can match the same plays", () => {
    const earlier: QualityRule[] = [
      // Migrated 0.3.20 rules match by name.
      { id: "r_1", viewers: [3], devices: [{ contains: "Roku", label: "Roku" }], profile: "720p" },
      {
        id: "r_2",
        devices: [{ contains: "Plex for Android", label: "Plex for Android" }],
        profile: "720p",
      },
      { id: "r_3", where: "away", profile: "1080p" },
    ];
    const infuse: QualityRule = {
      id: "",
      viewers: [3],
      devices: [{ app: "jellyfin:Infuse", label: "Infuse" }],
      profile: "4K",
    };
    // Infuse runs on Apple TV and iOS: never a Roku, never Plex for Android.
    expect(ruleStanding(earlier, infuse, MOCK_QUALITY_DEVICES)).toEqual({
      shadowedBy: [],
      overlaps: [2],
    });
    // The same app on a kind of device it does not run on can't overlap either.
    expect(
      ruleStanding(
        [{ id: "r_pc", devices: [{ group: "computer", label: "Computers" }], profile: "720p" }],
        infuse,
        MOCK_QUALITY_DEVICES,
      ).overlaps,
    ).toEqual([]);
  });

  it("sees a rule shadowed by a Movies rule and an Episodes rule together", () => {
    const rules: QualityRule[] = [
      { id: "r_1", where: "away", maxBitrateKbps: 8000 },
      { id: "r_2", viewers: [2], profile: "720p" },
      { id: "r_3", kind: "movie", profile: "4K" },
      { id: "r_4", kind: "episode", profile: "1080p" },
    ];
    const fresh: QualityRule = { id: "", libraries: [4], profile: "720p" };
    expect(ruleStanding(rules, fresh, MOCK_QUALITY_DEVICES).shadowedBy).toEqual([2, 3]);
    // New rules start above the first catch-all that picks a profile.
    expect(defaultRulePosition(rules)).toBe(2);
    expect(ruleStanding(rules.slice(0, 2), fresh, MOCK_QUALITY_DEVICES).shadowedBy).toEqual([]);
    expect(defaultRulePosition([rules[0]!, rules[1]!])).toBe(2);
  });

  it("tracks profile uses and renames references across defaults and rules", () => {
    const quality: EtherConfig["quality"] = {
      profiles: [],
      default: "4K",
      defaultAway: "1080p",
      rules: [{ id: "r_1", name: "TVs", profile: "4K" }],
    };
    const names = { library: (id: number) => `Lib ${id}`, viewer: (id: number) => `User ${id}` };
    expect(profileUses(quality, "4K", names)).toEqual([
      { key: "r_1", label: "Rule 1: TVs", open: { rule: "r_1" } },
      { key: "default", label: "Everyone else, at home", open: { fallback: "home" } },
    ]);
    expect(profileUses(quality, "1080p", names)).toEqual([
      { key: "away", label: "Everyone else, away", open: { fallback: "away" } },
    ]);
    const renamed = withProfileRenamed(quality, "4K", "UHD");
    expect(renamed.default).toBe("UHD");
    expect(renamed.defaultAway).toBe("1080p");
    expect(renamed.rules?.[0]?.profile).toBe("UHD");
  });
});

describe("Releases › Rules", () => {
  it("lists rules in order with the two fallback rows fixed at the bottom", async () => {
    renderApp("/releases/rules");
    expect(await screen.findByText("Living room Apple TV", {}, WAIT)).toBeInTheDocument();
    expect(
      screen.getByText(
        "Checked from the top. The first rule with a profile picks it; every matching limit applies.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("Everyone else, at home")).toBeInTheDocument();
    expect(screen.getByText("Everyone else, away")).toBeInTheDocument();
    expect(screen.getByText("Same as at home")).toBeInTheDocument();
  });

  it("validates empty rules and adds a limits-only rule via PUT /config/quality", async () => {
    const { fake } = renderApp("/releases/rules");
    fireEvent.click(await screen.findByRole("button", { name: "Add rule" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "New rule" });

    // Saving without a profile or limit shows inline validation.
    fireEvent.click(within(drawer).getByRole("button", { name: "Save rule" }));
    expect(
      await within(drawer).findByText("Choose a profile or set at least one limit."),
    ).toBeInTheDocument();
    expect(calls(fake, "PUT", "/config/quality")).toHaveLength(0);

    // Pick Device -> Only... -> Phones & tablets from GET /quality/devices.
    const deviceMode = within(drawer).getByRole("radiogroup", { name: "Device" });
    fireEvent.click(within(deviceMode).getByRole("radio", { name: "Only…" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Devices" }));
    fireEvent.click(await screen.findByRole("option", { name: /Phones & tablets/ }));

    // Pick Where -> Away and limits (1080p, 5 Mbps, Stereo), leaving Profile on "No change".
    fireEvent.click(
      within(within(drawer).getByRole("radiogroup", { name: "Where" })).getByRole("radio", {
        name: "Away",
      }),
    );
    fireEvent.click(
      within(within(drawer).getByRole("radiogroup", { name: "Max resolution" })).getByRole(
        "radio",
        { name: "1080p" },
      ),
    );
    fireEvent.change(within(drawer).getByLabelText("Max bitrate"), { target: { value: "5" } });
    fireEvent.click(
      within(within(drawer).getByRole("radiogroup", { name: "Audio" })).getByRole("radio", {
        name: "Stereo",
      }),
    );

    fireEvent.click(within(drawer).getByRole("button", { name: "Save rule" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
    const body = calls(fake, "PUT", "/config/quality")[0]!.body as EtherConfig["quality"];
    expect(body.rules).toHaveLength(2);
    expect(body.rules?.[1]).toMatchObject({
      devices: [{ group: "mobile", label: "Phones & tablets" }],
      where: "away",
      maxHeight: 1080,
      maxBitrateKbps: 5000,
      stereoAudio: true,
    });
    expect(body.rules?.[1]?.profile).toBeUndefined();
    // Wait for drawer close and list refresh with chips.
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(await screen.findByText("≤ 1080p")).toBeInTheDocument();
    expect(screen.getByText("≤ 5 Mbps")).toBeInTheDocument();
    expect(screen.getByText("Stereo")).toBeInTheDocument();
  });

  it("reorders rules with order buttons and saves the new order", async () => {
    const { fake } = renderApp("/releases/rules", {}, (f) => {
      f.state.config.quality.rules = [
        { id: "r_first", name: "First rule", profile: "4K" },
        { id: "r_second", name: "Second rule", maxHeight: 1080 },
      ];
    });
    await screen.findByText("First rule", {}, WAIT);
    fireEvent.click(screen.getByRole("button", { name: "Move rule 2 up" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
    const body = calls(fake, "PUT", "/config/quality")[0]!.body as EtherConfig["quality"];
    expect(body.rules?.map((r) => r.id)).toEqual(["r_second", "r_first"]);
  });

  it("edits a fallback row in the rule drawer: profile, limits and Same as at home", async () => {
    const { fake } = renderApp("/releases/rules");
    fireEvent.click(await screen.findByRole("button", { name: "Edit Everyone else, away" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "Everyone else, away" });
    // Its condition is spelled out, not editable; away starts as Same as at home.
    expect(
      within(drawer).getByText("Applies to everything no rule above caught, away from home."),
    ).toBeInTheDocument();
    expect(within(drawer).queryByRole("radiogroup", { name: "Library" })).not.toBeInTheDocument();
    expect(within(drawer).queryByRole("button", { name: "Profile" })).not.toBeInTheDocument();

    fireEvent.click(within(drawer).getByRole("radio", { name: "Its own profile and limits" }));
    fireEvent.click(within(drawer).getByRole("button", { name: "Profile" }));
    fireEvent.click(await screen.findByRole("option", { name: "4K" }));
    fireEvent.click(
      within(within(drawer).getByRole("radiogroup", { name: "Max resolution" })).getByRole(
        "radio",
        { name: "1080p" },
      ),
    );
    fireEvent.click(within(drawer).getByRole("button", { name: "Save rule" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
    expect(fake.state.config.quality).toMatchObject({
      default: "1080p",
      defaultAway: "4K",
      defaultAwayLimits: { maxHeight: 1080 },
    });
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(await screen.findByText("≤ 1080p")).toBeInTheDocument();

    // Back to Same as at home clears the away profile and its limits.
    fireEvent.click(screen.getByRole("button", { name: "Edit Everyone else, away" }));
    const again = await screen.findByRole("dialog", { name: "Everyone else, away" });
    fireEvent.click(within(again).getByRole("radio", { name: "Same as at home" }));
    fireEvent.click(within(again).getByRole("button", { name: "Save rule" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(2));
    expect(fake.state.config.quality).toMatchObject({ defaultAway: "", defaultAwayLimits: {} });
  });

  it("flags shadowed rules in the list and offers a jump link in the drawer", async () => {
    renderApp("/releases/rules", {}, (f) => {
      f.state.config.quality.rules = [
        { id: "r_all", name: "Everything 4K", profile: "4K" },
        { id: "r_sub", name: "Movies in 1080p", libraries: [1], profile: "1080p" },
      ];
    });
    expect(await screen.findByText("Never picks a profile", {}, WAIT)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Edit rule 2" }));
    const drawer = await screen.findByRole("dialog", { name: "Movies in 1080p" });
    expect(
      within(drawer).getByText(/Rule 1 “Everything 4K” comes first for all of these/),
    ).toBeInTheDocument();
    fireEvent.click(within(drawer).getByRole("button", { name: "Open rule 1" }));
    expect(await screen.findByRole("dialog", { name: "Everything 4K" })).toBeInTheDocument();
  });

  it("starts a new rule above the catch-all rules and lets the drawer place it", async () => {
    const { fake } = renderApp("/releases/rules", {}, (f) => {
      f.state.config.quality.rules = [
        { id: "r_cap", name: "Away cap", where: "away", maxBitrateKbps: 8000 },
        { id: "r_movies", name: "All movies", kind: "movie", profile: "4K" },
        { id: "r_shows", name: "All episodes", kind: "episode", profile: "4K" },
      ];
    });
    fireEvent.click(await screen.findByRole("button", { name: "Add rule" }, WAIT));
    const drawer = await screen.findByRole("dialog", { name: "New rule" });
    // Above the first rule that picks a profile for anyone on any device.
    expect(within(drawer).getByRole("button", { name: "Position" })).toHaveTextContent(
      "Above rule 2: All movies",
    );
    fireEvent.click(within(drawer).getByRole("button", { name: "Profile" }));
    fireEvent.click(await screen.findByRole("option", { name: "1080p" }));
    expect(within(drawer).queryByTestId("rule-shadowed")).not.toBeInTheDocument();

    // At the bottom the two rules above take every play between them; the warning says so before saving.
    fireEvent.click(within(drawer).getByRole("button", { name: "Position" }));
    fireEvent.click(await screen.findByRole("option", { name: "Bottom" }));
    expect(within(drawer).getByTestId("rule-shadowed")).toHaveTextContent(
      "Rules 2, 3 come first for all of these, so this rule never picks a profile.",
    );
    fireEvent.click(within(drawer).getByRole("button", { name: "Insert before rule 2" }));
    expect(within(drawer).queryByTestId("rule-shadowed")).not.toBeInTheDocument();

    fireEvent.click(within(drawer).getByRole("button", { name: "Save rule" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
    const body = calls(fake, "PUT", "/config/quality")[0]!.body as EtherConfig["quality"];
    expect(body.rules?.map((r) => r.id || "new")).toEqual(["r_cap", "new", "r_movies", "r_shows"]);

    // Rows move straight to the top.
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    fireEvent.click(await screen.findByRole("button", { name: "Move rule 4 to top" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(2));
    const moved = calls(fake, "PUT", "/config/quality")[1]!.body as EtherConfig["quality"];
    expect(moved.rules?.[0]?.id).toBe("r_shows");
  });

  it("opens rules and presets from URL query params", async () => {
    renderApp("/releases/rules?new=1&library=1&profile=4K");
    const drawer = await screen.findByRole("dialog", { name: "New rule" }, WAIT);
    expect(await within(drawer).findByText("Only Movies", {}, WAIT)).toBeInTheDocument();
    // Clicking the widen button unlocks the library condition to the full picker.
    fireEvent.click(within(drawer).getByRole("button", { name: "Choose other libraries" }));
    expect(within(drawer).getByRole("radiogroup", { name: "Library" })).toBeInTheDocument();
  });

  it("opens an existing rule via ?rule=<id> and a default row via ?default=home", async () => {
    renderApp("/releases/rules?rule=r_appletv1");
    expect(
      await screen.findByRole("dialog", { name: "Living room Apple TV" }, WAIT),
    ).toBeInTheDocument();
  });

  it("removes a rule after confirmation and shows a dismissible upgrade note", async () => {
    const note =
      "Your quality settings from 0.3.20 were moved into these rules. Nothing plays differently.";
    const { fake } = renderApp("/releases/rules", {}, (f) => {
      f.state.config.quality.migratedRules = 1;
      f.state.config.quality.migrationNote = { at: "2026-11-24T15:04:05Z", dismissed: false };
      f.state.config.quality.rules![0]!.from = { kind: "rule", name: "Apple TV" };
    });
    expect(await screen.findByText(note, {}, WAIT)).toBeInTheDocument();
    // Each migrated rule says which 0.3.20 setting it came from.
    expect(screen.getByText("From the “Apple TV” rule")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText(note)).not.toBeInTheDocument();
    await waitFor(() =>
      expect(fake.state.config.quality.migrationNote).toEqual({
        at: "2026-11-24T15:04:05Z",
        dismissed: true,
      }),
    );
    expect(calls(fake, "PUT", "/config/quality")[0]!.body).toEqual({
      migrationNote: { at: "2026-11-24T15:04:05Z", dismissed: true },
    });

    fireEvent.click(screen.getByRole("button", { name: "Edit rule 1" }));
    const drawer = await screen.findByRole("dialog", { name: "Living room Apple TV" });
    fireEvent.click(within(drawer).getByRole("button", { name: "Remove rule" }));
    fireEvent.click(await screen.findByRole("button", { name: "Remove" }));
    await waitFor(() => expect(fake.state.config.quality.rules).toEqual([]));
  });

  it("renders standalone RuleDrawer with lockLibrary and lockViewer props", async () => {
    const fake = createFakeServer();
    vi.stubGlobal("fetch", vi.fn(fake.fetch));
    let closed = false;
    render(
      <MemoryRouter>
        <AppProvider
          boot={{
            ok: true,
            version: "test",
            setupComplete: true,
            authenticated: true,
            authRequired: false,
            serverName: "Test",
          }}
          reloadBoot={async () => {}}
        >
          <RuleDrawer
            open
            lockLibrary={1}
            lockViewer={2}
            onClose={() => {
              closed = true;
            }}
          />
        </AppProvider>
      </MemoryRouter>,
    );
    const drawer = await screen.findByRole("dialog", { name: "New rule" }, WAIT);
    expect(await within(drawer).findByText("Only Movies", {}, WAIT)).toBeInTheDocument();
    expect(await within(drawer).findByText("Only Sam Rivera", {}, WAIT)).toBeInTheDocument();
    fireEvent.click(within(drawer).getByRole("button", { name: "Cancel" }));
    expect(closed).toBe(true);
  });
});
