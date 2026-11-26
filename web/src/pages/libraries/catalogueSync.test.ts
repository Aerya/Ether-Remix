import { afterEach, describe, expect, it, vi } from "vitest";
import { apiFetch } from "../../lib/api";
import { librariesApi } from "./api";
vi.mock("../../lib/api", () => ({ apiFetch: vi.fn() }));
afterEach(() => {
  vi.useRealTimers();
  vi.resetAllMocks();
});
describe("catalogue synchronization", () => {
  it("starts once and polls until completion", async () => {
    vi.useFakeTimers();
    vi.mocked(apiFetch)
      .mockResolvedValueOnce({ running: true })
      .mockResolvedValueOnce({ running: true })
      .mockResolvedValueOnce({ running: false, itemCount: 500, listed: 520, limit: 500 });
    const result = librariesApi.syncCatalogueSource("stremio:test:movie");
    await vi.advanceTimersByTimeAsync(3000);
    await expect(result).resolves.toEqual({ ok: true, itemCount: 500, listed: 520, limit: 500 });
    expect(apiFetch).toHaveBeenCalledTimes(3);
    expect(apiFetch).toHaveBeenNthCalledWith(
      1,
      "/libraries/catalogue-sources/stremio%3Atest%3Amovie/sync",
      { method: "POST" },
    );
    expect(apiFetch).toHaveBeenNthCalledWith(
      2,
      "/libraries/catalogue-sync?source=stremio%3Atest%3Amovie",
    );
  });
  it("reports background failure instead of claiming success", async () => {
    vi.useFakeTimers();
    vi.mocked(apiFetch)
      .mockResolvedValueOnce({ running: true })
      .mockResolvedValueOnce({ running: false, error: "upstream unavailable" });
    const result = expect(librariesApi.syncCatalogueSource("one")).rejects.toThrow(
      "upstream unavailable",
    );
    await vi.advanceTimersByTimeAsync(1500);
    await result;
  });
});
