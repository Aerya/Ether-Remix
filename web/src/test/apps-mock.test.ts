import { describe, expect, it } from "vitest";
import { createFakeServer } from "../mock/server";

describe("Apps mock API", () => {
  it.each([false, true])(
    "preserves setup choices and installed-client status (%s) while saving listener settings",
    async (velaInstalled) => {
      const fake = createFakeServer({ velaInstalled });
      const fetch = fake.fetch;
      const initial = await (await fetch("/ether/api/frontends")).json();
      expect(
        initial.find((app: { descriptor: { id: string } }) => app.descriptor.id === "plex").enabled,
      ).toBe(true);
      expect(
        initial.find((app: { descriptor: { id: string } }) => app.descriptor.id === "vela").status
          .webPlayerReady,
      ).toBe(velaInstalled);
      const players = await (await fetch("/ether/api/web-player")).json();
      expect(players.players.some((player: { id: string }) => player.id === "vela")).toBe(
        velaInstalled,
      );
      await fetch("/ether/api/frontends/silo/settings", {
        method: "PUT",
        body: JSON.stringify({ port: 8092, discovery: true }),
      });
      await fetch("/ether/api/frontends/silo/enabled", {
        method: "PUT",
        body: JSON.stringify({ enabled: true }),
      });
      const settings = await (await fetch("/ether/api/frontends/silo/settings")).json();
      expect(settings).toEqual({ port: 8092, discovery: true });
      expect(fake.state.config.frontends?.plex?.enabled).toBe(true);
      expect(fake.state.config.frontendPorts?.silo).toBe(8092);
    },
  );

  it("lists enabled apps and returns a viewer connection", async () => {
    const fake = createFakeServer();
    await fake.fetch("/ether/api/frontends/stremio/enabled", {
      method: "PUT",
      body: JSON.stringify({ enabled: true }),
    });
    // Plex is on by default in the mock and is listed like any other enabled app.
    expect(await (await fake.fetch("/ether/api/me/apps")).json()).toEqual({
      apps: ["plex", "stremio"],
    });
    const created = await (
      await fake.fetch("/ether/api/me/apps/stremio", { method: "POST" })
    ).json();
    expect(created.url).toContain("/stremio/mock-grant/manifest.json");
    expect(created.qr).toMatch(/^data:image\/png;base64,/);
  });

  it("lists, installs, guards private URLs, and removes Vela skins in mock mode", async () => {
    const fake = createFakeServer({ velaInstalled: true });
    const initial = await (await fake.fetch("/ether/api/vela/skins")).json();
    expect(
      initial.skins.some(
        (s: { id: string; builtin: boolean }) => s.id === "cinematic-doc" && s.builtin,
      ),
    ).toBe(true);
    expect(
      initial.skins.some((s: { id: string; builtin: boolean }) => s.id === "harbor" && !s.builtin),
    ).toBe(true);

    const builtinDelete = await fake.fetch("/ether/api/vela/skins/mochi", { method: "DELETE" });
    expect(builtinDelete.status).toBe(400);

    const lanBlocked = await fake.fetch("/ether/api/vela/skins", {
      method: "POST",
      body: JSON.stringify({ url: "http://192.168.1.50/reef.vskin", lan: false }),
    });
    expect(lanBlocked.status).toBe(400);

    const lanAllowed = await fake.fetch("/ether/api/vela/skins", {
      method: "POST",
      body: JSON.stringify({ url: "http://192.168.1.50/reef.vskin", lan: true }),
    });
    expect(lanAllowed.status).toBe(201);
    expect((await lanAllowed.json()).id).toBe("reef");

    const removed = await fake.fetch("/ether/api/vela/skins/reef", { method: "DELETE" });
    expect(removed.status).toBe(200);
  });

  it("uploads Vela wordmarks and stores normalized branding settings", async () => {
    const fake = createFakeServer({ velaInstalled: true });
    const svg =
      '<svg xmlns="http://www.w3.org/2000/svg" width="220" height="48" viewBox="0 0 220 48"><rect width="220" height="48" rx="8" fill="#111"/></svg>';
    const form = new FormData();
    form.append("file", new File([svg], "wordmark.svg", { type: "image/svg+xml" }));
    const uploadRes = await fake.fetch("/ether/api/frontends/vela/wordmark", {
      method: "POST",
      body: form,
    });
    expect(uploadRes.status).toBe(200);
    const uploaded = (await uploadRes.json()) as { url: string; width: number; height: number };
    expect(uploaded.url).toMatch(/^\/vela\/v0\/branding\/[0-9a-f]{64}\.svg$/);
    expect(uploaded.width).toBe(220);
    expect(uploaded.height).toBe(48);

    const served = fake.velaBranding.serveAsset(uploaded.url.replace("/vela/v0/branding/", ""));
    expect(served.status).toBe(200);
    expect(served.headers.get("Content-Type")).toBe("image/svg+xml");

    await fake.fetch("/ether/api/frontends/vela/settings", {
      method: "PUT",
      body: JSON.stringify({
        defaultSkinId: "cinematic",
        appName: "  North Star Cinema  ",
        wordmark: uploaded,
      }),
    });
    const saved = await (await fake.fetch("/ether/api/frontends/vela/settings")).json();
    expect(saved).toEqual({
      defaultSkinId: "cinematic",
      appName: "North Star Cinema",
      wordmark: uploaded,
    });
  });

  it("uploads a square Vela app icon and rejects non-square icons", async () => {
    const fake = createFakeServer({ velaInstalled: true });
    const nonSquareSvg =
      '<svg xmlns="http://www.w3.org/2000/svg" width="200" height="100" viewBox="0 0 200 100"><rect width="200" height="100"/></svg>';
    const badForm = new FormData();
    badForm.append("file", new File([nonSquareSvg], "wide.svg", { type: "image/svg+xml" }));
    const badRes = await fake.fetch("/ether/api/frontends/vela/icon", {
      method: "POST",
      body: badForm,
    });
    expect(badRes.status).toBe(400);

    const squareSvg =
      '<svg xmlns="http://www.w3.org/2000/svg" width="128" height="128" viewBox="0 0 128 128"><rect width="128" height="128" fill="#111"/></svg>';
    const okForm = new FormData();
    okForm.append("file", new File([squareSvg], "icon.svg", { type: "image/svg+xml" }));
    const okRes = await fake.fetch("/ether/api/frontends/vela/icon", {
      method: "POST",
      body: okForm,
    });
    expect(okRes.status).toBe(200);
    const uploaded = (await okRes.json()) as { url: string; width: number; height: number };
    expect(uploaded.url).toMatch(/^\/vela\/v0\/branding\/icon-[0-9a-f]{64}\.svg$/);
    expect(uploaded.width).toBe(128);
    expect(uploaded.height).toBe(128);

    const served = fake.velaBranding.serveAsset(uploaded.url.replace("/vela/v0/branding/", ""));
    expect(served.status).toBe(200);
    expect(served.headers.get("Content-Type")).toBe("image/svg+xml");
  });

  it("exposes observed addresses without inventing domains and persists published addresses", async () => {
    const fake = createFakeServer();
    await fake.fetch("/ether/api/frontends/jellyfin/enabled", {
      method: "PUT",
      body: JSON.stringify({ enabled: true }),
    });
    const list = await (await fake.fetch("/ether/api/frontends")).json();
    const jellyfin = list.find(
      (app: { descriptor: { id: string } }) => app.descriptor.id === "jellyfin",
    );
    expect(jellyfin.status.connectUrls).not.toContain("https://jellyfin.example.com");
    expect(jellyfin.status.observedAddresses).toEqual([
      "https://jellyfin.example.com",
      "https://media.example.com/jellyfin",
    ]);

    await fake.fetch("/ether/api/frontends/jellyfin/settings", {
      method: "PUT",
      body: JSON.stringify({
        port: 8096,
        publishedAddresses: ["https://media.example.com/jellyfin/"],
      }),
    });
    const saved = await (await fake.fetch("/ether/api/frontends/jellyfin/settings")).json();
    expect(saved).toEqual({
      port: 8096,
      publishedAddresses: ["https://media.example.com/jellyfin"],
    });
    const connect = await (
      await fake.fetch("/ether/api/me/apps/jellyfin", { method: "POST" })
    ).json();
    expect(connect.url).toBe("https://media.example.com/jellyfin");
  });
});
