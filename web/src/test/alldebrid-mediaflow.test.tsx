import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AllDebridMediaFlow } from "../pse/AllDebridMediaFlow";
import { api } from "../lib/api";
import type { EtherConfig } from "../lib/types";

describe("AllDebrid MediaFlow", () => {
  it("requires relay credentials and preserves the other outbound settings", async () => {
    const outbound = { enabled: true, url: "http://proxy:8888", indexers: true, stremio: true, debrid: true, usenet: false, metadata: false };
    const get = vi.spyOn(api, "getConfig").mockResolvedValue({ outbound } as EtherConfig);
    const put = vi.spyOn(api, "putSection").mockResolvedValue(undefined as never);
    render(<AllDebridMediaFlow />);
    const button = await screen.findByRole("button", { name: "Save connection" });
    await waitFor(() => expect(button).toBeEnabled());
    fireEvent.click(screen.getByRole("checkbox", { name: "Use MediaFlow for AllDebrid" }));
    expect(button).toBeDisabled();
    fireEvent.change(screen.getByLabelText("MediaFlow URL"), { target: { value: "http://mediaflow:8888" } });
    fireEvent.change(screen.getByLabelText("MediaFlow API password"), { target: { value: "relay-password" } });
    fireEvent.click(button);
    await waitFor(() => expect(put).toHaveBeenCalledWith("outbound", { ...outbound, allDebridMediaFlow: { enabled: true, url: "http://mediaflow:8888", password: "relay-password" } }));
    expect(screen.getByRole("link", { name: /affiliate link/ })).toHaveAttribute("href", "https://airvpn.org/?referred_by=483746");
    expect(screen.getByText(/Cloudflare WARP/)).toBeInTheDocument();
    get.mockRestore(); put.mockRestore();
  });
});
