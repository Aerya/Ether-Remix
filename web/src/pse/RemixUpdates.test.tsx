import { fireEvent, render, screen, cleanup, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { compareUpdateVersions, REMIX_UPDATES, RemixUpdates } from "./RemixUpdates";

const version = "0.2.37-remix.24.1";
const readKey = "ether-remix.updates.read";
const legacyKey = "ether-remix.updates.seen";
beforeEach(() => {
  localStorage.clear();
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value: function (this: HTMLDialogElement) {
      this.setAttribute("open", "");
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value: function (this: HTMLDialogElement) {
      this.removeAttribute("open");
    },
  });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  localStorage.clear();
});
const dismiss = () => fireEvent.click(screen.getByRole("button", { name: /Got it|J’ai compris/ }));
const openManually = () =>
  fireEvent.click(screen.getByRole("button", { name: /what's new|Nouveautés Ether-Remix/i }));

it("remembers read versions, shows no previously read notes on reopening, and keeps explicit history", async () => {
  const first = render(<RemixUpdates version={version} />);
  expect(await screen.findByRole("dialog")).toHaveAttribute("open");
  dismiss();
  const progress = JSON.parse(localStorage.getItem(readKey)!);
  expect(progress.version).toBe(version);
  expect(progress.ids).toEqual(REMIX_UPDATES.map((r) => r.id));
  expect(localStorage.getItem(legacyKey)).toBe(REMIX_UPDATES[0].id);
  first.unmount();
  render(<RemixUpdates version={version} />);
  expect(screen.queryByRole("dialog")).toBeNull();
  openManually();
  expect(await screen.findByText(/already read all|déjà lu toutes/)).toBeTruthy();
  expect(screen.queryByRole("heading", { name: REMIX_UPDATES[1].title[1] })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /Full history|Historique complet/ }));
  expect(screen.getByRole("heading", { name: REMIX_UPDATES[1].title[1] })).toBeTruthy();
});

it("migrates the old delivery ID and only shows newer notes, including releases skipped between upgrades", async () => {
  localStorage.setItem(legacyKey, "2026-11-09-animes");
  render(<RemixUpdates version={version} />);
  const dialog = await screen.findByRole("dialog");
  expect(screen.getByRole("heading", { name: REMIX_UPDATES[0].title[1] })).toBeTruthy();
  expect(screen.getByRole("heading", { name: REMIX_UPDATES[1].title[1] })).toBeTruthy();
  expect(screen.getByRole("heading", { name: REMIX_UPDATES[2].title[1] })).toBeTruthy();
  expect(screen.getByRole("heading", { name: REMIX_UPDATES[3].title[1] })).toBeTruthy();
  expect(
    screen.queryByRole("heading", {
      name: REMIX_UPDATES.find((r) => r.id === "2026-11-09-animes")!.title[1],
    }),
  ).toBeNull();
  fireEvent(dialog, new Event("cancel", { bubbles: true, cancelable: true }));
  await waitFor(() => expect(dialog).not.toHaveAttribute("open"));
  expect(JSON.parse(localStorage.getItem(readKey)!).version).toBe(version);
});

it("does not show the IPTV delivery again when migrating the latest legacy marker", async () => {
  localStorage.setItem(legacyKey, "2026-11-09-iptv-editor");
  render(<RemixUpdates version={version} />);
  expect(await screen.findByRole("dialog")).toHaveAttribute("open");
  expect(screen.getAllByRole("heading", { level: 3 })).toHaveLength(
    REMIX_UPDATES.findIndex((entry) => entry.id === "2026-11-09-iptv-editor"),
  );
  expect(screen.getByText(/v0.2.37-remix.24.1/)).toBeTruthy();
});

it("does not repeat the same notes when a Docker build is retried or rebuilt", async () => {
  const first = render(<RemixUpdates version={version} />);
  await screen.findByRole("dialog");
  dismiss();
  first.unmount();
  render(<RemixUpdates version="0.2.37-remix.24.2" />);
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("preserves a newer read version after rolling back and consulting history", async () => {
  localStorage.setItem(readKey, JSON.stringify({ version: "0.2.37-remix.24.1", ids: [] }));
  render(<RemixUpdates version={version} />);
  expect(screen.queryByRole("dialog")).toBeNull();
  openManually();
  expect(await screen.findByText(/already read all|déjà lu toutes/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /Full history|Historique complet/ }));
  dismiss();
  expect(JSON.parse(localStorage.getItem(readKey)!).version).toBe("0.2.37-remix.24.1");
});

it("excludes notes from a version newer than the running server", async () => {
  localStorage.setItem(legacyKey, "2026-11-09-animes");
  render(<RemixUpdates version="0.2.37-remix.4.1" />);
  await screen.findByRole("dialog");
  expect(screen.queryByRole("heading", { name: REMIX_UPDATES[1].title[1] })).toBeNull();
  expect(
    screen.getByRole("heading", {
      name: REMIX_UPDATES.find((r) => r.id === "2026-11-09-sources-sync")!.title[1],
    }),
  ).toBeTruthy();
});

it("keeps manual access available when browser storage is blocked", async () => {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw new Error("blocked");
  });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new Error("blocked");
  });
  render(<RemixUpdates version={version} />);
  expect(screen.queryByRole("dialog")).toBeNull();
  openManually();
  expect(await screen.findByRole("dialog")).toHaveAttribute("open");
  dismiss();
  openManually();
  expect(await screen.findByText(/already read all|déjà lu toutes/)).toBeTruthy();
});

it("compares versions numerically and reserves the running version for the newest delivery", () => {
  expect(compareUpdateVersions("v0.2.37-remix.10.1", "0.2.37-remix.9.2")).toBe(1);
  expect(compareUpdateVersions("0.2.38-remix.1.1", "0.2.37-remix.99.1")).toBe(1);
  expect(compareUpdateVersions("0.2.37-remix.5.2", "0.2.37-remix.5.1")).toBe(1);
  expect(compareUpdateVersions("dev", version)).toBeNull();
  expect(REMIX_UPDATES.filter((r) => r.version === "current")).toEqual([REMIX_UPDATES[0]]);
});

it("keeps the migrated read version in development previews with a non-release version", async () => {
  localStorage.setItem(legacyKey, "2026-11-09-iptv-editor");
  const first = render(<RemixUpdates version="0.9.0-mock" />);
  await screen.findByRole("dialog");
  dismiss();
  first.unmount();
  expect(JSON.parse(localStorage.getItem(readKey)!).version).toBe("0.2.37-remix.5.1");
  render(<RemixUpdates version="0.9.0-mock" />);
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("treats a legacy newest-note marker as acknowledging its entire previous history", () => {
  localStorage.setItem(legacyKey, REMIX_UPDATES[0].id);
  render(<RemixUpdates version="0.9.0-mock" />);
  expect(screen.queryByRole("dialog")).toBeNull();
});
