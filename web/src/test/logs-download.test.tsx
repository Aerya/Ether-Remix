import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setToken } from "../lib/api";
import Logs from "../pages/Logs";

afterEach(() => {
  cleanup();
  setToken("");
  vi.unstubAllGlobals();
});

describe("Logs download and mobile/support ergonomics", () => {
  it("fetches the scrubbed bundle with the console session and saves it", async () => {
    setToken("ether-admin-logs");
    const calls: { url: string; init?: RequestInit }[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        calls.push({ url, init });
        if (url.endsWith("/logs/download"))
          return new Response("# Ether logs\n", { headers: { "Content-Type": "text/plain" } });
        return new Response(JSON.stringify({ entries: [], total: 0 }), {
          headers: { "Content-Type": "application/json" },
        });
      }),
    );
    const createObjectURL = vi.fn(() => "blob:logs");
    Object.assign(URL, { createObjectURL, revokeObjectURL: vi.fn() });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    render(
      <MemoryRouter>
        <Logs />
      </MemoryRouter>,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Download logs" }));
    await waitFor(() => expect(click).toHaveBeenCalledOnce());
    const req = calls.find((c) => c.url === "/ether/api/logs/download")!;
    // Authenticated like every console request (the header name is the API's).
    expect([...new Headers(req.init?.headers).values()]).toContain("ether-admin-logs");
    expect(createObjectURL).toHaveBeenCalledOnce();
    click.mockRestore();
  });

  it("opens Record a problem with styled pickers and friendly category labels", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        if (url.endsWith("/support/devices"))
          return new Response(
            JSON.stringify({
              devices: [{ id: "tv-1", name: "Living Room TV", product: "Plex for Apple TV" }],
            }),
            { headers: { "Content-Type": "application/json" } },
          );
        return new Response(
          JSON.stringify({
            entries: [
              {
                id: 1,
                time: "2026-11-18T03:00:00Z",
                level: "debug",
                message: "usenet connection pool ready",
                attrs: { host: "news.example.invalid" },
              },
            ],
            total: 1,
          }),
          { headers: { "Content-Type": "application/json" } },
        );
      }),
    );
    render(
      <MemoryRouter>
        <Logs />
      </MemoryRouter>,
    );
    expect(await screen.findByText("usenet connection pool ready")).toHaveClass("basis-full");
    expect(screen.getByRole("button", { name: "More actions" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Record a problem" }));
    const dialog = await screen.findByRole("dialog", { name: "Record a problem" });
    expect(dialog.querySelectorAll("select")).toHaveLength(0);

    const devicePicker = within(dialog).getByRole("button", { name: "Device" });
    expect(devicePicker).toHaveTextContent("I don't know which device");

    const categoryPicker = within(dialog).getByRole("button", { name: "Category" });
    expect(categoryPicker).toHaveTextContent("Playback");
    fireEvent.click(categoryPicker);
    for (const label of ["Playback", "Picture", "Subtitles", "Library", "Other"]) {
      expect(await screen.findByRole("option", { name: label })).toBeInTheDocument();
    }
    fireEvent.click(screen.getByRole("option", { name: "Subtitles" }));
    expect(categoryPicker).toHaveTextContent("Subtitles");
  });
});
