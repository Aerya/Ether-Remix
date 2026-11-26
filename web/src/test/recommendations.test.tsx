import { cleanup, fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { RowPage } from "../lib/pages";
import { calls, renderApp } from "./harness";

const WAIT = { timeout: 4000 };

type PreviewBody = { page?: RowPage; viewer?: number; device: string; at: string };

function lastPreview(fake: Parameters<typeof calls>[0]): PreviewBody {
  return calls(fake, "POST", "/pages/home/preview").at(-1)!.body as PreviewBody;
}

async function home() {
  const r = renderApp("/recommendations");
  await screen.findByRole("region", { name: "#1: Continue Watching" }, WAIT);
  return r;
}

afterEach(() => localStorage.clear());

describe("Recommendations designer", () => {
  it("shows the page as the one list of rows, with placement, kind and fan-out", async () => {
    const { fake } = await home();
    const cw = screen.getByRole("region", { name: "#1: Continue Watching" });
    expect(cw).toHaveTextContent("Pinned #1");
    expect(within(cw).getByText("Personal")).toBeInTheDocument();
    const tonight = screen.getByRole("region", { name: "#2: Tonight" });
    expect(within(tonight).getByText("Only in Vela")).toBeInTheDocument();
    // Trending, one row per library, sits in one bracket.
    const block = screen.getByRole("group", { name: /^Trending · one row per library/ });
    expect(within(block).getByRole("region", { name: "#3: Trending Movies" })).toBeInTheDocument();
    expect(
      within(block).getByRole("region", { name: "#4: Trending TV Shows" }),
    ).toBeInTheDocument();
    // The saved page is previewed (no draft), for the typical viewer.
    const body = lastPreview(fake);
    expect(body.page).toBeUndefined();
    expect(body.viewer).toBeUndefined();
    expect(body.device).toBe("tv");
    // Folded sections close the page.
    expect(screen.getByRole("button", { name: /Not shown right now · 3/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /More rows you can add · 3/ })).toBeInTheDocument();
    // No row list outside the page.
    expect(screen.queryByRole("tab", { name: "Overrides" })).toBeNull();
    expect(screen.getByRole("heading", { name: "Recommendations" })).toBeInTheDocument();
  });

  it("adds a row in place: + at #3, pick a source, the rows appear in the draft, then save", async () => {
    const { fake } = await home();
    fireEvent.click(screen.getByRole("button", { name: "Add a row at position 3" }));
    const drawer = await screen.findByRole("dialog", {}, WAIT);
    expect(within(drawer).getByText("What fills this row?")).toBeInTheDocument();
    fireEvent.click(await within(drawer).findByRole("button", { name: "Top rated" }, WAIT));
    expect(
      await within(drawer).findByRole("region", { name: "Libraries" }, WAIT),
    ).toBeInTheDocument();
    // The new rows preview in place, outlined as new, before anything is added.
    await waitFor(
      () =>
        expect(screen.getByRole("region", { name: "#3: Top rated Movies" })).toBeInTheDocument(),
      WAIT,
    );
    expect(
      within(screen.getByRole("region", { name: "#3: Top rated Movies" })).getByText(
        "New · in the draft",
      ),
    ).toBeInTheDocument();
    const previewed = lastPreview(fake).page!;
    const added = previewed.rows.find((r) => r.source.kind === "top-rated" && !r.name)!;
    expect(added.placement).toMatchObject({ mode: "pinned", position: 3 });
    expect(added.scope.perLibrary).toBe(true);
    expect(added.id).toMatch(/^r[a-z0-9]{8}$/);

    fireEvent.click(within(drawer).getByRole("button", { name: "Add to page" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull(), WAIT);
    expect(screen.getByText("Unsaved changes · kept if you leave")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Save page/ }));
    await waitFor(() => expect(calls(fake, "PUT", "/pages/home")).toHaveLength(1), WAIT);
    const put = calls(fake, "PUT", "/pages/home")[0]!.body as RowPage;
    expect(put.rows.some((r) => r.id === added.id)).toBe(true);
    await waitFor(
      () => expect(screen.queryByText("Unsaved changes · kept if you leave")).toBeNull(),
      WAIT,
    );
  });

  it("edits a row in the one drawer: look changes go to the draft and preview; remove takes it off", async () => {
    const { fake } = await home();
    fireEvent.click(screen.getByRole("button", { name: "Edit Continue Watching" }));
    const drawer = await screen.findByRole("dialog", {}, WAIT);
    expect(within(drawer).getByRole("tab", { name: "Settings" })).toBeInTheDocument();
    expect(within(drawer).getByRole("tab", { name: "Content" })).toBeInTheDocument();
    fireEvent.click(within(drawer).getByRole("switch", { name: "Hide watched" }));
    await waitFor(() => {
      const page = lastPreview(fake).page;
      const cw = page?.rows.find((r) => r.source.kind === "continue");
      expect(cw?.look.hideWatched).toBe(true);
    }, WAIT);
    fireEvent.click(within(drawer).getByRole("tab", { name: "Content" }));
    await waitFor(
      () => expect(calls(fake, "POST", "/pages/home/rows/preview").length).toBe(1),
      WAIT,
    );
    fireEvent.click(within(drawer).getByRole("button", { name: "Remove from page" }));
    await waitFor(() => {
      const page = lastPreview(fake).page;
      expect(page?.rows.some((r) => r.source.kind === "continue")).toBe(false);
    }, WAIT);
  });

  it("keeps the draft when you leave and restores it, then Discard drops it", async () => {
    await home();
    fireEvent.click(screen.getByRole("button", { name: "Hide Continue Watching" }));
    expect(
      await screen.findByText("Unsaved changes · kept if you leave", {}, WAIT),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        Object.keys(localStorage).some((k) => k.startsWith("ether.recommendations.draft:home@")),
      ).toBe(true),
    );
    cleanup();
    renderApp("/recommendations");
    expect(
      await screen.findByText("Unsaved changes · kept if you leave", {}, WAIT),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Discard/ }));
    await waitFor(() =>
      expect(screen.queryByText("Unsaved changes · kept if you leave")).toBeNull(),
    );
    expect(
      Object.keys(localStorage).some((k) => k.startsWith("ether.recommendations.draft:")),
    ).toBe(false);
  });

  it("says when someone else saved the page first", async () => {
    const { fake } = await home();
    fireEvent.click(screen.getByRole("button", { name: "Hide Continue Watching" }));
    await screen.findByText("Unsaved changes · kept if you leave", {}, WAIT);
    // Another owner saves first.
    await fake.fetch("/ether/api/pages/home", {
      method: "PUT",
      headers: { "If-Match": "0" },
      body: JSON.stringify({
        key: "home",
        mode: "curated",
        length: {},
        picks: false,
        rows: [],
        revision: 0,
      }),
    });
    fireEvent.click(screen.getByRole("button", { name: /Save page/ }));
    expect(
      await screen.findByText(/Someone else saved this page while you were editing/, {}, WAIT),
    ).toBeInTheDocument();
  });

  it("previews as a child: the access strip and rows hidden for them", async () => {
    const { fake } = renderApp("/recommendations?as=3");
    const strip = await screen.findByLabelText("Viewer access", {}, WAIT);
    expect(strip).toHaveTextContent("The Kids · age limit PG");
    expect(strip).toHaveTextContent(/Sees \d+ of \d+ libraries \(not Anime Shows\)/);
    expect(lastPreview(fake).viewer).toBe(3);
  });

  it("edits for one viewer: changes become theirs, and Reset to Everyone drops them", async () => {
    const { fake } = renderApp("/recommendations?editing=2");
    await screen.findByRole("region", { name: "#1: Continue Watching" }, WAIT);
    expect(screen.getByText(/Editing Sam Rivera's page only/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Hide Continue Watching" }));
    await waitFor(() => {
      const page = lastPreview(fake).page!;
      const cw = page.rows.find((r) => r.source.kind === "continue")!;
      expect(cw.placement.mode).toBe("pinned");
      const v = page.variants?.find((x) => x.viewer === 2);
      expect(v?.rows.find((o) => o.row === cw.id)?.placement?.mode).toBe("hide");
    }, WAIT);
  });

  it("opens the page and row a link names", async () => {
    renderApp("/recommendations?page=library:9/home");
    await waitFor(
      () =>
        expect(screen.getByRole("button", { name: "Page" })).toHaveTextContent("Field Recordings"),
      WAIT,
    );
    fireEvent.click(screen.getByRole("button", { name: /Page settings/ }));
    const drawer = await screen.findByRole("dialog", {}, WAIT);
    expect(within(drawer).getByRole("radio", { name: /Curated/ })).toHaveAttribute(
      "aria-checked",
      "true",
    );
  });
});
