import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { expect, it } from "vitest";
import type { FakeServer } from "../mock/server";
import { calls, renderApp } from "./harness";

const WAIT = { timeout: 4000 };

type Route = (method: string, path: string, body: unknown) => unknown;

/** Serve this test's stream-preference fixtures ahead of the shared fake. */
function extraRoutes(route: Route) {
  return (fake: FakeServer) => {
    const base = fake.fetch;
    fake.fetch = async (input, init) => {
      const url = new URL(String(input), "http://localhost");
      const path = url.pathname.replace(/^\/ether\/api/, "") + url.search;
      const raw = typeof init?.body === "string" && init.body ? init.body : "";
      const method = (init?.method ?? "GET").toUpperCase();
      const body = raw ? JSON.parse(raw) : null;
      const out = route(method, path, body);
      if (out === undefined) return base(input, init);
      fake.state.calls.push({ method, path, body });
      return new Response(JSON.stringify(out), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    };
  };
}

async function pickLanguage(pickerLabel: string, languageName: string) {
  fireEvent.click(screen.getByRole("button", { name: pickerLabel }));
  const search = await screen.findByRole("textbox", { name: "Search…" }, WAIT);
  fireEvent.change(search, { target: { value: languageName } });
  fireEvent.click(await screen.findByRole("option", { name: languageName }, WAIT));
}

it("opens an empty installation with nullable language lists and saves ordered language picks", async () => {
  let saved: Record<string, unknown> | undefined;
  const { fake } = renderApp(
    "/playback/subtitles",
    {},
    extraRoutes((method, path, body) => {
      if (path !== "/stream-preferences/0") return undefined;
      if (method === "PUT") {
        saved = body as Record<string, unknown>;
        return saved;
      }
      return {
        audioLanguages: null,
        subtitleLanguages: null,
        subtitleMode: "off",
        subtitleStyle: "normal",
      };
    }),
  );
  const audio = await screen.findByRole("button", { name: "Audio languages" }, WAIT);
  await waitFor(() => expect(audio).toBeEnabled());
  expect(audio).toHaveTextContent("Any language");
  expect(screen.queryByRole("list", { name: "Audio languages order" })).toBeNull();

  await pickLanguage("Audio languages", "French");
  await pickLanguage("Audio languages", "English");
  const orderList = screen.getByRole("list", { name: "Audio languages order" });
  expect(within(orderList).getAllByRole("listitem")).toHaveLength(2);
  fireEvent.click(within(orderList).getByRole("button", { name: "Move English earlier" }));

  fireEvent.click(await screen.findByRole("button", { name: /Save changes/ }));
  await waitFor(() => expect(saved?.audioLanguages).toEqual(["en", "fr"]), WAIT);
  expect(saved?.subtitleLanguages).toEqual([]);
  expect(calls(fake, "PUT", "/stream-preferences/0")).toHaveLength(1);
  expect(await screen.findByText("Audio and subtitles saved", {}, WAIT)).toBeInTheDocument();
  await waitFor(() => expect(screen.queryByText("You have unsaved changes")).toBeNull());
});

it("loads a viewer's override and restores both language fields from the defaults", async () => {
  let overridden = true;
  const { fake } = renderApp(
    "/viewers/2/playback",
    {},
    extraRoutes((method, path) => {
      if (path === "/stream-preferences/0")
        return { audioLanguages: ["en"], subtitleLanguages: ["fr"] };
      if (path === "/stream-preferences/2/shows")
        return [
          {
            showId: 77,
            showTitle: "Slow Horses",
            kind: "subtitle",
            choice: { language: "en", forced: false, sdh: true, off: false },
          },
        ];
      if (path === "/stream-preferences/2") {
        if (method === "DELETE") {
          overridden = false;
          return { ok: true };
        }
        return overridden
          ? { audioLanguages: ["ja"], subtitleLanguages: ["en"] }
          : { audioLanguages: ["en"], subtitleLanguages: ["fr"] };
      }
      if (method === "DELETE" && path === "/stream-preferences/2/shows/77") return { ok: true };
      return undefined;
    }),
  );
  const audioOrder = await screen.findByRole("list", { name: "Audio languages order" }, WAIT);
  const subtitleOrder = screen.getByRole("list", { name: "Subtitle languages order" });
  expect(audioOrder).toHaveTextContent("Japanese");
  expect(subtitleOrder).toHaveTextContent("English");
  expect(screen.getByText("Custom for this viewer")).toBeInTheDocument();
  expect(screen.getByText("Slow Horses")).toBeInTheDocument();
  // Show override uses human-readable language name rather than raw ISO code.
  expect(screen.getByText(/English · SDH/)).toBeInTheDocument();

  await pickLanguage("Audio languages", "German");
  fireEvent.click(screen.getByRole("button", { name: /Use default/ }));
  await waitFor(() => expect(calls(fake, "DELETE", "/stream-preferences/2")).toHaveLength(1), WAIT);
  await waitFor(
    () =>
      expect(screen.getByRole("list", { name: "Audio languages order" })).toHaveTextContent(
        "English",
      ),
    WAIT,
  );
  expect(screen.getByRole("list", { name: "Subtitle languages order" })).toHaveTextContent(
    "French",
  );
  expect(screen.getByText("Uses default")).toBeInTheDocument();
  expect(await screen.findByText("Now using the defaults", {}, WAIT)).toBeInTheDocument();
  expect(calls(fake, "PUT", "/stream-preferences/2")).toHaveLength(0);

  fireEvent.click(screen.getByRole("button", { name: "Forget" }));
  await waitFor(() => expect(screen.queryByText("Slow Horses")).toBeNull(), WAIT);
  expect(calls(fake, "DELETE", "/stream-preferences/2/shows/77")).toHaveLength(1);
});

it("saves a custom override for one viewer", async () => {
  let saved: Record<string, unknown> | undefined;
  renderApp(
    "/viewers/2/playback",
    {},
    extraRoutes((method, path, body) => {
      if (path === "/stream-preferences/0")
        return { audioLanguages: ["en"], subtitleLanguages: [] };
      if (path === "/stream-preferences/2/shows") return [];
      if (path === "/stream-preferences/2") {
        if (method === "PUT") saved = body as Record<string, unknown>;
        return saved ?? { audioLanguages: ["en"], subtitleLanguages: [] };
      }
      return undefined;
    }),
  );
  expect(await screen.findByText("Uses default", {}, WAIT)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Show subtitles" })).toHaveTextContent(
    "Off unless I turn them on",
  );
  await pickLanguage("Subtitle languages", "Spanish");
  fireEvent.click(screen.getByRole("button", { name: "Show subtitles" }));
  fireEvent.click(await screen.findByRole("option", { name: "Only for foreign audio" }));
  fireEvent.click(screen.getByRole("button", { name: "More options: Subtitle appearance" }));
  fireEvent.click(screen.getByRole("button", { name: "Size" }));
  fireEvent.click(await screen.findByRole("option", { name: "Small" }));
  fireEvent.change(screen.getByLabelText("Text colour"), { target: { value: "#ffffff" } });
  fireEvent.click(screen.getByRole("button", { name: /Save changes/ }));
  await waitFor(() => expect(saved?.subtitleLanguages).toEqual(["es"]), WAIT);
  expect(saved?.subtitleMode).toBe("foreign-only");
  expect(saved?.subtitleAppearance).toMatchObject({
    fontSize: "small",
    fontColor: "#ffffff",
    position: "bottom",
  });
  expect(saved?.audioLanguages).toEqual(["en"]);
  expect(await screen.findByText("Custom for this viewer", {}, WAIT)).toBeInTheDocument();
});
