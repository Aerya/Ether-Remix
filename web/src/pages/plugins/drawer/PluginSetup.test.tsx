import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { PluginDetail } from "../types";
import { PluginSetup } from "./PluginSetup";

const apiFetch = vi.hoisted(() => vi.fn());
vi.mock("../../../lib/api", () => ({
  ApiError: class ApiError extends Error {},
  apiFetch,
  errorMessage: String,
  isAbort: (e: unknown) => e instanceof DOMException && e.name === "AbortError",
}));
vi.mock("../../../i18n/react", () => ({ useLocale: () => ({ locale: "en" }) }));
vi.mock("../../../components/pickers", () => ({ useViewers: () => ({ viewers: [] }) }));

afterEach(() => {
  cleanup();
  apiFetch.mockReset();
});

const detail: PluginDetail = {
  id: "sports",
  name: "Sports",
  version: "1.0.0",
  enabled: true,
  verified: true,
  shadow: false,
  autoUpdate: false,
  health: "ok",
  installedAt: 0,
  manifest: { id: "sports", version: "1.0.0", name: "Sports" },
  permissions: {
    lines: [],
    declared: {},
    grants: {},
    needsViewers: false,
    needsFolders: false,
  },
  healthDetail: { status: "ok", restarts: 0, slow: 0, drops: 0, egressBytes: 0 },
  privacy: { declaredHosts: [], observedHosts: {} },
  impact: {
    calls: 0,
    errors: 0,
    p50Ms: 0,
    p95Ms: 0,
    egressBytes: 0,
    kvBytes: 0,
    slow: 0,
    eventDrops: 0,
    restarts: 0,
  },
};

it("draws multiSelect chips from static, runtime, and search options and saves string lists", async () => {
  const schema = {
    groups: [
      {
        key: "general",
        label: "General",
        fields: [
          {
            key: "leagues",
            kind: "multiSelect",
            label: "Leagues",
            options: [
              { value: "nfl", label: "NFL" },
              { value: "nba", label: "NBA" },
            ],
          },
          {
            key: "genres",
            kind: "multiSelect",
            label: "Genres",
          },
          {
            key: "teams",
            kind: "multiSelect",
            search: true,
            label: "Followed teams",
            placeholder: "Search teams…",
          },
        ],
      },
      {
        key: "tuning",
        label: "Advanced tuning",
        advanced: true,
        fields: [{ key: "endpoint", kind: "text", label: "Custom endpoint" }],
      },
    ],
  };

  let saved: Record<string, unknown> | null = null;
  apiFetch.mockImplementation(async (path: string, init?: RequestInit) => {
    if (path === "/plugins/sports/settings?locale=en") {
      return {
        settings: saved ?? {
          leagues: ["nfl", "legacy-cup"],
          genres: ["scifi"],
          teams: ["custom-team"],
          endpoint: "",
        },
        schema,
      };
    }
    if (path === "/plugins/sports/options/genres?locale=en") {
      return { options: [{ value: "scifi", label: "Sci-Fi" }] };
    }
    if (path === "/plugins/sports/options/teams?q=bos&locale=en") {
      return { options: [{ value: "nhl:bos", label: "Boston Bruins", hint: "NHL" }] };
    }
    if (path === "/plugins/sports/settings" && init?.method === "PUT") {
      saved = (JSON.parse(String(init.body)) as { settings: Record<string, unknown> }).settings;
      return { status: "saved" };
    }
    return { options: [] };
  });

  render(
    <PluginSetup
      detail={detail}
      onDetail={async () => {}}
      onChanged={() => {}}
      onError={() => {}}
    />,
  );

  // Static options multiSelect shows chips for known options and saved unknown values.
  const leaguesBtn = await screen.findByRole("button", { name: "Leagues" });
  expect(leaguesBtn).toHaveTextContent("NFL");
  expect(leaguesBtn).toHaveTextContent("legacy-cup");

  // Runtime /options/{key} multiSelect shows loaded option labels as chips.
  const genresBtn = screen.getByRole("button", { name: "Genres" });
  expect(genresBtn).toHaveTextContent("Sci-Fi");

  // Search-backed multiSelect does not eagerly call /options/teams on load.
  expect(apiFetch).not.toHaveBeenCalledWith(expect.stringContaining("/options/teams"));

  // Pick another static option in MultiSelectRow.
  fireEvent.click(leaguesBtn);
  fireEvent.click(await screen.findByRole("option", { name: "NBA" }));
  fireEvent.keyDown(document, { key: "Escape" });

  // Search multiSelect keeps saved values as removable chips, queries ?q=…, and adds/removes chips.
  expect(screen.getByText("custom-team")).toBeInTheDocument();
  fireEvent.change(screen.getByRole("textbox", { name: "Followed teams" }), {
    target: { value: "bos" },
  });
  await waitFor(() =>
    expect(apiFetch).toHaveBeenCalledWith(
      "/plugins/sports/options/teams?q=bos&locale=en",
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    ),
  );
  fireEvent.click(await screen.findByRole("option", { name: /Boston Bruins/ }));
  expect(screen.getByRole("button", { name: "Remove Boston Bruins" })).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Remove custom-team" }));
  expect(screen.queryByText("custom-team")).toBeNull();

  // Collapsible advanced group starts collapsed and opens on click.
  expect(screen.queryByLabelText("Custom endpoint")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Advanced tuning" }));
  expect(screen.getByLabelText("Custom endpoint")).toBeInTheDocument();

  // Saved payload preserves string lists for every multiSelect field.
  fireEvent.click(screen.getByRole("button", { name: "Save settings" }));
  await waitFor(() =>
    expect(saved).toEqual({
      leagues: ["nfl", "legacy-cup", "nba"],
      genres: ["scifi"],
      teams: ["nhl:bos"],
      endpoint: "",
    }),
  );
});
