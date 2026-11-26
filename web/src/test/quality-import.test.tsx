import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { calls, renderApp } from "./harness";

const WAIT = { timeout: 4000 };

/** Saves only, not previews. */
const imports = (fake: Parameters<typeof calls>[0]) =>
  calls(fake, "POST", "/releases/quality-import").filter(
    (c) => c.path === "/releases/quality-import",
  );

async function openAddProfile() {
  fireEvent.click(await screen.findByRole("button", { name: "Add profile" }, WAIT));
  return screen.findByRole("dialog", { name: "Add profile" }, WAIT);
}

describe("Unified Add profile drawer", () => {
  it("starts from the four clear options and adds an Ether preset", async () => {
    const { fake } = renderApp("/releases/profiles");
    const drawer = await openAddProfile();

    expect(within(drawer).getByText("Start from")).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: /Ether preset/ })).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: /TRaSH Guides/ })).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: /Profilarr database/ })).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: /Blank/ })).toBeInTheDocument();
    expect(
      within(drawer).getByRole("button", { name: /Paste or upload a profile file/ }),
    ).toBeInTheDocument();

    fireEvent.click(within(drawer).getByRole("button", { name: /Ether preset/ }));
    fireEvent.click(await within(drawer).findByRole("radio", { name: /^720p / }, WAIT));
    await waitFor(() => expect(within(drawer).getByLabelText("Name")).toHaveValue("720p"), WAIT);

    fireEvent.click(within(drawer).getByRole("button", { name: "Add profile" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument(), WAIT);

    const saved = fake.state.config.quality.profiles!.find((p) => p.name === "720p")!;
    expect(saved).toBeDefined();
    expect(saved.maxHeight).toBe(720);
    expect(saved.import).toBeUndefined();
    expect(await screen.findByText("Use it in a rule →", {}, WAIT)).toBeInTheDocument();
  });

  it("previews a TRaSH profile, shows own/default format counts, and adds it", async () => {
    const { fake } = renderApp("/releases/profiles");
    const drawer = await openAddProfile();
    const add = within(drawer).getByRole("button", { name: "Add profile" });
    expect(add).toBeDisabled();

    fireEvent.click(within(drawer).getByRole("button", { name: /TRaSH Guides/ }));
    const sqpCard = await within(drawer).findByRole(
      "radio",
      { name: /\[SQP\] SQP-1 \(2160p\)/ },
      WAIT,
    );
    expect(sqpCard).toHaveTextContent("3 own · 32 default");
    fireEvent.click(sqpCard);

    const table = await within(drawer).findByRole("table", { name: "Custom formats" }, WAIT);
    const rows = within(table).getAllByRole("row");
    expect(rows[1]).toHaveTextContent("WEB Tier 01");
    expect(rows[1]).toHaveTextContent("+1,800");
    expect(rows[1]).toHaveTextContent("Added");
    expect(within(drawer).getByText(/Scored with TRaSH's sqp-1-2160p scores/)).toBeInTheDocument();
    expect(within(drawer).getByText("Blu-ray 4K")).toBeInTheDocument();
    expect(within(drawer).getByText("Bluray-2160p › WEB 2160p")).toBeInTheDocument();
    expect(within(drawer).getByText(/Left out SDTV/)).toBeInTheDocument();
    expect(within(drawer).getByText("Minimum score 1,000")).toBeInTheDocument();

    // "4K" is taken by an existing profile in the fake; rename before adding.
    fireEvent.change(within(drawer).getByLabelText("Name"), { target: { value: "4K" } });
    fireEvent.click(add);
    expect(
      await within(drawer).findByText(/A profile named "4K" already exists/, {}, WAIT),
    ).toBeInTheDocument();

    fireEvent.change(within(drawer).getByLabelText("Name"), { target: { value: "UHD (TRaSH)" } });
    fireEvent.click(add);
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument(), WAIT);
    expect(imports(fake).at(-1)!.body).toEqual({
      app: "radarr",
      profile: "sqp-1-2160p",
      name: "UHD (TRaSH)",
    });
    expect(await screen.findByText("UHD (TRaSH)", {}, WAIT)).toBeInTheDocument();
    const saved = fake.state.config.quality.profiles!.find((p) => p.name === "UHD (TRaSH)")!;
    expect(saved.customFormatScores).toMatchObject({ c20f169ef63c5f40c2def54abaf4438e: 1800 });
    expect(saved.minCustomFormatScore).toBe(1000);
    expect(saved.import?.noSync).toBeUndefined();
  });

  it("warns and permits a profile with an unreachable preferred score", async () => {
    const { fake } = renderApp("/releases/profiles");
    const drawer = await openAddProfile();
    fireEvent.click(within(drawer).getByRole("button", { name: /TRaSH Guides/ }));
    fireEvent.click(
      await within(drawer).findByRole("radio", { name: /Needs a perfect release/ }, WAIT),
    );
    expect(await within(drawer).findByText(/prefers a score of 100/, {}, WAIT)).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: "Add profile" })).toBeEnabled();
    expect(imports(fake)).toHaveLength(0);
  });

  it("checks and saves a custom Profilarr database and supports sync:false (noSync)", async () => {
    const { fake } = renderApp("/releases/profiles");
    const drawer = await openAddProfile();
    fireEvent.click(within(drawer).getByRole("button", { name: /Profilarr database/ }));

    // Open Database picker and choose Add a database…
    fireEvent.click(await within(drawer).findByRole("button", { name: "Database" }, WAIT));
    fireEvent.click(await screen.findByRole("option", { name: "Add a database…" }, WAIT));

    fireEvent.change(within(drawer).getByLabelText("GitHub repository"), {
      target: { value: "https://github.com/kaito/database.git/" },
    });
    fireEvent.click(within(drawer).getByRole("button", { name: "Check and save" }));

    // Saved to quality.databases and switched to the custom database's profiles
    await waitFor(
      () =>
        expect(fake.state.config.quality.databases).toEqual([
          {
            id: "kaito-database",
            name: "database",
            repo: "https://github.com/kaito/database",
            branch: "v2",
          },
        ]),
      WAIT,
    );
    expect(await within(drawer).findByText(/3 profiles found/, {}, WAIT)).toBeInTheDocument();

    // Switch For toggle to Episodes (Sonarr) and pick the custom fork profile
    fireEvent.click(within(drawer).getByRole("radio", { name: "Episodes (Sonarr)" }));
    fireEvent.click(await within(drawer).findByRole("radio", { name: /1080p Anime Fork/ }, WAIT));

    // Toggle "Keep in sync" off -> sends sync: false while keeping import provenance
    const syncSwitch = await within(drawer).findByRole("switch", { name: "Keep in sync" }, WAIT);
    expect(syncSwitch).toBeChecked();
    fireEvent.click(syncSwitch);
    expect(syncSwitch).not.toBeChecked();

    fireEvent.click(within(drawer).getByRole("button", { name: "Add profile" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument(), WAIT);

    expect(imports(fake).at(-1)!.body).toEqual({
      app: "sonarr",
      source: "dictionarry",
      database: "kaito-database",
      profile: "1080p-anime-fork",
      sync: false,
      name: "1080p Anime Fork",
    });
    const saved = fake.state.config.quality.profiles!.find((p) => p.name === "1080p Anime Fork")!;
    expect(saved.import).toMatchObject({
      source: "dictionarry",
      app: "sonarr",
      profile: "1080p-anime-fork",
      database: "kaito-database",
      noSync: true,
    });
  });

  it("previews pasted profile JSON for the app its qualities name", async () => {
    const { fake } = renderApp("/releases/profiles");
    const drawer = await openAddProfile();
    fireEvent.click(within(drawer).getByRole("button", { name: /Paste or upload a profile file/ }));
    const preview = within(drawer).getByRole("button", { name: "Preview" });
    expect(preview).toBeDisabled();

    fireEvent.change(within(drawer).getByLabelText("Profile JSON"), { target: { value: "{" } });
    fireEvent.click(preview);
    expect(
      await within(drawer).findByText("Not valid profile JSON.", {}, WAIT),
    ).toBeInTheDocument();

    const json = JSON.stringify({
      name: "My remuxes",
      items: [{ name: "Bluray-1080p Remux", allowed: true }],
      formatItems: { "BR-DISK": "85c61753df5da1fb2aab6f2a47426b09", Mystery: "nope" },
    });
    fireEvent.change(within(drawer).getByLabelText("Profile JSON"), { target: { value: json } });
    fireEvent.click(within(drawer).getByRole("button", { name: "Preview" }));
    const table = await within(drawer).findByRole("table", { name: "Custom formats" }, WAIT);
    expect(within(table).getByText("Not found")).toBeInTheDocument();
    expect(within(drawer).getByText("Remux 1080p")).toBeInTheDocument();
    // Pasted JSON has no upstream sync toggle
    expect(within(drawer).queryByRole("switch", { name: "Keep in sync" })).not.toBeInTheDocument();

    fireEvent.click(within(drawer).getByRole("button", { name: "Add profile" }));
    await waitFor(() => expect(imports(fake)).toHaveLength(1), WAIT);
    expect(imports(fake)[0]!.body).toEqual({
      app: "sonarr",
      json,
      name: "My remuxes",
    });
    expect(await screen.findByText("My remuxes", {}, WAIT)).toBeInTheDocument();
  });

  it("adds a blank profile", async () => {
    const { fake } = renderApp("/releases/profiles");
    const drawer = await openAddProfile();
    fireEvent.click(within(drawer).getByRole("button", { name: /Blank/ }));

    fireEvent.change(within(drawer).getByLabelText("Name"), { target: { value: "Scratch 1080p" } });
    fireEvent.click(within(drawer).getByRole("button", { name: "Add profile" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument(), WAIT);

    expect(fake.state.config.quality.profiles!.some((p) => p.name === "Scratch 1080p")).toBe(true);
  });
});
