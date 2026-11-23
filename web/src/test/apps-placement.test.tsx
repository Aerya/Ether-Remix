import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { calls, renderApp } from "./harness";

const WAIT = { timeout: 8000 };

describe("Apps Plex placement", () => {
  it("keeps account, remote access, and secure connections on the Plex tab and read-only main port on Overview", async () => {
    const { fake } = renderApp("/apps/plex");
    expect(
      await screen.findByRole("button", { name: "Sign out of Plex" }, WAIT),
    ).toBeInTheDocument();
    expect(screen.getAllByText(/192\.168\.1\.20:32400/).length).toBeGreaterThan(0);
    expect(screen.queryByText(/127\.0\.0\.1/)).not.toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: "Port" })).toHaveValue(32400);
    expect(screen.getByRole("heading", { name: "Connect a device" })).toBeInTheDocument();
    expect(screen.getAllByText("Claimed by Plex account Braedon").length).toBeGreaterThan(0);
    expect(screen.queryByLabelText("Owner token")).not.toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: "Claimed" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Remote access" }));
    expect(await screen.findByLabelText("Allow remote access", {}, WAIT)).toBeInTheDocument();
    expect(screen.getByLabelText("Secure connections")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Connection checks" })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Advanced" })).not.toBeInTheDocument();
    expect(calls(fake, "POST", "/plex/claim")).toHaveLength(0);
    fireEvent.click(screen.getByRole("tab", { name: "Overview" }));
    await waitFor(() =>
      expect(screen.queryByLabelText("Allow remote access")).not.toBeInTheDocument(),
    );
    expect(screen.queryByRole("spinbutton", { name: "Port" })).not.toBeInTheDocument();
    // Server address (with the read-only main port) moved to General.
    expect(screen.queryByRole("heading", { name: "Server address" })).toBeNull();
    const plexRow = document.querySelector('[data-app-id="plex"]') as HTMLElement;
    expect(plexRow).not.toBeNull();
    expect(within(plexRow).getByRole("switch", { name: "Plex enabled" })).toBeChecked();
    expect(within(plexRow).getByRole("link", { name: "Plex" })).toHaveAttribute(
      "href",
      "/apps/plex",
    );
  });

  it("opens a disabled Plex page with Off status and port editor without mutating settings", async () => {
    const { fake } = renderApp("/apps/plex", {}, (f) => {
      f.state.config.frontends = { plex: { enabled: false } };
    });
    expect(await screen.findByRole("heading", { name: "Apps" }, WAIT)).toBeInTheDocument();
    expect(await screen.findByRole("switch", { name: "Plex enabled" }, WAIT)).not.toBeChecked();
    expect(screen.getByText("Off")).toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: "Port" })).toHaveValue(32400);
    expect(calls(fake, "PUT", "/config")).toHaveLength(0);
  });

  it("opens remote access and secure connections at /apps/plex/remote", async () => {
    renderApp("/apps/plex/remote");
    expect(await screen.findByLabelText("Allow remote access", {}, WAIT)).toBeInTheDocument();
    expect(screen.getByLabelText("Secure connections")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Remote access", selected: true })).toBeInTheDocument();
  });
});
