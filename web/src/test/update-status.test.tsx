import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { describeUpdaterMessage, UpdatesStatus } from "../pages/settings/sections";
import { AutomaticWhatsNew } from "../components/UpdateNotesDrawer";
import type { UpdateNotes, UpdateStatus } from "../lib/types";

const base: UpdateStatus = {
  enabled: true,
  current: "0.2.32",
  capability: "self-apply",
  state: "idle",
  autoUpdate: false,
  requested: false,
  force: false,
  window: { start: "03:00", end: "05:00", open: false },
  activeSessions: 0,
  quietPeriodSeconds: 60,
  confirming: false,
};

async function show(patch: Partial<UpdateStatus> = {}) {
  const fetch = vi.fn(
    async (_input: string, _init?: RequestInit) =>
      new Response(JSON.stringify({ ...base, ...patch })),
  );
  vi.stubGlobal("fetch", fetch);
  render(<UpdatesStatus />);
  await screen.findByText("Version 0.2.32");
  return fetch;
}

describe("update availability confidence", () => {
  it("does not claim to be current before the first check", async () => {
    await show();
    expect(screen.queryByText("Ether is up to date")).not.toBeInTheDocument();
    expect(screen.getAllByText("Not checked yet")).toHaveLength(2);
  });

  it("shows a failed feed check instead of claiming to be current", async () => {
    await show({ lastCheck: "2026-10-29T20:00:00Z", lastError: "check: certificate trust failed" });
    expect(screen.queryByText("Ether is up to date")).not.toBeInTheDocument();
    expect(screen.getAllByText("check: certificate trust failed")).toHaveLength(2);
  });

  it("reports current after a successful check with no newer release", async () => {
    await show({ lastCheck: "2026-10-29T20:00:00Z" });
    expect(screen.getByText("Ether is up to date")).toBeInTheDocument();
  });

  it("keeps a known release installable when a later check fails", async () => {
    await show({
      lastCheck: "2026-10-29T20:00:00Z",
      lastError: "check: unavailable",
      available: { version: "0.2.45", kind: "installer", staged: false },
    });
    expect(screen.getByText("Version 0.2.45 — Idle")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Update now" })).toBeEnabled();
    expect(screen.getByText("check: unavailable")).toBeInTheDocument();
  });

  it("preserves the disabled explanation", async () => {
    await show({ enabled: false, disabledReason: "No update feed configured" });
    expect(screen.getByText("No update feed configured")).toBeInTheDocument();
    expect(screen.queryByText("Ether is up to date")).not.toBeInTheDocument();
  });

  it("maps internal Go updater errors to plain owner language and disables manual check", async () => {
    await show({
      enabled: false,
      capability: "disabled",
      disabledReason: "updater: no release public key configured",
    });
    expect(screen.getByText("This build can't check for signed updates.")).toBeInTheDocument();
    expect(screen.queryByText(/updater:/i)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Check now" })).toBeDisabled();
  });

  it("never leaks raw updater: prefixes from Go error messages", () => {
    expect(describeUpdaterMessage("updater: no release public key configured")).toBe(
      "This build can't check for signed updates.",
    );
    expect(
      describeUpdaterMessage(
        "updater: updates are disabled in this build: updater: no release public key configured",
      ),
    ).toBe("This build can't check for signed updates.");
    expect(describeUpdaterMessage("check: updater: manifest signature verification failed")).toBe(
      "The update feed's signature couldn't be verified.",
    );
    expect(describeUpdaterMessage("updater: stat target: permission denied")).not.toMatch(
      /updater:/i,
    );
  });

  it("preserves the skipped rollback explanation", async () => {
    await show({ state: "rolled-back", failedVersion: "0.2.44" });
    expect(
      screen.getByText("Version 0.2.44 was rolled back and will be skipped"),
    ).toBeInTheDocument();
    expect(screen.queryByText("Ether is up to date")).not.toBeInTheDocument();
  });
});

describe("automatic update schedule", () => {
  const staged = { version: "0.3.6", kind: "binary", staged: true };

  it("says when a staged update installs", async () => {
    await show({ autoUpdate: true, state: "waiting-for-window", available: staged });
    expect(
      screen.getByText("Installs tonight between 03:00 and 05:00, once nobody is watching."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Ether installs updates by itself at night, once nobody is watching."),
    ).toBeInTheDocument();
  });

  it("explains a staged rollout wait", async () => {
    await show({
      autoUpdate: true,
      state: "waiting-for-rollout",
      available: { ...staged, staged: false, installAfter: "2026-11-09T10:00:00Z" },
    });
    expect(screen.getByText(/New releases reach servers a few at a time/)).toBeInTheDocument();
  });

  it("quietly suggests automatic updates when they are off", async () => {
    await show({ autoUpdate: false });
    expect(
      screen.getByText(/Off\. Turn on “Install updates automatically” below/),
    ).toBeInTheDocument();
  });

  it("gives a container the exact pull commands", async () => {
    await show({
      capability: "notify-only",
      notifyKind: "container",
      channel: "stable",
      available: { version: "0.3.6", kind: "binary", staged: false },
    });
    expect(screen.getByText("docker compose pull && docker compose up -d")).toBeInTheDocument();
    expect(
      screen.getByText("docker pull ghcr.io/braedonsaunders/ether:latest"),
    ).toBeInTheDocument();
    expect(screen.getByText(/Watchtower/)).toBeInTheDocument();
  });
});

describe("update startup progress", () => {
  it("shows a neutral startup phase", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({ error: { code: "starting", phase: "backing_up", message: "Saving" } }),
            { status: 503 },
          ),
      ),
    );
    render(<UpdatesStatus />);
    expect(await screen.findByText("Saving a safety copy")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});

describe("release notes", () => {
  it("remembers an automatic dismissal across console visits", async () => {
    const notes: UpdateNotes = {
      version: "0.2.32",
      markdown: "## Playback\n\n- Faster startup",
      unseen: true,
      installed: { from: "0.2.31", to: "0.2.32", at: "2026-11-08T03:12:00Z" },
    };
    const fetch = vi.fn(async (input: string, init?: RequestInit) => {
      if (input.endsWith("/dismiss")) {
        expect(JSON.parse(String(init?.body))).toEqual({ version: "0.2.32" });
        notes.unseen = false;
        return new Response(null, { status: 204 });
      }
      return new Response(JSON.stringify(notes));
    });
    vi.stubGlobal("fetch", fetch);
    const view = render(<AutomaticWhatsNew version={notes.version} />);
    expect(await screen.findByRole("heading", { name: "Playback" })).toBeInTheDocument();
    expect(screen.getByRole("listitem")).toHaveTextContent("Faster startup");
    expect(screen.getByText(/Ether installed it by itself/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    view.unmount();
    render(<AutomaticWhatsNew version={notes.version} />);
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});
