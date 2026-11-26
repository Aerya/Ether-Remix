import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { chosenStremioLink, stremioOptionsSegment } from "./StremioLinkChoice";
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

it("builds the link and QR code from the display and quota choices", async () => {
  const connection = {
    surface: "stremio",
    url: "https://ether.example/stremio/secret/manifest.json",
    qr: "data:image/png;base64,AA==",
    filesUrl: "https://ether.example/stremio/secret/fichiers/manifest.json",
    filesQr: "data:image/png;base64,BB==",
    "qr:detaille": "data:image/png;base64,CC==",
    "filesQr:compact": "data:image/png;base64,DD==",
  };
  const quotas = { q2160: 2, q1080: 3, q720: 1 };
  expect(stremioOptionsSegment({ format: "direct", ...quotas })).toBeNull();
  expect(stremioOptionsSegment({ format: "compact", q2160: 9, q1080: 0, q720: 1 })).toBe(
    "compact-2160.5-1080.0-720.1",
  );
  expect(chosenStremioLink(connection, false, { format: "direct", ...quotas })).toBe(connection);
  expect(chosenStremioLink(connection, false, { format: "detaille", ...quotas })).toMatchObject({
    url: "https://ether.example/stremio/secret/o/detaille-2160.2-1080.3-720.1/manifest.json",
    qr: "data:image/png;base64,CC==",
  });
  expect(chosenStremioLink(connection, true, { format: "compact", ...quotas })).toMatchObject({
    url: "https://ether.example/stremio/secret/fichiers/o/compact-2160.2-1080.3-720.1/manifest.json",
    qr: "data:image/png;base64,DD==",
  });
  expect(chosenStremioLink(connection, false, { format: "detaille", ...quotas, q720: 0 }).qr).toBe(
    "",
  );

  vi.spyOn(globalThis, "fetch").mockImplementation(async (_url, init) => {
    const body = init?.method === "POST" ? connection : { apps: ["stremio"] };
    return new Response(JSON.stringify(body), { status: 200 });
  });
  render(<ConnectApps base="/me" token="portal" />);
  fireEvent.click(await screen.findByRole("button", { name: "Create connection" }));
  fireEvent.click(await screen.findByRole("radio", { name: /detailed/i }));
  expect(
    screen.getByDisplayValue(
      "https://ether.example/stremio/secret/o/detaille-2160.2-1080.3-720.1/manifest.json",
    ),
  ).toBeInTheDocument();
  fireEvent.change(screen.getByRole("spinbutton", { name: "1080p" }), { target: { value: "0" } });
  expect(
    screen.getByDisplayValue(
      "https://ether.example/stremio/secret/o/detaille-2160.2-1080.0-720.1/manifest.json",
    ),
  ).toBeInTheDocument();
});
