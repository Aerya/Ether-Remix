import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { LibraryTab } from "../pages/libraries/api";
import { calls, renderApp, settleUi } from "./harness";

const WAIT = { timeout: 15000 };

function lastPutTabs(fake: Parameters<typeof calls>[0], id: number): LibraryTab[] {
  const body = calls(fake, "PUT", `/libraries/${id}/tabs`).at(-1)?.body as
    { tabs: LibraryTab[] } | undefined;
  return body?.tabs ?? [];
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
    expect(screen.queryByRole("switch", { name: "Display Recommendations tab" })).toBeNull();

    fireEvent.click(within(nav).getByRole("tab", { name: "Tabs" }));
    expect(
      await screen.findByRole("switch", { name: "Display Recommendations tab" }, WAIT),
    ).toBeChecked();
    expect(
      screen.getByText(
        "Vela shows these as tabs. Plex, Jellyfin, Emby and Silo show the first rows tab as the library's Recommended page.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reset to default" })).toBeDisabled();
    expect(screen.getByRole("switch", { name: "Display Library tab" })).toBeChecked();

    // The rows tab's rows live in the Recommendations designer.
    expect(screen.getByText("Rows arranged in Recommendations")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Edit rows on Recommendations" })).toHaveAttribute(
      "href",
      "/recommendations?page=library%3A2%2Frecommended",
    );
    first.unmount();
  });

  it("opens the Recommendations designer from the rows tab instead of editing rows in the drawer", async () => {
    const { fake } = renderApp("/libraries/1/tabs");

    await screen.findByRole("switch", { name: "Display Recommendations tab" }, WAIT);
    fireEvent.click(screen.getByRole("button", { name: "Edit Recommendations tab" }));
    const dialog = await screen.findByRole("dialog", { name: "Edit Recommendations" }, WAIT);

    const rowsSection = within(dialog).getByRole("region", { name: "Rows on this tab" });
    expect(within(rowsSection).getByRole("link", { name: /Edit rows/ })).toHaveAttribute(
      "href",
      "/recommendations?page=library%3A1%2Frecommended",
    );
    expect(within(rowsSection).queryByRole("region", { name: "Pinned rows" })).toBeNull();
    expect(within(rowsSection).queryByRole("region", { name: "Available rows" })).toBeNull();

    fireEvent.click(within(dialog).getByRole("button", { name: "Save tab" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), WAIT);
    expect(calls(fake, "PUT", "/libraries/1/tabs")).toHaveLength(0);
  });

  it("deletes and resets a plugin library's own tabs", async () => {
    const { fake } = renderApp("/libraries/3/tabs");

    expect(await screen.findByRole("switch", { name: "Display Subjects tab" }, WAIT)).toBeChecked();
    expect(screen.getByText("Plugin default")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reset to plugin tabs" })).toBeDisabled();
    expect(screen.getByRole("switch", { name: "Display Authors tab" })).toBeChecked();

    expect(screen.getByRole("link", { name: "Edit rows on Recommendations" })).toHaveAttribute(
      "href",
      "/recommendations?page=library%3A3%2Frecommended",
    );

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
    expect(
      await screen.findByRole("switch", { name: "Display Recommendations tab" }),
    ).toBeChecked();
    expect(screen.getByRole("switch", { name: "Display Library tab" })).toBeChecked();
  });

  it("adds Collections (including Your collections), People, and Saved view tabs via /tabs/preview, toggles visibility, and saves", async () => {
    const { fake } = renderApp("/libraries/1/tabs");
    await screen.findByRole("switch", { name: "Display Library tab" }, WAIT);

    // 1. Add a "Collections by…" tab grouped by Decades, ordered Name A–Z
    fireEvent.click(screen.getByRole("button", { name: "Add tab" }));
    const picker1 = await screen.findByRole("dialog", { name: "Add tab" }, WAIT);
    expect(within(picker1).getByText("Already in tabs")).toBeInTheDocument();
    fireEvent.click(within(picker1).getByRole("button", { name: /Collections by…/ }));

    const colDialog = await screen.findByRole("dialog", { name: "New Collections by… tab" }, WAIT);
    // Verify Your collections omits Bucket order & Sort, then switch to Decades
    fireEvent.click(within(colDialog).getByRole("radio", { name: "Your collections" }));
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
