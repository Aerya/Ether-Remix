import { fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { expect, it, vi } from "vitest";
import { MusicSourceSettings, musicSourceGroup } from "./MusicSourceSettings";
vi.mock("../lib/api", () => ({
  api: {
    getConfig: async () => ({
      debrid: {
        providers: [
          { id: "rd", name: "RD account", kind: "realdebrid", enabled: true, apiKey: "••••" },
        ],
      },
    }),
  },
}));
vi.mock("./MusicAddons", () => ({
  MusicAddons: ({ fields }: { fields: { key: string; control: React.ReactNode }[] }) => (
    <div>
      {fields.map((f) => (
        <div key={f.key}>{f.control}</div>
      ))}
    </div>
  ),
}));
vi.mock("./MusicSources", () => ({ MusicSources: () => <p>Diagnostic fixture</p> }));
it("keeps Eclipse settings, DMM and Real-Debrid together, and other sources separate", async () => {
  const keys = [
    "PSE_MUSIC_ECLIPSE_URL",
    "PSE_MUSIC_ADDON_REGISTRY",
    "PSE_MUSIC_DMM",
    "PSE_MUSIC_REALDEBRID",
    "PSE_MUSIC_ALLDEBRID",
    "PSE_MUSIC_TORBOX",
    "PSE_MUSIC_AUDIUS",
    "PSE_MUSIC_HIFI_URL",
    "COMMUNITY",
  ];
  render(
    <MemoryRouter>
      <MusicSourceSettings
        status={<p>Music status</p>}
        fields={keys.map((key) => ({
          key,
          community: key === "COMMUNITY",
          advanced: key === "PSE_MUSIC_HIFI_URL",
          control: <input aria-label={key} />,
        }))}
      />
    </MemoryRouter>,
  );
  await screen.findByText("RD account");
  for (const key of keys) {
    const control = screen.getByLabelText(key);
    expect(screen.getAllByLabelText(key)).toHaveLength(1);
    const group = musicSourceGroup(key, key === "COMMUNITY");
    expect(control.closest('[id^="music-"]')?.id).toBe(`music-${group}`);
  }
  fireEvent.click(screen.getByRole("tab", { name: "DMM / Real-Debrid" }));
  expect(
    within(screen.getByText("RD account").closest("#music-realdebrid") as HTMLElement).getByRole(
      "link",
    ),
  ).toHaveAttribute("href", "/accent/sources?source=debrid");
  expect(screen.queryByDisplayValue("••••")).toBeNull();
  expect(screen.getByText("Diagnostic fixture")).toBeTruthy();
});
