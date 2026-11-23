import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { RatingSources, type RatingSettings } from "../pages/metadata/Ratings";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const settings: RatingSettings = {
  enabled: true,
  provider: "omdb",
  apiKey: "[configured]",
  backupProvider: "mdblist",
  backupApiKey: "[configured]",
  critic: "tomatoes",
  audience: "imdb",
  missing: "none",
  cacheHours: 24,
};

const LABELS: Record<string, string> = {
  "": "None",
  omdb: "OMDb",
  mdblist: "MDBList",
};

async function chooseProvider(label: string, value: string) {
  fireEvent.click(await screen.findByRole("button", { name: label }));
  fireEvent.click(await screen.findByRole("option", { name: LABELS[value] ?? value }));
}

it.each([false, true])(
  "swaps and saves masked keys both ways (controlled=%s)",
  async (controlled) => {
    const writes: RatingSettings[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: unknown, init?: RequestInit) => {
        const value = init?.method === "PUT" ? JSON.parse(String(init.body)) : settings;
        if (init?.method === "PUT") writes.push(value);
        return new Response(JSON.stringify(value), { status: 200 });
      }),
    );
    function Form() {
      const [saved, setSaved] = useState(settings);
      return (
        <RatingSources keysOnly saved={controlled ? saved : undefined} onPersisted={setSaved} />
      );
    }
    render(
      <MemoryRouter>
        <Form />
      </MemoryRouter>,
    );
    for (const provider of ["mdblist", "omdb"]) {
      await chooseProvider("Provider", provider);
      expect(screen.getByRole("button", { name: "Backup ratings provider" })).toHaveTextContent(
        provider === "omdb" ? "MDBList" : "OMDb",
      );
      expect(screen.getByLabelText("API key")).toHaveValue("[configured]");
      expect(screen.getByLabelText("Backup API key")).toHaveValue("[configured]");
      fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
      await waitFor(() => expect(writes.at(-1)?.provider).toBe(provider));
      expect(writes.at(-1)).toEqual({
        ...settings,
        provider,
        backupProvider: provider === "omdb" ? "mdblist" : "omdb",
      });
      await waitFor(() =>
        expect(screen.queryByRole("button", { name: "Save changes" })).not.toBeInTheDocument(),
      );
    }
  },
);

it("retains provider-specific references when removing and reselecting backup before saving", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(settings))),
  );
  render(
    <MemoryRouter>
      <RatingSources keysOnly />
    </MemoryRouter>,
  );
  await chooseProvider("Backup ratings provider", "");
  expect(screen.queryByLabelText("Backup API key")).not.toBeInTheDocument();
  await chooseProvider("Backup ratings provider", "mdblist");
  expect(screen.getByLabelText("Backup API key")).toHaveValue("[configured]");
});

it("moves explicit draft keys with their providers and preserves a deliberate clear", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(settings))),
  );
  render(
    <MemoryRouter>
      <RatingSources keysOnly saved={settings} />
    </MemoryRouter>,
  );
  fireEvent.change(screen.getByLabelText("API key"), { target: { value: "replacement-omdb" } });
  fireEvent.change(screen.getByLabelText("Backup API key"), { target: { value: "" } });
  await chooseProvider("Provider", "mdblist");
  expect(screen.getByLabelText("API key")).toHaveValue("");
  expect(screen.getByLabelText("Backup API key")).toHaveValue("replacement-omdb");
  await chooseProvider("Provider", "omdb");
  expect(screen.getByLabelText("API key")).toHaveValue("replacement-omdb");
  expect(screen.getByLabelText("Backup API key")).toHaveValue("");
});

it("leaves a new provider's key empty when only the other provider is saved", async () => {
  const single = { ...settings, backupProvider: "", backupApiKey: "" };
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(single))),
  );
  render(
    <MemoryRouter>
      <RatingSources keysOnly saved={single} />
    </MemoryRouter>,
  );
  await chooseProvider("Provider", "mdblist");
  expect(screen.getByLabelText("API key")).toHaveValue("");
  expect(screen.getByRole("button", { name: "Backup ratings provider" })).toHaveTextContent("None");
  await chooseProvider("Backup ratings provider", "omdb");
  expect(screen.getByLabelText("Backup API key")).toHaveValue("[configured]");
});

it.each(["replacement-mdblist", ""])(
  "preserves a backup draft %s across removal and reselection",
  async (key) => {
    render(
      <MemoryRouter>
        <RatingSources keysOnly saved={settings} />
      </MemoryRouter>,
    );
    fireEvent.change(screen.getByLabelText("Backup API key"), { target: { value: key } });
    await chooseProvider("Backup ratings provider", "");
    await chooseProvider("Backup ratings provider", "mdblist");
    expect(screen.getByLabelText("Backup API key")).toHaveValue(key);
  },
);

it("forgets removed provider drafts on discard", async () => {
  render(
    <MemoryRouter>
      <RatingSources keysOnly saved={settings} />
    </MemoryRouter>,
  );
  fireEvent.change(screen.getByLabelText("Backup API key"), { target: { value: "replacement" } });
  await chooseProvider("Backup ratings provider", "");
  fireEvent.click(screen.getByRole("button", { name: "Discard" }));
  await chooseProvider("Backup ratings provider", "");
  await chooseProvider("Backup ratings provider", "mdblist");
  expect(screen.getByLabelText("Backup API key")).toHaveValue("[configured]");
});
