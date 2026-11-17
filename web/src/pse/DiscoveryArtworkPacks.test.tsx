import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { DiscoveryArtworkPacks } from "./DiscoveryArtworkPacks";
const fetch = vi.fn();
vi.mock("../lib/api", () => ({ apiFetch: (...args: unknown[]) => fetch(...args) }));
it("loads only on request, protects custom cards, preserves missing cards and allows manual ambiguity resolution", async () => {
  fetch.mockResolvedValue({
    images: [
      { name: "Netflix", url: "https://example.com/n.png" },
      { name: "Disney+", url: "https://example.com/d1.png" },
      { name: "Disney+", url: "https://example.com/d2.png" },
    ],
  });
  const apply = vi.fn();
  render(
    <DiscoveryArtworkPacks
      disabled={false}
      onApply={apply}
      targets={[
        { name: "Netflix", key: "netflix:FR", group: "services", entity: 8, customized: true },
        { name: "Netflix", key: "netflix:US", group: "services", entity: 8, customized: false },
        { name: "Disney+", key: "disney:FR", group: "services", entity: 337, customized: false },
        { name: "Missing", key: "missing", group: "services", entity: 999, customized: false },
      ]}
    />,
  );
  expect(fetch).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText(/Artwork packs|Packs de visuels/));
  fireEvent.click(screen.getByRole("button", { name: /Preview pack|Prévisualiser le pack/ }));
  await screen.findByText(/images in this pack|visuels dans le pack/);
  expect(apply).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: /Apply this pack|Appliquer ce pack/ }));
  expect(apply).toHaveBeenLastCalledWith({ "netflix:US": "https://example.com/n.png" });
  fireEvent.click(screen.getByRole("checkbox", { name: /Preserve|Préserver/ }));
  fireEvent.click(screen.getByText(/Review or adjust matches|Vérifier ou ajuster/));
  await screen.findByRole("combobox", { name: /Artwork for Disney|Visuel pour Disney/ });
  fireEvent.change(
    screen.getByRole("combobox", { name: /Artwork for Disney|Visuel pour Disney/ }),
    { target: { value: "https://example.com/d2.png" } },
  );
  fireEvent.click(screen.getByRole("button", { name: /Apply this pack|Appliquer ce pack/ }));
  expect(apply).toHaveBeenLastCalledWith({
    "netflix:FR": "https://example.com/n.png",
    "netflix:US": "https://example.com/n.png",
    "disney:FR": "https://example.com/d2.png",
  });
});
it("clears the previous preview when a replacement pack fails", async () => {
  fetch
    .mockResolvedValueOnce({ images: [{ name: "Netflix", url: "https://example.com/n.png" }] })
    .mockRejectedValueOnce(new Error("private-url-must-not-leak"));
  render(<DiscoveryArtworkPacks disabled={false} onApply={vi.fn()} targets={[]} />);
  fireEvent.click(screen.getByText(/Artwork packs|Packs de visuels/));
  fireEvent.click(screen.getByRole("button", { name: /Preview pack|Prévisualiser le pack/ }));
  await screen.findByText(/images in this pack|visuels dans le pack/);
  fireEvent.click(screen.getByRole("button", { name: /Preview pack|Prévisualiser le pack/ }));
  await screen.findByRole("alert");
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: /Apply this pack|Appliquer ce pack/ })).toBeNull(),
  );
  expect(screen.queryByText(/private-url-must-not-leak/)).toBeNull();
});
