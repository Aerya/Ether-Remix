import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../lib/api";
import type {
  AddonCatalogImportList,
  AddonCatalogImportResult,
  AddonCatalogPreview,
} from "../../lib/contract";
import { StremioImportDrawer } from "./StremioImportDrawer";

vi.mock("../../components/pickers", () => ({
  useViewers: () => ({
    viewers: [
      {
        id: 1,
        plexId: 1,
        username: "owner",
        title: "Owner",
        isOwner: true,
        plays30d: 0,
        hoursWatched30d: 0,
      },
      {
        id: 2,
        plexId: 2,
        username: "kids",
        title: "Kids",
        isOwner: false,
        plays30d: 0,
        hoursWatched30d: 0,
      },
    ],
    error: null,
  }),
  viewerName: (v: { title?: string; username: string }) => v.title || v.username,
}));

const LIST_DATA: AddonCatalogImportList = {
  imports: [],
  availableAddons: [
    {
      addonId: "cinemeta",
      name: "Cinemeta",
      host: "catalogue.example",
      enabled: true,
      catalogCount: 3,
      hasImport: false,
    },
  ],
};

const PREVIEW_DATA: AddonCatalogPreview = {
  addonId: "addon.aiometadata",
  installed: false,
  addonName: "AIOMetadata",
  addonVersion: "2.4.0",
  host: "aiometadata.example",
  catalogs: [
    {
      key: "movie/popular",
      type: "movie",
      mediaType: "movie",
      id: "popular",
      name: "Popular",
      order: 1,
      supportsSkip: true,
      standalone: true,
      hasDuplicateName: true,
    },
    {
      key: "series/popular",
      type: "series",
      mediaType: "show",
      id: "popular",
      name: "Popular",
      order: 2,
      supportsSkip: true,
      standalone: true,
      hasDuplicateName: true,
    },
    {
      key: "movie/by-genre",
      type: "movie",
      mediaType: "movie",
      id: "by-genre",
      name: "By Genre",
      order: 3,
      supportsSkip: true,
      standalone: false,
      requiredExtras: ["genre"],
      extras: [{ name: "genre", label: "Genre", required: true }],
    },
    {
      key: "movie/search",
      type: "movie",
      mediaType: "movie",
      id: "search",
      name: "Search",
      order: 4,
      standalone: false,
      requiredExtras: ["search"],
      extras: [{ name: "search", label: "Search", required: true }],
    },
  ],
};

describe("StremioImportDrawer", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("previews a manifest URL, selects standalone catalogues by default, allows pinning required extras, and imports into a folder", async () => {
    vi.spyOn(api, "addonCatalogImports").mockResolvedValue(LIST_DATA);
    const previewSpy = vi.spyOn(api, "previewAddonCatalogImport").mockResolvedValue(PREVIEW_DATA);
    const applySpy = vi.spyOn(api, "applyAddonCatalogImport").mockResolvedValue({
      addonId: "addon.aiometadata",
      addonName: "AIOMetadata",
      created: 3,
      updated: 0,
      removed: 0,
      restored: 0,
      collectionIds: ["c1", "c2", "c3"],
      subscription: {
        addonId: "addon.aiometadata",
        addonName: "AIOMetadata",
        host: "aiometadata.example",
        addonEnabled: true,
        folder: "Adults",
        nameMode: "autoType",
        followManifest: true,
        refresh: "daily",
        maxItems: 500,
        enabled: true,
        browseInApps: true,
        catalogCount: 4,
        managedCount: 3,
        removedCount: 0,
      },
    } satisfies AddonCatalogImportResult);

    const onImported = vi.fn();
    const onClose = vi.fn();
    render(<StremioImportDrawer open onClose={onClose} onImported={onImported} />);

    const dialog = await screen.findByRole("dialog", { name: "Import from Stremio add-on" });

    // Paste manifest URL and inspect catalogues.
    const urlInput = within(dialog).getByLabelText("Add-on manifest link");
    fireEvent.change(urlInput, {
      target: { value: "https://aiometadata.example/secret-cfg/manifest.json" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Inspect manifest" }));

    await waitFor(() =>
      expect(previewSpy).toHaveBeenCalledWith({
        url: "https://aiometadata.example/secret-cfg/manifest.json",
      }),
    );

    // Step 2 shows standalone catalogues pre-checked and duplicate names disambiguated.
    const popularMoviesCheckbox = (await within(dialog).findByRole("checkbox", {
      name: /Popular \(Movies\)/i,
    })) as HTMLInputElement;
    const popularSeriesCheckbox = within(dialog).getByRole("checkbox", {
      name: /Popular \(Series\)/i,
    }) as HTMLInputElement;
    const byGenreCheckbox = within(dialog).getByRole("checkbox", {
      name: /By Genre/i,
    }) as HTMLInputElement;
    expect(popularMoviesCheckbox.checked).toBe(true);
    expect(popularSeriesCheckbox.checked).toBe(true);
    expect(byGenreCheckbox.checked).toBe(false);

    // Selecting "By Genre" reveals the required extra input and blocks submit until filled.
    fireEvent.click(byGenreCheckbox);
    expect(byGenreCheckbox.checked).toBe(true);
    const submitBtn = within(dialog).getByRole("button", { name: /Import 3 collections/i });
    expect(submitBtn).toBeDisabled();

    const genreInput = within(dialog).getByLabelText("By Genre Genre");
    fireEvent.change(genreInput, { target: { value: "Sci-Fi" } });
    expect(submitBtn).not.toBeDisabled();

    // Customise folder name and submit.
    const folderInput = within(dialog).getByLabelText("Folder");
    fireEvent.change(folderInput, { target: { value: "Adults" } });
    fireEvent.click(submitBtn);

    await waitFor(() => expect(applySpy).toHaveBeenCalledTimes(1));
    expect(applySpy).toHaveBeenCalledWith(
      expect.objectContaining({
        url: "https://aiometadata.example/secret-cfg/manifest.json",
        folder: "Adults",
        nameMode: "autoType",
        followManifest: true,
        items: [
          { key: "movie/popular" },
          { key: "series/popular" },
          { key: "movie/by-genre", params: { genre: "Sci-Fi" } },
        ],
      }),
    );
    expect(onImported).toHaveBeenCalled();
    expect(onClose).toHaveBeenCalled();
  });
});
