import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { resetRegistryCache } from "../lib/extend";
import { resetStreamState } from "../lib/stream";
import { pushMockStreamEvent, resetMockStream } from "../mock/extend/stream";
import { RowBody } from "../pages/recommendations/RowVisuals";
import { calls, renderApp, settleUi } from "./harness";

afterEach(() => {
  resetRegistryCache();
  resetMockStream();
  resetStreamState();
});

describe("Console labels, search, surface display and stream coalescing", () => {
  it("distinguishes built-in, plugin and Stremio add-on subtitle providers (M4)", async () => {
    renderApp("/services", {}, (fake) => {
      const original = fake.fetch;
      fake.fetch = async (...args) => {
        const res = await original(...args);
        if (!String(args[0]).includes("/extend/registry")) return res;
        const body = await res.json();
        body.descriptors.push(
          {
            kind: "subtitleProvider",
            id: "addon.subdl:subtitles",
            owner: "addon.subdl",
            label: { resolved: "SubDL" },
            meta: { languages: ["en"], formats: ["srt"], stremio: true },
          },
          {
            kind: "subtitleProvider",
            id: "org.example.subs:subtitles",
            owner: "org.example.subs",
            label: { resolved: "Custom Subs" },
            meta: { languages: ["en"], formats: ["srt"] },
          },
        );
        return new Response(JSON.stringify(body), {
          headers: { "content-type": "application/json" },
        });
      };
    });

    await settleUi(() => {
      expect(screen.getAllByRole("article", { name: "SubDL" })).toHaveLength(2);
    });

    const subdlCards = screen.getAllByRole("article", { name: "SubDL" });
    const builtin = subdlCards.find((card) => card.getAttribute("data-app-id") === "subdl")!;
    const stremio = subdlCards.find(
      (card) => card.getAttribute("data-app-id") === "addon.subdl:subtitles",
    )!;
    const plugin = screen.getByRole("article", { name: "Custom Subs" });

    expect(within(builtin).getByText("Built-in")).toBeVisible();
    expect(within(stremio).getByText("Stremio add-on")).toBeVisible();
    expect(within(plugin).getByText("Plugin")).toBeVisible();
  });

  it("finds General (not stale Server tab) and clears ⌘K query on Escape and navigation (m1, m2)", async () => {
    renderApp("/");
    const input = (await screen.findByLabelText("Search Ether")) as HTMLInputElement;

    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "server" } });

    const listbox = await screen.findByRole("listbox");
    await waitFor(() => {
      expect(within(listbox).getByRole("option", { name: /^General\b/ })).toBeInTheDocument();
    });
    expect(within(listbox).queryByRole("option", { name: /^Server\s+General$/ })).toBeNull();

    // Pressing Escape clears the header search query so it does not linger.
    fireEvent.keyDown(input, { key: "Escape" });
    await waitFor(() => {
      expect((screen.getByLabelText("Search Ether") as HTMLInputElement).value).toBe("");
    });

    // Navigating to another route also clears any typed search query.
    const nextInput = screen.getByLabelText("Search Ether") as HTMLInputElement;
    fireEvent.focus(nextInput);
    fireEvent.change(nextInput, { target: { value: "cache" } });
    expect(nextInput.value).toBe("cache");

    fireEvent.click(screen.getByRole("link", { name: "Libraries" }));
    await waitFor(() => {
      expect((screen.getByLabelText("Search Ether") as HTMLInputElement).value).toBe("");
    });
  });

  it("uses the plugin display name on surface rows instead of the raw plugin id (m4)", () => {
    render(
      <RowBody
        row={{
          items: null,
          present: {
            surface: {
              plugin: "org.ether.sports",
              pluginName: "Sports",
              surface: "sports.tonight",
            },
          },
        }}
        showRating={false}
      />,
    );
    expect(screen.getByText(/Drawn by Sports in Vela\./)).toBeInTheDocument();
    expect(screen.queryByText(/org\.ether\.sports/)).toBeNull();
  });

  it("coalesces bursts of sessions.changed stream events into one sessions reload (m10)", async () => {
    const { fake } = renderApp("/");
    await screen.findByRole("button", { name: "Server health: All healthy" });
    await settleUi(() => {
      expect(calls(fake, "GET", "/status/sessions").length).toBeGreaterThanOrEqual(1);
    });

    const before = calls(fake, "GET", "/status/sessions").length;
    for (let i = 0; i < 15; i++) {
      pushMockStreamEvent("sessions.changed", { count: i % 2 });
    }

    await waitFor(() => {
      expect(calls(fake, "GET", "/status/sessions").length).toBe(before + 1);
    });
    // Allow any stray timers to settle and verify no extra burst reloads happened.
    await new Promise((r) => setTimeout(r, 150));
    expect(calls(fake, "GET", "/status/sessions").length).toBe(before + 1);
  });
});
