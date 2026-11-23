import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ClientIdentityConfig } from "../lib/types";
import { calls, renderApp } from "./harness";

const WAIT = { timeout: 4000 };
const DEFAULT_UA =
  "Ether/dev (+https://github.com/braedonsaunders/ether/blob/main/docs/indexer-operators.md)";

describe("Sources › Indexers identity rules", () => {
  it("shows the compact honest-default card and opens the rules drawer", async () => {
    renderApp("/sources/indexers");
    await screen.findByRole("heading", { name: "How Ether identifies itself" }, WAIT);
    expect(screen.getByText("Ether (honest, recommended)")).toBeInTheDocument();
    expect(screen.getByText("No rules")).toBeInTheDocument();
    expect(screen.getByText(DEFAULT_UA)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Manage rules" }));
    const drawer = await screen.findByRole("dialog", { name: "How Ether identifies itself" }, WAIT);
    expect(within(drawer).getByText("Default")).toBeInTheDocument();
    expect(within(drawer).getByRole("button", { name: "Add rule" })).toBeInTheDocument();
  });

  it("adds a rule with Advanced header/parameter edits and renders masked preview", async () => {
    const { fake } = renderApp("/sources/indexers");
    await screen.findByRole("heading", { name: "How Ether identifies itself" }, WAIT);
    fireEvent.click(screen.getByRole("button", { name: "Manage rules" }));
    const drawer = await screen.findByRole("dialog", { name: "How Ether identifies itself" }, WAIT);
    fireEvent.click(within(drawer).getByRole("button", { name: "Add rule" }));

    const editor = await screen.findByRole("dialog", { name: "Add identity rule" }, WAIT);
    fireEvent.click(within(editor).getByRole("button", { name: /Advanced/ }));
    fireEvent.click(within(editor).getByRole("button", { name: "Add header edit" }));
    fireEvent.change(within(editor).getByLabelText("Header name"), {
      target: { value: "X-Owner" },
    });
    fireEvent.change(within(editor).getByLabelText("Header value"), {
      target: { value: "yes" },
    });

    await waitFor(() => expect(within(editor).getAllByText(/apikey=••••/)).toHaveLength(2), WAIT);

    fireEvent.click(within(editor).getByRole("button", { name: "Save rule" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/clientIdentity")).toHaveLength(1), WAIT);
    const body = calls(fake, "PUT", "/config/clientIdentity")[0]!.body as ClientIdentityConfig;
    expect(body.rules).toHaveLength(1);
    expect(body.rules![0]).toMatchObject({
      target: "indexer",
      indexerId: "i1",
      profile: "sonarr",
      headers: [{ name: "X-Owner", value: "yes" }],
    });
  });

  it("shows read-only Identifies as summary inside an indexer drawer and opens rules drawer", async () => {
    renderApp("/sources/indexers", {}, (fake) => {
      fake.state.config.clientIdentity = {
        rules: [
          { id: "r1", target: "indexer", indexerId: "i1", profile: "sonarr" },
          { id: "r2", target: "nzb_downloads", profile: "sabnzbd" },
        ],
      };
    });
    await screen.findByRole("heading", { name: "How Ether identifies itself" }, WAIT);
    expect(screen.getByText("2 rules")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Edit Indexer One" }));
    const drawer = await screen.findByRole("dialog", { name: "Indexer One" }, WAIT);
    expect(
      within(drawer).getByText("Identifies as: Compatible with Sonarr (from rule: Indexer One)"),
    ).toBeInTheDocument();

    fireEvent.click(within(drawer).getByRole("button", { name: "Manage rules" }));
    await screen.findByRole("dialog", { name: "How Ether identifies itself" }, WAIT);
  });

  it("runs a capabilities test for a chosen indexer from the rules drawer", async () => {
    const { fake } = renderApp("/sources/indexers", {}, (f) => {
      f.state.config.clientIdentity = {
        rules: [{ id: "r1", target: "indexer", indexerId: "i1", profile: "sonarr" }],
      };
    });
    await screen.findByRole("heading", { name: "How Ether identifies itself" }, WAIT);
    fireEvent.click(screen.getByRole("button", { name: "Manage rules" }));
    const drawer = await screen.findByRole("dialog", { name: "How Ether identifies itself" }, WAIT);

    fireEvent.click(within(drawer).getByRole("button", { name: "Test" }));
    await waitFor(
      () => expect(calls(fake, "POST", "/test/indexer-identity")).toHaveLength(1),
      WAIT,
    );
    await waitFor(
      () =>
        expect(
          within(drawer).getByText(/Capabilities answered using Compatible with Sonarr\./),
        ).toBeInTheDocument(),
      WAIT,
    );
  });

  it("renders Movies→Radarr and TV→Sonarr rules with media-specific previews and indexer summary", async () => {
    renderApp("/sources/indexers", {}, (fake) => {
      fake.state.config.clientIdentity = {
        rules: [
          {
            id: "r-movie",
            target: "indexer",
            indexerId: "i1",
            mediaType: "movie",
            profile: "radarr",
          },
          {
            id: "r-tv",
            target: "indexer",
            indexerId: "i1",
            mediaType: "tv",
            profile: "sonarr",
          },
        ],
      };
    });

    await screen.findByRole("heading", { name: "How Ether identifies itself" }, WAIT);
    expect(screen.getByText("2 rules")).toBeInTheDocument();

    // Indexer drawer reflects the media-type split accurately instead of Default.
    fireEvent.click(screen.getByRole("button", { name: "Edit Indexer One" }));
    const ixDrawer = await screen.findByRole("dialog", { name: "Indexer One" }, WAIT);
    expect(
      within(ixDrawer).getByText(
        "Identifies by media type: Movies: Compatible with Radarr · TV: Compatible with Sonarr",
      ),
    ).toBeInTheDocument();

    // Open the rules drawer with Movies→Radarr and TV→Sonarr and verify both previews.
    fireEvent.click(within(ixDrawer).getByRole("button", { name: "Manage rules" }));
    const drawer = await screen.findByRole("dialog", { name: "How Ether identifies itself" }, WAIT);
    expect(within(drawer).getAllByText("Movies").length).toBeGreaterThan(0);
    expect(within(drawer).getAllByText("TV").length).toBeGreaterThan(0);
    expect(within(drawer).getByText("Compatible with Radarr")).toBeInTheDocument();
    expect(within(drawer).getByText("Compatible with Sonarr")).toBeInTheDocument();
    expect(
      within(drawer).getByRole("button", { name: "Edit Indexer One · Movies" }),
    ).toBeInTheDocument();
    expect(
      within(drawer).getByRole("button", { name: "Edit Indexer One · TV" }),
    ).toBeInTheDocument();

    const previewButtons = within(drawer).getAllByRole("button", {
      name: "What the indexer sees",
    });
    // First rule = Movies → Radarr; second rule = TV → Sonarr.
    fireEvent.click(previewButtons[0]!);
    fireEvent.click(previewButtons[1]!);

    await waitFor(() => {
      expect(within(drawer).getByText("Sample search (Movies)")).toBeInTheDocument();
      expect(within(drawer).getByText("Sample NZB download (Movies)")).toBeInTheDocument();
      expect(within(drawer).getByText(/t=movie/)).toBeInTheDocument();
      expect(within(drawer).getByText(/cat=2000/)).toBeInTheDocument();
      expect(within(drawer).getAllByText(/Radarr\//).length).toBeGreaterThanOrEqual(2);

      expect(within(drawer).getByText("Sample search (TV)")).toBeInTheDocument();
      expect(within(drawer).getByText("Sample NZB download (TV)")).toBeInTheDocument();
      expect(within(drawer).getByText(/t=tvsearch/)).toBeInTheDocument();
      expect(within(drawer).getByText(/cat=5000/)).toBeInTheDocument();
      expect(within(drawer).getAllByText(/Sonarr\//).length).toBeGreaterThanOrEqual(2);
    }, WAIT);
  });

  it("edits and saves a rule media type via the compact select", async () => {
    const { fake } = renderApp("/sources/indexers");
    await screen.findByRole("heading", { name: "How Ether identifies itself" }, WAIT);
    fireEvent.click(screen.getByRole("button", { name: "Manage rules" }));
    const drawer = await screen.findByRole("dialog", { name: "How Ether identifies itself" }, WAIT);
    fireEvent.click(within(drawer).getByRole("button", { name: "Add rule" }));

    const editor = await screen.findByRole("dialog", { name: "Add identity rule" }, WAIT);
    fireEvent.click(within(editor).getByRole("button", { name: "Media type" }));
    fireEvent.click(await screen.findByRole("option", { name: "Movies" }, WAIT));

    await waitFor(() => expect(within(editor).getByText(/t=movie/)).toBeInTheDocument(), WAIT);

    fireEvent.click(within(editor).getByRole("button", { name: "Save rule" }));
    await waitFor(() => expect(calls(fake, "PUT", "/config/clientIdentity")).toHaveLength(1), WAIT);
    const body = calls(fake, "PUT", "/config/clientIdentity")[0]!.body as ClientIdentityConfig;
    expect(body.rules?.[0]).toMatchObject({
      target: "indexer",
      indexerId: "i1",
      mediaType: "movie",
      profile: "sonarr",
    });
  });
});
