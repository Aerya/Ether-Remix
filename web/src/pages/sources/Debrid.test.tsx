import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { DebridProvider, DebridProviderStatus } from "../../lib/types";
import { StateBadge, visibleLastError } from "./Debrid";

const provider: DebridProvider = {
  id: "ad",
  name: "AllDebrid",
  kind: "alldebrid",
  apiKey: "",
  priority: 1,
  enabled: true,
};

function status(over: Partial<DebridProviderStatus>): DebridProviderStatus {
  return { id: "ad", name: "AllDebrid", kind: "alldebrid", enabled: true, priority: 1, state: "ok", ...over };
}

describe("debrid quota badge", () => {
  it("shows the quota pause with the local time of the next try", () => {
    const until = new Date(Date.now() + 3600_000);
    const clock = until.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    render(<StateBadge p={provider} s={status({ state: "quota_exceeded", backoffUntil: until.toISOString() })} />);
    const text = screen.getByText(/Quota reached/).textContent ?? "";
    expect(text).toContain(clock);
  });

  it("still says the quota is reached without a retry time", () => {
    render(<StateBadge p={provider} s={status({ state: "quota_exceeded" })} />);
    expect(screen.getByText("Quota reached")).toBeTruthy();
  });
});

describe("debrid last error", () => {
  it("is hidden once the account is back to ok", () => {
    expect(visibleLastError(status({ state: "ok", lastError: "daily download limit" }))).toBe("");
    expect(visibleLastError(status({ state: "quota_exceeded", lastError: "daily download limit" }))).toBe(
      "daily download limit",
    );
    expect(visibleLastError(undefined)).toBe("");
  });
});
