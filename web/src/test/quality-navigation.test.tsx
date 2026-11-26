import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { calls, renderApp } from "./harness";
import { RELEASE_SEARCH } from "../pages/settings/schema";
import { destinations } from "../lib/sitemap";
import { settingsIndex } from "../lib/settings-index";

it("gives Who gets what its own searchable destination and inline controls", async () => {
  renderApp("/releases/check");
  const result = await screen.findByTestId("quality-headline", {}, { timeout: 10000 });
  expect(result).toHaveTextContent("1080p");
  expect(screen.getAllByTestId("quality-headline")).toHaveLength(1);
  // The tabs are the only navigation: no step bar repeats them.
  expect(screen.queryByRole("navigation", { name: "How quality works" })).not.toBeInTheDocument();
  // One viewer picker and one device picker drive the table and the check below it.
  expect(screen.getAllByRole("button", { name: "Filter by viewer" })).toHaveLength(1);
  expect(screen.getAllByRole("button", { name: "Filter by device" })).toHaveLength(1);
  expect(screen.queryByRole("button", { name: "App" })).not.toBeInTheDocument();
  expect(screen.getByRole("radio", { name: "Away" })).toBeVisible();
  expect(destinations().some((d) => d.href === "/releases/check")).toBe(true);
  expect(settingsIndex().find((d) => d.title === "Who gets what")?.href).toBe("/releases/check");
});

it("keeps the four search settings visible", () => {
  expect(RELEASE_SEARCH[0]!.more).toBeUndefined();
  expect(RELEASE_SEARCH[0]!.fields.map((f) => f.key)).toEqual([
    "maxCandidates",
    "healthSamples",
    "searchCacheTtl",
    "unavailableCooloff",
  ]);
});

it("orders profile sections without a remote bitrate section", async () => {
  renderApp("/releases/profiles");
  await screen.findByRole("button", { name: "Add profile" }, { timeout: 10000 });
  await screen.findByText("Same-quality versions to offer", {}, { timeout: 10000 });
  expect(screen.queryByRole("navigation", { name: "How quality works" })).not.toBeInTheDocument();
  const headings = screen.getAllByRole("heading").map((h) => h.textContent);
  const order = ["Profiles", "Which source goes first", "Version menu"].map((s) =>
    headings.indexOf(s),
  );
  expect(order.every((i) => i >= 0)).toBe(true);
  expect(order).toEqual([...order].sort((a, b) => a - b));
  expect(screen.queryByText("Bitrate limit")).not.toBeInTheDocument();
  expect(screen.queryByTestId("quality-headline")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "Import a preset quality profile" }),
  ).not.toBeInTheDocument();
});

it("shows all profiles together without a media switcher and lists where each is used", async () => {
  renderApp("/releases/profiles", {}, (fake) => {
    const profiles = fake.state.config.quality.profiles!;
    profiles.push({ ...structuredClone(profiles[1]!), name: "Anime" });
  });
  await screen.findByRole("button", { name: "Edit 4K" }, { timeout: 10000 });
  expect(screen.getByRole("button", { name: "Edit 1080p" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Edit Anime" })).toBeInTheDocument();
  expect(screen.queryByRole("radiogroup", { name: "Applies to" })).not.toBeInTheDocument();

  const row = (name: string) => screen.getByRole("button", { name: `Edit ${name}` }).closest("li")!;
  expect(within(row("4K")).getByText("Rule 1: Living room Apple TV")).toBeInTheDocument();
  expect(within(row("1080p")).getByText("Everyone else, at home and away")).toBeInTheDocument();
  expect(within(row("Anime")).getByText("Not used by any rule")).toBeInTheDocument();
});

it("collapses guide sync to one line until a profile is imported", async () => {
  renderApp("/releases/profiles");
  expect(
    await screen.findByText(
      "Applies once you import a profile from TRaSH Guides or Profilarr.",
      {},
      { timeout: 10000 },
    ),
  ).toBeInTheDocument();
  expect(
    screen.queryByRole("switch", { name: "Keep imported profiles in sync" }),
  ).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Sync now" })).not.toBeInTheDocument();
});

it("saves the automatic sync cadence as a patch and runs it", async () => {
  const { fake } = renderApp("/releases/profiles", {}, (f) => {
    f.state.config.quality.profiles![0]!.import = {
      source: "trash",
      app: "radarr",
      profile: "UHD Bluray + WEB",
    };
  });
  await screen.findByRole("switch", { name: "Keep imported profiles in sync" }, { timeout: 10000 });
  // The title appears once, in the card header with its switch.
  expect(screen.getAllByText("Keep imported profiles in sync")).toHaveLength(1);
  expect(screen.getByText("Never synced")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Check for changes" })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("switch", { name: "Keep imported profiles in sync" }));
  fireEvent.click(screen.getByRole("button", { name: "Check for changes" }));
  fireEvent.click(await screen.findByRole("option", { name: "Every 3 days" }, { timeout: 4000 }));
  fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
  await waitFor(() => expect(calls(fake, "PUT", "/config/quality")).toHaveLength(1));
  expect(calls(fake, "PUT", "/config/quality")[0]!.body).toEqual({
    sync: { enabled: true, interval: "72h" },
  });
  await waitFor(() => expect(fake.state.config.quality.sync?.interval).toBe("72h"));

  fireEvent.click(screen.getByRole("button", { name: "Sync now" }));
  await waitFor(() => expect(calls(fake, "POST", "/releases/quality/sync")).toHaveLength(1));
  expect(await screen.findByText("2 formats added")).toBeInTheDocument();
});

it("shows a sync failure without a settings save bar and clears it on retry", async () => {
  let fail = true;
  renderApp("/releases/profiles", {}, (f) => {
    f.state.config.quality.profiles![0]!.import = {
      source: "trash",
      app: "radarr",
      profile: "UHD Bluray + WEB",
    };
    const original = f.fetch;
    f.fetch = async (input, init) => {
      if (String(input).endsWith("/releases/quality/sync") && init?.method === "POST" && fail) {
        fail = false;
        return new Response(
          JSON.stringify({ error: { code: "validation", message: "Sync failed" } }),
          {
            status: 400,
          },
        );
      }
      return original(input, init);
    };
  });
  const sync = await screen.findByRole("button", { name: "Sync now" });
  fireEvent.click(sync);
  expect(await screen.findByText("Sync failed")).toBeVisible();
  expect(screen.queryByRole("region", { name: "Unsaved changes" })).not.toBeInTheDocument();
  fireEvent.click(sync);
  expect(await screen.findByText("2 formats added")).toBeVisible();
  expect(screen.queryByText("Sync failed")).not.toBeInTheDocument();
});
