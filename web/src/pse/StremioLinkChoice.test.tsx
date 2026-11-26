import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ConnectApps } from "../pages/me/ConnectApps";

afterEach(() => vi.restoreAllMocks());

it("offers the catalogue link and the files-only link for Stremio", async () => {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (_url, init) => {
    const body =
      init?.method === "POST"
        ? {
            surface: "stremio",
            url: "https://ether.example/stremio/secret/manifest.json",
            qr: "data:image/png;base64,AA==",
            filesUrl: "https://ether.example/stremio/secret/fichiers/manifest.json",
            filesQr: "data:image/png;base64,BB==",
          }
        : { apps: ["stremio"] };
    return new Response(JSON.stringify(body), { status: 200 });
  });
  render(<ConnectApps base="/me" token="portal" />);
  fireEvent.click(await screen.findByRole("button", { name: "Create connection" }));
  expect(
    await screen.findByDisplayValue("https://ether.example/stremio/secret/manifest.json"),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("radio", { name: /files only/i }));
  expect(
    screen.getByDisplayValue("https://ether.example/stremio/secret/fichiers/manifest.json"),
  ).toBeInTheDocument();
  expect(screen.getByAltText("QR code for the server address")).toHaveAttribute(
    "src",
    "data:image/png;base64,BB==",
  );
  expect(screen.getByRole("link", { name: "Open in Stremio" })).toHaveAttribute(
    "href",
    "stremio://ether.example/stremio/secret/fichiers/manifest.json",
  );
  fireEvent.click(screen.getByRole("radio", { name: /with the profile's catalogues/i }));
  expect(screen.getByAltText("QR code for the server address")).toHaveAttribute(
    "src",
    "data:image/png;base64,AA==",
  );
});
