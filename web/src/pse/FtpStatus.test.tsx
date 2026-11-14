import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { apiFetch } from "../lib/api";
import { FtpStatus } from "./FtpStatus";
vi.mock("../lib/api", () => ({ apiFetch: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});
const state = {
  protocol: "sftp",
  server: "example.test:22",
  mode: "stream",
  writes: false,
  files: 1,
  movies: 1,
  episodes: 0,
  reading: 0,
  tasks: [],
};
it("allows testing a disabled source without accepting its fingerprint", async () => {
  vi.mocked(apiFetch).mockResolvedValue({
    ok: false,
    message: "Verify the fingerprint",
    fingerprint: "SHA256:test",
  });
  render(<FtpStatus status={{ active: false }} />);
  fireEvent.click(screen.getByRole("button", { name: /Test connection|Tester la connexion/ }));
  expect(await screen.findByText("SHA256:test")).toBeTruthy();
  expect(apiFetch).toHaveBeenCalledTimes(1);
  expect(vi.mocked(apiFetch).mock.calls[0]?.[0]).toBe("/pse/ftp/test");
});
it("never offers a disk copy in streaming mode even with an outdated search response", async () => {
  vi.mocked(apiFetch).mockResolvedValue({
    ok: true,
    writes: true,
    results: [
      { id: "a", name: "Film.2020", path: "/film.mkv", size: 40000000, state: "available" },
    ],
  });
  render(<FtpStatus status={{ active: true, state }} />);
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "Film" } });
  fireEvent.click(screen.getByRole("button", { name: /^(Search|Chercher)$/ }));
  expect(await screen.findByText("Film.2020")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Copy to disk|Copier sur le disque/ })).toBeNull();
});
