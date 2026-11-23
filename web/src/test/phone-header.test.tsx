import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api, getToken, setToken } from "../lib/api";
import { renderApp, settleUi } from "./harness";

const originalMatchMedia = window.matchMedia;

function setViewportWidth(width: number) {
  window.matchMedia = (query: string) => {
    const minMatch = /min-width:\s*(\d+)px/.exec(query);
    const maxMatch = /max-width:\s*(\d+)px/.exec(query);
    let matches = false;
    if (minMatch) matches = width >= Number(minMatch[1]);
    else if (maxMatch) matches = width <= Number(maxMatch[1]);
    else if (query.includes("prefers-reduced-motion")) matches = true;
    return {
      matches,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    };
  };
}

afterEach(() => {
  window.matchMedia = originalMatchMedia;
  setToken("");
});

describe("phone and tablet header", () => {
  it("collapses search to a dialog trigger and moves secondary actions into the mobile menu at 390px", async () => {
    setViewportWidth(390);
    setToken("remote-owner-token");
    renderApp("/server", {
      bootstrap: { authRequired: true, authenticated: true, serverName: "Ether On Air Lab" },
    });

    const header = (await screen.findAllByRole("banner"))[0]!;
    await settleUi(() => {
      expect(within(header).getByText("Ether On Air Lab")).toBeInTheDocument();
      expect(within(header).getByRole("button", { name: "Search Ether" })).toBeInTheDocument();
      expect(within(header).getByRole("button", { name: "Ask Ether" })).toBeInTheDocument();
    });

    // Inline search box and secondary actions are not crowded into the phone header.
    expect(within(header).queryByPlaceholderText("Search or jump to…")).toBeNull();
    expect(within(header).queryByRole("button", { name: /Server health/ })).toBeNull();
    expect(within(header).queryByRole("button", { name: "Open the web player" })).toBeNull();
    expect(within(header).queryByRole("button", { name: "Appearance" })).toBeNull();
    expect(within(header).queryByRole("link", { name: "Your account" })).toBeNull();
    expect(within(header).queryByRole("button", { name: /Sign out/ })).toBeNull();

    // Tapping the search icon opens the ⌘K search dialog and navigates on hit.
    fireEvent.click(within(header).getByRole("button", { name: "Search Ether" }));
    const searchDialog = await screen.findByRole("dialog", { name: "Search Ether" });
    const searchInput = within(searchDialog).getByPlaceholderText("Search or jump to…");
    fireEvent.focus(searchInput);
    fireEvent.change(searchInput, { target: { value: "Live TV" } });
    const hits = await within(searchDialog).findAllByRole("option", { name: /Live TV/ });
    fireEvent.mouseDown(hits[0]!);
    expect(screen.queryByRole("dialog", { name: "Search Ether" })).toBeNull();
    expect(await screen.findByRole("heading", { name: "Live TV" })).toBeInTheDocument();

    // Opening the bottom Menu sheet exposes Watch, health, theme, account, and Sign out.
    const bottomNav = screen.getByRole("navigation", { name: "Primary navigation" });
    fireEvent.click(within(bottomNav).getByRole("button", { name: "Menu" }));
    const menuSheet = await screen.findByRole("dialog");
    expect(
      await within(menuSheet).findByRole("button", { name: /Server health:/ }),
    ).toBeInTheDocument();
    expect(
      await within(menuSheet).findByRole("button", { name: "Open the web player" }),
    ).toBeInTheDocument();
    expect(within(menuSheet).getByRole("button", { name: "Appearance" })).toBeInTheDocument();
    const accountLink = within(menuSheet).getByRole("link", { name: "Your account" });
    expect(accountLink).toBeInTheDocument();
    expect(within(menuSheet).getByRole("button", { name: /Sign out/ })).toBeInTheDocument();

    // Navigating to Your account closes the mobile menu sheet.
    fireEvent.click(accountLink);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByRole("heading", { name: /Alex Rivera/ })).toBeInTheDocument();

    // Re-open Menu and verify Sign out works from the mobile menu sheet.
    fireEvent.click(within(bottomNav).getByRole("button", { name: "Menu" }));
    const menuAgain = await screen.findByRole("dialog");
    const signOut = vi.spyOn(api, "signOut").mockResolvedValue({ ok: true });
    const reload = vi.fn();
    vi.stubGlobal("location", { ...window.location, reload });
    fireEvent.click(within(menuAgain).getByRole("button", { name: /Sign out/ }));
    await waitFor(() => expect(reload).toHaveBeenCalledOnce());
    expect(signOut).toHaveBeenCalledOnce();
    expect(getToken()).toBe("");
  });

  it("opens the search dialog with ⌘K at 430px and closes on Escape", async () => {
    setViewportWidth(430);
    renderApp("/");
    await screen.findByRole("button", { name: "Search Ether" });

    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(await screen.findByRole("dialog", { name: "Search Ether" })).toBeInTheDocument();

    fireEvent.keyDown(document, { key: "Escape" });
    await settleUi(() => {
      expect(screen.queryByRole("dialog", { name: "Search Ether" })).toBeNull();
    });
  });

  it("keeps inline search at 768px while tucking secondary actions into the menu sheet", async () => {
    setViewportWidth(768);
    setToken("remote-owner-token");
    renderApp("/", {
      bootstrap: { authRequired: true, authenticated: true, serverName: "Ether On Air Lab" },
    });

    const header = (await screen.findAllByRole("banner"))[0]!;
    await settleUi(() => {
      expect(within(header).getByPlaceholderText("Search or jump to…")).toBeInTheDocument();
      expect(within(header).getByText("Ether On Air Lab")).toBeInTheDocument();
      expect(within(header).getByRole("button", { name: "Ask Ether" })).toBeInTheDocument();
    });
    expect(within(header).queryByRole("button", { name: /Server health/ })).toBeNull();
    expect(within(header).queryByRole("link", { name: "Your account" })).toBeNull();
    expect(within(header).queryByRole("button", { name: /Sign out/ })).toBeNull();
  });

  it("leaves the desktop header unchanged at 1280px", async () => {
    setViewportWidth(1280);
    setToken("remote-owner-token");
    renderApp("/", {
      bootstrap: { authRequired: true, authenticated: true, serverName: "Ether On Air Lab" },
    });

    const header = (await screen.findAllByRole("banner"))[0]!;
    await settleUi(() => {
      expect(within(header).getByPlaceholderText("Search or jump to…")).toBeInTheDocument();
      expect(within(header).getByText("Ether On Air Lab")).toBeInTheDocument();
      expect(within(header).getByRole("button", { name: /Server health:/ })).toBeInTheDocument();
      expect(
        within(header).getByRole("button", { name: "Open the web player" }),
      ).toBeInTheDocument();
      expect(within(header).getByRole("button", { name: "Ask Ether" })).toBeInTheDocument();
      expect(within(header).getByRole("button", { name: "Appearance" })).toBeInTheDocument();
      expect(within(header).getByRole("link", { name: "Your account" })).toBeInTheDocument();
      expect(within(header).getByRole("button", { name: /Sign out/ })).toBeInTheDocument();
    });
  });
});
