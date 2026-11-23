import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { LayoutPage } from "../lib/contract";
import type { LibraryTab } from "../pages/libraries/api";
import { calls, renderApp, settleUi } from "./harness";

const WAIT = { timeout: 15000 };

function lastPutTabs(fake: Parameters<typeof calls>[0], id: number): LibraryTab[] {
  const body = calls(fake, "PUT", `/libraries/${id}/tabs`).at(-1)?.body as
    { tabs: LibraryTab[] } | undefined;
  return body?.tabs ?? [];
}

function lastPutLibraryLayout(
  fake: Parameters<typeof calls>[0],
  id: number,
): LayoutPage | undefined {
  const body = calls(fake, "PUT", `/layout/libraries/${id}`).at(-1)?.body as
    { page?: LayoutPage } | undefined;
  return body?.page;
}

describe("Library tabs page", () => {
  it("lives on its own top-level Tabs tab (/libraries/<id>/tabs) without shared standard-rows links", async () => {
    const first = renderApp("/libraries/2/settings");

    const nav = await screen.findByRole("tablist", { name: "Library sections" }, WAIT);
    expect(
      within(nav)
        .getAllByRole("tab")
        .map((el) => el.textContent),
    ).toEqual(["Browse", "Rules", "Tabs", "Settings"]);
    expect(screen.queryByRole("switch", { name: "Display Recommended tab" })).toBeNull();

    fireEvent.click(within(nav).getByRole("tab", { name: "Tabs" }));
    expect(
      await screen.findByRole("switch", { name: "Display Recommended tab" }, WAIT),
    ).toBeChecked();
    expect(
      screen.getByText("Tabs show in Vela. Plex, Jellyfin and Emby keep their own library tabs."),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reset to default" })).toBeDisabled();
    expect(screen.getByRole("switch", { name: "Display Library tab" })).toBeChecked();

    // Uncustomised library Recommended tab shows its own summary with no shared standard-rows copy or /home link.
    expect(screen.getByText("Personalized rows")).toBeInTheDocument();
    expect(screen.queryByText(/Uses the standard rows/i)).toBeNull();
    expect(screen.queryByRole("link", { name: /Edit standard rows/i })).toBeNull();

    first.unmount();
    // Stale ?section=library:1 query param still opens the Home-only Designer with no Page dropdown.
    renderApp("/home?section=library:1");
    expect(
      await screen.findByRole("tablist", { name: "Designer tools" }, WAIT),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Page" })).toBeNull();
  });

  it("edits Recommended rows directly inside the tab drawer (including row Settings/Content subtabs) and saves to /layout/libraries/<id>", async () => {
    const { fake } = renderApp("/libraries/1/tabs");

    await screen.findByRole("switch", { name: "Display Recommended tab" }, WAIT);
    expect(screen.getByText("Personalized rows")).toBeInTheDocument();
    expect(screen.queryByText(/Uses the standard rows/i)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Edit Recommended tab" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit Recommended" }, WAIT);

    expect(within(dialog).queryByText(/standard rows/i)).toBeNull();
    expect(
      within(dialog).queryByRole("button", { name: /Choose rows for this library/i }),
    ).toBeNull();

    const rowsSection = within(dialog).getByRole("region", { name: "Rows on this tab" });
    expect(
      within(rowsSection).getByText(
        "These rows also show on this library's Recommended page in Plex, Jellyfin and Emby.",
      ),
    ).toBeInTheDocument();
    expect(within(rowsSection).getByRole("region", { name: "Pinned rows" })).toBeInTheDocument();
    expect(within(rowsSection).getByRole("region", { name: "Available rows" })).toBeInTheDocument();
    expect(within(rowsSection).getByRole("region", { name: "Page length" })).toBeInTheDocument();
    expect(
      within(rowsSection).getByRole("switch", { name: "Don't repeat what Home shows" }),
    ).toBeChecked();

    // Relevant movie collections appear under Collections; TV-only rows do not.
    const collectionsGroup = within(rowsSection).getByRole("group", { name: "Collections" });
    expect(
      within(collectionsGroup).getByRole("listitem", { name: "Row Weeknight wind-down" }),
    ).toBeInTheDocument();
    expect(
      within(collectionsGroup).getByRole("listitem", { name: "Row Family Movie Night" }),
    ).toBeInTheDocument();
    expect(
      within(rowsSection).queryByRole("listitem", { name: "Row New Seasons of Your Shows" }),
    ).toBeNull();

    // Clicking a row opens its nested row drawer with Settings and Content subtabs,
    // defaulting to the owner viewer (Alex Rivera) rather than Typical viewer.
    const popularRow = within(rowsSection).getByRole("listitem", {
      name: "Row Popular on This Server",
    });
    fireEvent.click(within(popularRow).getByText("Popular on This Server"));
    const rowDialog = await screen.findByRole("dialog", { name: "Popular on This Server" }, WAIT);
    const rowSubtabs = within(rowDialog).getByRole("tablist", { name: "Row sections" });
    expect(
      within(rowSubtabs)
        .getAllByRole("tab")
        .map((el) => el.textContent?.replace(/\d+$/, "").trim()),
    ).toEqual(["Settings", "Content"]);
    fireEvent.click(within(rowSubtabs).getByRole("tab", { name: /Content/ }));
    expect(
      await within(rowDialog).findByText(
        /titles? in the current preview for Alex Rivera/,
        undefined,
        WAIT,
      ),
    ).toBeInTheDocument();
    expect(calls(fake, "POST", "/layout/preview").at(-1)?.body).toMatchObject({
      viewerId: 1,
      section: "library:1",
      layout: {
        global: expect.arrayContaining([
          expect.objectContaining({
            section: "library:1",
            rows: [{ row: "popular-on-server", status: "always", slot: 1 }],
          }),
        ]),
      },
    });
    fireEvent.click(within(rowDialog).getByRole("button", { name: "Done" }));
    await waitFor(
      () => expect(screen.queryByRole("dialog", { name: "Popular on This Server" })).toBeNull(),
      WAIT,
    );

    // Personal rows like New This Week also show populated Content for the selected viewer.
    const newThisWeekRow = within(rowsSection).getByRole("listitem", {
      name: "Row New This Week",
    });
    fireEvent.click(within(newThisWeekRow).getByText("New This Week"));
    const newThisWeekDialog = await screen.findByRole("dialog", { name: "New This Week" }, WAIT);
    fireEvent.click(within(newThisWeekDialog).getByRole("tab", { name: /Content/ }));
    expect(
      await within(newThisWeekDialog).findByText(
        /titles? in the current preview for Alex Rivera/,
        undefined,
        WAIT,
      ),
    ).toBeInTheDocument();
    fireEvent.click(within(newThisWeekDialog).getByRole("button", { name: "Done" }));
    await waitFor(
      () => expect(screen.queryByRole("dialog", { name: "New This Week" })).toBeNull(),
      WAIT,
    );

    // Switching "Preview as" to another viewer updates the row Content preview to that viewer.
    fireEvent.click(within(rowsSection).getByRole("button", { name: "Preview as" }));
    fireEvent.click(await screen.findByRole("option", { name: /Sam Rivera/ }, WAIT));
    await settleUi(() => {
      expect(calls(fake, "POST", "/layout/preview").at(-1)?.body).toMatchObject({
        viewerId: 2,
        section: "library:1",
      });
    });
    fireEvent.click(within(newThisWeekRow).getByText("New This Week"));
    const samDialog = await screen.findByRole("dialog", { name: "New This Week" }, WAIT);
    fireEvent.click(within(samDialog).getByRole("tab", { name: /Content/ }));
    expect(
      await within(samDialog).findByText(
        /titles? in the current preview for Sam Rivera/,
        undefined,
        WAIT,
      ),
    ).toBeInTheDocument();
    fireEvent.click(within(samDialog).getByRole("button", { name: "Done" }));
    await waitFor(
      () => expect(screen.queryByRole("dialog", { name: "New This Week" })).toBeNull(),
      WAIT,
    );

    // Hide a row and save; persists library:1 on /layout/libraries/1 without converting default tabs to owner.
    fireEvent.click(within(popularRow).getByRole("radio", { name: "Off" }));

    fireEvent.click(within(dialog).getByRole("button", { name: "Save tab" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), WAIT);

    await settleUi(() => {
      expect(calls(fake, "PUT", "/layout/libraries/1")).toHaveLength(1);
    });
    expect(calls(fake, "PUT", "/libraries/1/tabs")).toHaveLength(0);
    expect(lastPutLibraryLayout(fake, 1)?.rows).toEqual([
      { row: "popular-on-server", status: "off" },
    ]);
    expect(await screen.findByText("Personalized rows · Custom rows")).toBeInTheDocument();
    expect(screen.getByText("Default")).toBeInTheDocument();
  });

  it("opens a TV library with customised Recommended rows and resets back to default rows via DELETE /layout/libraries/<id>", async () => {
    const { fake } = renderApp("/libraries/7/tabs?editTab=recommended");

    const dialog = await screen.findByRole("dialog", { name: "Edit Recommended" }, WAIT);
    const rowsSection = within(dialog).getByRole("region", { name: "Rows on this tab" });

    // Seeded pinned TV rows are present; movie-only collections are filtered out.
    const pinned = within(rowsSection).getByRole("region", { name: "Pinned rows" });
    expect(within(pinned).getByText("Finish What You Started")).toBeInTheDocument();
    expect(within(pinned).getByText("New Seasons of Your Shows")).toBeInTheDocument();
    const collectionsGroup = within(rowsSection).getByRole("group", { name: "Collections" });
    expect(
      within(collectionsGroup).queryByRole("listitem", { name: "Row Family Movie Night" }),
    ).toBeNull();
    expect(
      within(collectionsGroup).getByRole("listitem", { name: "Row Recently Added" }),
    ).toBeInTheDocument();

    fireEvent.click(within(rowsSection).getByRole("button", { name: "Reset to default rows" }));
    expect(within(rowsSection).queryByRole("button", { name: "Reset to default rows" })).toBeNull();

    fireEvent.click(within(dialog).getByRole("button", { name: "Save tab" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), WAIT);

    await settleUi(() => {
      expect(calls(fake, "DELETE", "/layout/libraries/7")).toHaveLength(1);
    });
    expect(await screen.findByText("Personalized rows")).toBeInTheDocument();
  });

  it("edits a plugin library's own Recommended rows and resets plugin default tabs independently", async () => {
    const { fake } = renderApp("/libraries/3/tabs");

    expect(await screen.findByRole("switch", { name: "Display Subjects tab" }, WAIT)).toBeChecked();
    expect(screen.getByText("Plugin default")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reset to plugin tabs" })).toBeDisabled();
    expect(screen.getByRole("switch", { name: "Display Authors tab" })).toBeChecked();

    // Edit Recommended rows on the Books plugin library.
    fireEvent.click(screen.getByRole("button", { name: "Edit Recommended tab" }));
    const recDialog = await screen.findByRole("dialog", { name: "Edit Recommended" }, WAIT);
    const rowsSection = within(recDialog).getByRole("region", { name: "Rows on this tab" });
    expect(
      await within(rowsSection).findByRole("listitem", { name: "Row Recently Added Books" }, WAIT),
    ).toBeInTheDocument();
    expect(
      within(rowsSection).getByRole("listitem", { name: "Row Top Rated Books" }),
    ).toBeInTheDocument();
    expect(
      within(rowsSection).getByRole("listitem", { name: "Row Featured Subjects" }),
    ).toBeInTheDocument();
    // Movie/show engine rows do not leak into a plugin library's Recommended editor.
    expect(
      within(rowsSection).queryByRole("listitem", { name: "Row Popular on This Server" }),
    ).toBeNull();

    const topRatedBooks = within(rowsSection).getByRole("listitem", {
      name: "Row Top Rated Books",
    });
    fireEvent.click(within(topRatedBooks).getByRole("radio", { name: "Always" }));
    fireEvent.click(within(recDialog).getByRole("button", { name: "Save tab" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), WAIT);

    await settleUi(() => {
      expect(calls(fake, "PUT", "/layout/libraries/3")).toHaveLength(1);
    });
    expect(lastPutLibraryLayout(fake, 3)?.rows).toEqual([
      { row: "plugin.3.top-rated", status: "always" },
    ]);

    fireEvent.click(screen.getByRole("button", { name: "Delete Authors tab" }));
    await settleUi(() => {
      expect(lastPutTabs(fake, 3).map((t) => t.key)).toEqual([
        "recommended",
        "library",
        "subjects",
      ]);
    });
    expect(await screen.findByText("Customized")).toBeInTheDocument();

    const resetBtn = screen.getByRole("button", { name: "Reset to plugin tabs" });
    expect(resetBtn).not.toBeDisabled();
    fireEvent.click(resetBtn);
    await settleUi(() => {
      expect(calls(fake, "DELETE", "/libraries/3/tabs")).toHaveLength(1);
    });
    expect(await screen.findByRole("switch", { name: "Display Authors tab" })).toBeChecked();
    expect(screen.getByText("Plugin default")).toBeInTheDocument();
  });

  it("prevents hiding or deleting the last visible tab and restores standard tabs on reset", async () => {
    const { fake } = renderApp("/libraries/4/tabs");

    const lastSwitch = await screen.findByRole(
      "switch",
      { name: "Display Weekend Picks tab" },
      WAIT,
    );
    expect(lastSwitch).toBeChecked();
    expect(lastSwitch).toBeDisabled();
    expect(screen.getByRole("button", { name: "Delete Weekend Picks tab" })).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Reset to default" }));
    await settleUi(() => {
      expect(calls(fake, "DELETE", "/libraries/4/tabs")).toHaveLength(1);
    });
    expect(await screen.findByRole("switch", { name: "Display Recommended tab" })).toBeChecked();
    expect(screen.getByRole("switch", { name: "Display Library tab" })).toBeChecked();
  });

  it("adds Collections (including Owner collections), People, and Saved view tabs via /tabs/preview, toggles visibility, and saves", async () => {
    const { fake } = renderApp("/libraries/1/tabs");
    await screen.findByRole("switch", { name: "Display Library tab" }, WAIT);

    // 1. Add a "Collections by…" tab grouped by Decades, ordered Name A–Z
    fireEvent.click(screen.getByRole("button", { name: "Add tab" }));
    const picker1 = await screen.findByRole("dialog", { name: "Add tab" }, WAIT);
    expect(within(picker1).getByText("Already in tabs")).toBeInTheDocument();
    fireEvent.click(within(picker1).getByRole("button", { name: /Collections by…/ }));

    const colDialog = await screen.findByRole("dialog", { name: "New Collections by… tab" }, WAIT);
    // Verify Owner collections omits Bucket order & Sort, then switch to Decades
    fireEvent.click(within(colDialog).getByRole("radio", { name: "Owner collections" }));
    expect(within(colDialog).getByLabelText("Tab label")).toHaveValue("Collections");
    expect(within(colDialog).queryByRole("radiogroup", { name: "Bucket order" })).toBeNull();

    fireEvent.click(within(colDialog).getByRole("radio", { name: "Decades" }));
    expect(within(colDialog).getByLabelText("Tab label")).toHaveValue("Decades");
    const bucketPreview = await within(colDialog).findByRole(
      "status",
      { name: "Live preview" },
      WAIT,
    );
    await settleUi(() => {
      expect(bucketPreview).toHaveTextContent(/\d+ collections? in Movies/);
    });
    expect(calls(fake, "POST", "/libraries/1/tabs/preview").length).toBeGreaterThan(0);

    fireEvent.click(
      within(within(colDialog).getByRole("radiogroup", { name: "Bucket order" })).getByRole(
        "radio",
        { name: "Name A–Z" },
      ),
    );
    fireEvent.click(within(colDialog).getByRole("button", { name: "Add tab" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), WAIT);

    await settleUi(() => {
      expect(lastPutTabs(fake, 1)).toContainEqual(
        expect.objectContaining({
          key: "decades",
          label: "Decades",
          kind: "collections",
          facet: "decade",
          order: "name",
        }),
      );
    });

    // 2. Add a "People in…" tab grouped by Directors, ordered by Most titles
    fireEvent.click(screen.getByRole("button", { name: "Add tab" }));
    const picker2 = await screen.findByRole("dialog", { name: "Add tab" }, WAIT);
    fireEvent.click(within(picker2).getByRole("button", { name: /People in…/ }));

    const peopleDialog = await screen.findByRole("dialog", { name: "New People in… tab" }, WAIT);
    fireEvent.click(within(peopleDialog).getByRole("radio", { name: "Directors" }));
    expect(within(peopleDialog).getByLabelText("Tab label")).toHaveValue("Directors");
    const peoplePreview = await within(peopleDialog).findByRole(
      "status",
      { name: "Live preview" },
      WAIT,
    );
    await settleUi(() => {
      expect(peoplePreview).toHaveTextContent(/\d+ (person|people) in Movies/);
    });
    fireEvent.click(
      within(within(peopleDialog).getByRole("radiogroup", { name: "People order" })).getByRole(
        "radio",
        { name: "Most titles" },
      ),
    );
    fireEvent.click(within(peopleDialog).getByRole("button", { name: "Add tab" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), WAIT);

    await settleUi(() => {
      expect(lastPutTabs(fake, 1)).toContainEqual(
        expect.objectContaining({
          key: "directors",
          label: "Directors",
          kind: "people",
          facet: "director",
          order: "count",
        }),
      );
    });

    // 3. Add a "Saved view" tab with rules + live matching title count + custom search placeholder
    fireEvent.click(screen.getByRole("button", { name: "Add tab" }));
    const picker3 = await screen.findByRole("dialog", { name: "Add tab" }, WAIT);
    fireEvent.click(within(picker3).getByRole("button", { name: /Saved view/ }));

    const viewDialog = await screen.findByRole("dialog", { name: "New Saved view tab" }, WAIT);
    expect(within(viewDialog).getByLabelText("Tab label")).toHaveValue("Unwatched");
    const rulesPreview = await within(viewDialog).findByRole(
      "status",
      { name: "Matching titles preview" },
      WAIT,
    );
    await settleUi(() => {
      expect(rulesPreview).toHaveTextContent(/\d+ of \d+ titles match/);
    });
    // Choose Timeline layout and Recently watched sort on the Saved view tab
    fireEvent.click(
      within(within(viewDialog).getByRole("radiogroup", { name: "Layout" })).getByRole("radio", {
        name: "Timeline",
      }),
    );
    fireEvent.click(within(viewDialog).getByRole("button", { name: "Default sort" }));
    fireEvent.click(await screen.findByRole("option", { name: "Recently watched" }, WAIT));
    fireEvent.change(within(viewDialog).getByLabelText("Search placeholder"), {
      target: { value: "Find unwatched movies…" },
    });
    fireEvent.click(within(viewDialog).getByRole("button", { name: "Add tab" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), WAIT);

    await settleUi(() => {
      expect(lastPutTabs(fake, 1)).toContainEqual(
        expect.objectContaining({
          key: "unwatched",
          label: "Unwatched",
          kind: "view",
          layout: "timeline",
          sort: "lastViewed",
          direction: "desc",
          search: true,
          searchPlaceholder: "Find unwatched movies…",
        }),
      );
    });

    // 4. Hide the Decades tab via its inline switch
    fireEvent.click(screen.getByRole("switch", { name: "Display Decades tab" }));
    await settleUi(() => {
      expect(lastPutTabs(fake, 1)).toContainEqual(
        expect.objectContaining({
          key: "decades",
          hidden: true,
        }),
      );
    });
    expect(await screen.findByText("Hidden")).toBeInTheDocument();
  });
});
