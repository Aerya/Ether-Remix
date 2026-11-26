import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { AppProvider } from "../app-context";
import type { QualityAnswer } from "../lib/types";
import { createFakeServer } from "../mock/server";
import {
  alsoInEffects,
  cellLimitSources,
  createRuleResolver,
} from "../pages/releases/qualitySummaryUi";
import { answerHeadline, layerText, WhoGetsWhat } from "../pages/releases/WhoGetsWhat";
import { calls, settleUi } from "./harness";

const WAIT = { timeout: 4000 };

async function renderWhoGetsWhat(
  setup?: (fake: ReturnType<typeof createFakeServer>) => Promise<unknown> | void,
) {
  const fake = createFakeServer();
  await setup?.(fake);
  vi.stubGlobal("fetch", vi.fn(fake.fetch));
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
        <WhoGetsWhat />
      </AppProvider>
    </MemoryRouter>,
  );
  return fake;
}

const headline = () => screen.getByTestId("quality-headline").textContent;

describe("who gets what", () => {
  it("checks a play for anyone on any device until a viewer and device are chosen", async () => {
    const fake = await renderWhoGetsWhat();
    await settleUi(() => expect(headline()).toBe("1080pSDR"));
    expect(calls(fake, "POST", "/quality/explain")).toHaveLength(1);
    // Any library doesn't say whether the play is a movie or an episode, so the check asks.
    expect(calls(fake, "POST", "/quality/explain")[0]?.body).toEqual({
      clientId: "any",
      kind: "movie",
      remote: false,
    });
    fireEvent.click(screen.getByRole("radio", { name: "Episode" }));
    await settleUi(() =>
      expect(calls(fake, "POST", "/quality/explain").at(-1)?.body).toEqual({
        clientId: "any",
        kind: "episode",
        remote: false,
      }),
    );
    // The server refuses a question with no device or app at all.
    const rawMissing = await fake.fetch("/ether/api/quality/explain", {
      method: "POST",
      body: JSON.stringify({ userId: 1, remote: false }),
    });
    expect(rawMissing.status).toBe(400);
  });

  it("renders the per-library summary, exceptions, partial rules, plugin rows, and filters", async () => {
    const fake = await renderWhoGetsWhat(async (f) => {
      // Create a mixed-kind library (mediaType "") to verify Movies / Episodes split rows.
      await f.fetch("/ether/api/libraries", {
        method: "POST",
        body: JSON.stringify({
          name: "Anime",
          source: "auto",
          pinned: false,
        }),
      });
      f.state.config.quality.migratedRules = 1;
      f.state.config.quality.default = "1080p";
      f.state.config.quality.defaultAway = "";
      f.state.config.quality.profiles = [
        ...structuredClone(f.state.config.quality.profiles ?? []),
        {
          ...structuredClone(f.state.config.quality.profiles![0]!),
          name: "Anime Movies",
        },
        {
          ...structuredClone(f.state.config.quality.profiles![0]!),
          name: "Anime Episodes",
        },
      ];
      f.state.config.quality.rules = [
        {
          id: "r_shield",
          name: "Living room in 4K",
          devices: [{ client: "client-shield-lr", label: "Living room Shield" }],
          profile: "4K",
        },
        {
          id: "r_anime_m",
          libraries: [4],
          kind: "movie",
          profile: "Anime Movies",
        },
        {
          id: "r_anime_e",
          libraries: [4],
          kind: "episode",
          profile: "Anime Episodes",
        },
        {
          id: "r_mobile",
          devices: [{ group: "mobile", label: "Phones & tablets" }],
          profile: "",
          maxHeight: 1080,
        },
        {
          id: "r_away",
          where: "away",
          profile: "",
          maxBitrateKbps: 8000,
        },
      ];
    });

    const globalBox = await screen.findByTestId("global-exceptions", {}, WAIT);
    expect(within(globalBox).getByText(/Living room in 4K → 4K/)).toBeInTheDocument();
    expect(within(globalBox).getByText(/Phones & tablets → ≤ 1080p/)).toBeInTheDocument();

    // Mixed library splits into Movies and Episodes rows with distinct profiles.
    expect(screen.getAllByText("Anime Movies").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Anime Episodes").length).toBeGreaterThan(0);
    // Rule 2 names library 4 (Weekend movies) and sits above the fallback that
    // decides for Movies (id 1), so a title in both libraries gets Rule 2's profile.
    expect(
      screen
        .getAllByTestId("cell-also-in")
        .some(
          (el) =>
            el.textContent ===
            "A title that is also in Weekend movies: Rule 2 wins, so it gets Anime Movies.",
        ),
    ).toBe(true);
    // The result comes first, then the profile and where it came from.
    expect(screen.getAllByTestId("cell-result")[0]).toHaveTextContent("Up to 1080p");
    expect(screen.getAllByTestId("cell-profile")[0]).toHaveTextContent(
      "Profile:1080p·Everyone else, at home",
    );

    // Plugin library row shows "Releases chosen by Demo books" and limits when away.
    expect(screen.getAllByText("Releases chosen by Demo books")[0]).toBeInTheDocument();
    expect(screen.getAllByText("≤ 8 Mbps").length).toBeGreaterThan(0);

    // Filter by device: Phones & tablets -> exceptions disappear and ≤ 1080p applies in cells.
    fireEvent.click(screen.getByRole("button", { name: "Filter by device" }));
    fireEvent.click(await screen.findByRole("option", { name: "Phones & tablets" }, WAIT));
    await settleUi(() =>
      expect(calls(fake, "GET", "/quality/summary").at(-1)?.path).toContain(
        "device=group%3Amobile",
      ),
    );
    await settleUi(() => {
      expect(screen.queryByTestId("global-exceptions")).not.toBeInTheDocument();
      expect(screen.getAllByText("Up to 1080p").length).toBeGreaterThan(0);
      expect(
        screen
          .getAllByTestId("cell-limits")
          .some((el) => el.textContent === "Limit fromRule 4, 1080p"),
      ).toBe(true);
    });
    // A kind of device has no single play to check.
    expect(
      screen.getByText(
        "Phones & tablets covers several devices. Choose one device or app above to check a play.",
      ),
    ).toBeInTheDocument();

    // The fallback row named in a cell opens in the rule drawer, in place.
    fireEvent.click(screen.getAllByRole("button", { name: "Everyone else, at home" })[0]!);
    const drawer = await screen.findByRole("dialog", { name: "Everyone else, at home" }, WAIT);
    expect(
      within(drawer).getByText("Applies to everything no rule above caught, at home."),
    ).toBeInTheDocument();
  });

  it("traces one play for the chosen viewer and device, at home, away and in a library", async () => {
    const fake = await renderWhoGetsWhat((f) =>
      f.fetch("/ether/api/safety/policies/2", {
        method: "PUT",
        body: JSON.stringify({ maxBitrateKbps: 4000, remoteDisabled: true }),
      }),
    );
    await settleUi(() => expect(headline()).toBe("1080pSDR"));

    fireEvent.click(screen.getByRole("button", { name: "Filter by viewer" }));
    fireEvent.click(await screen.findByRole("option", { name: "Sam Rivera" }, WAIT));
    fireEvent.click(screen.getByRole("button", { name: "Filter by device" }));
    fireEvent.click(await screen.findByRole("option", { name: /Pixel 8/ }, WAIT));
    await settleUi(() => expect(headline()).toBe("1080pSDRUp to 4 Mbps"));

    fireEvent.click(screen.getByRole("radio", { name: "Away" }));
    await settleUi(() => expect(headline()).toBe("Can't play away from home"));
    expect(screen.getByText("Away playback disabled")).toBeInTheDocument();
    expect(calls(fake, "POST", "/quality/explain").at(-1)?.body).toEqual({
      userId: 2,
      clientId: "client-pixel-8",
      kind: "movie",
      remote: true,
    });

    fireEvent.click(screen.getByRole("radio", { name: "At home" }));
    fireEvent.click(screen.getByRole("button", { name: "Library" }));
    fireEvent.click(await screen.findByRole("option", { name: "TV Shows" }, WAIT));
    await settleUi(() =>
      expect(calls(fake, "POST", "/quality/explain").at(-1)?.body).toEqual({
        userId: 2,
        clientId: "client-pixel-8",
        libraryId: 2,
        remote: false,
      }),
    );
    await settleUi(() => expect(screen.getByTestId("quality-media")).toHaveTextContent("Episodes"));
  });

  it("names another library's rule only when it changes what a shared title gets", () => {
    const resolveRule = createRuleResolver({
      rules: [
        { id: "r_cap", where: "away", maxBitrateKbps: 8000 },
        { id: "r_mika", viewers: [2], profile: "Dub" },
        { id: "r_anime", libraries: [4], profile: "Anime" },
        { id: "r_drama", libraries: [5], profile: "Drama", maxBitrateKbps: 10000 },
        { id: "r_kids", libraries: [5], maxHeight: 720 },
      ],
      libraryName: (id) => (id === 4 ? "Anime" : "J-Drama"),
    });
    const cell = {
      profile: "Anime",
      source: "rule" as const,
      profileRuleId: "r_anime",
      maxHeight: 0,
      maxBitrateKbps: 8000,
      stereo: false,
      limitRuleIds: ["r_cap"],
      partialRuleIds: ["r_drama", "r_kids"],
    };
    // Anime's own rule sits above the J-Drama rule, whose looser cap changes
    // nothing either. Only the stricter resolution limit is worth a line.
    expect(alsoInEffects(cell, false, 1080, resolveRule)).toEqual([
      { ruleId: "r_kids", libraries: ["J-Drama"], profile: "", limits: ["≤ 720p"] },
    ]);
    // A viewer's rule above both decides first for that viewer.
    expect(
      alsoInEffects({ ...cell, profile: "Dub", profileRuleId: "r_mika" }, false, 1080, resolveRule),
    ).toEqual([{ ruleId: "r_kids", libraries: ["J-Drama"], profile: "", limits: ["≤ 720p"] }]);
    // From J-Drama's side the Anime rule above does win.
    expect(
      alsoInEffects(
        { ...cell, profile: "Drama", profileRuleId: "r_drama", partialRuleIds: ["r_anime"] },
        false,
        1080,
        resolveRule,
      ),
    ).toEqual([{ ruleId: "r_anime", libraries: ["Anime"], profile: "Anime", limits: [] }]);
    // Which limit binds: the strictest bitrate is the away cap, not the J-Drama rule's.
    expect(
      cellLimitSources({ ...cell, limitRuleIds: ["r_cap", "r_drama"] }, 1080, resolveRule, null),
    ).toEqual({
      binding: [{ ruleId: "r_cap", sets: ["8 Mbps"] }],
      looser: ["r_drama"],
      known: true,
    });
  });

  it("words every v1.3 layer the server can send", () => {
    const resolveRule = createRuleResolver({
      rules: [
        { id: "r_a", profile: "4K" },
        { id: "r_b", profile: "At home", maxHeight: 720 },
        { id: "r_c", profile: "4K" },
      ],
    });
    const answer: QualityAnswer = {
      profile: "At home",
      kind: "movie",
      profileSource: "rule",
      profileRuleId: "r_b",
      limitRuleIds: ["r_b"],
      maxHeight: 720,
      hdr: true,
      dolbyVision: false,
      bitrateKbps: 0,
      blocked: false,
      layers: [
        {
          kind: "profile",
          reason: "",
          profile: "At home",
          source: "rule",
          ruleId: "r_b",
          maxHeight: 2160,
          decisive: false,
        },
        {
          kind: "device",
          reason: "",
          maxHeight: 2160,
          hdr: true,
          dolbyVision: false,
          edited: true,
          decisive: false,
        },
        { kind: "rule-limit", reason: "", ruleId: "r_b", maxHeight: 720, decisive: true },
        { kind: "rule-limit", reason: "", bitrateKbps: 8000, stereo: true, decisive: true },
      ],
    };
    expect(answerHeadline(answer)).toBe("720p · HDR on");
    expect(answer.layers.map((l) => layerText(l, "Sam", resolveRule))).toEqual([
      "Rule 2 · At home",
      "No limit",
      "Rule 2 · up to 720p",
      "8 Mbps · Stereo",
    ]);
    expect(
      layerText(
        { kind: "rule-limit", reason: "", ruleId: "r_a", ignored: true, decisive: false },
        "Sam",
        resolveRule,
      ),
    ).toBe("Device resolution is fixed");
    expect(
      layerText(
        { kind: "android-lift", reason: "", ruleId: "r_c", maxHeight: 2160, decisive: false },
        "Sam",
        resolveRule,
      ),
    ).toBe("Rule 3 lifts the device limit");
    expect(
      answerHeadline({
        ...answer,
        maxHeight: 0,
        hdr: false,
        bitrateKbps: 12500,
        stereo: true,
        layers: [],
      }),
    ).toBe("Any resolution · HDR off · Up to 12.5 Mbps · Stereo");
  });
});
