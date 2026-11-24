import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AllDebridMediaFlow } from "../pse/AllDebridMediaFlow";
import { api } from "../lib/api";
import type { EtherConfig } from "../lib/types";

describe("AllDebrid MediaFlow", () => {
  it("tests the saved AllDebrid relay and blocks testing unsaved changes", async () => {
    const account = {
      id: "ad",
      name: "AllDebrid",
      kind: "alldebrid",
      enabled: true,
      apiKey: "••••",
      priority: 0,
    } as const;
    const get = vi.spyOn(api, "getConfig").mockResolvedValue({
      debrid: { providers: [account] },
      outbound: {
        allDebridMediaFlow: { enabled: true, url: "http://mediaflow:8888", password: "••••" },
      },
    } as EtherConfig);
    const test = vi.spyOn(api, "testDebrid").mockResolvedValue({ ok: true, durationMs: 30 });
    render(<AllDebridMediaFlow />);
    const button = await screen.findByRole("button", { name: "Test AllDebrid through MediaFlow" });
    await waitFor(() => expect(button).toBeEnabled());
    fireEvent.click(button);
    expect(await screen.findByText(/Connection successful/)).toBeInTheDocument();
    expect(test).toHaveBeenCalledWith(account);
    test.mockResolvedValueOnce({ ok: false, error: "private upstream details", durationMs: 30 });
    fireEvent.click(button);
    expect(await screen.findByText(/Test through MediaFlow failed/)).toBeInTheDocument();
    expect(screen.queryByText(/private upstream details/)).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("MediaFlow URL"), {
      target: { value: "http://other:8888" },
    });
    expect(button).toBeDisabled();
    expect(screen.getByRole("link", { name: /Compose and Remix setup/ })).toHaveAttribute(
      "href",
      "https://github.com/Remix-Media/Ether-Remix/blob/main/docs/alldebrid-mediaflow.md#installation",
    );
    get.mockRestore();
    test.mockRestore();
  });
  it("requires relay credentials and preserves the other outbound settings", async () => {
    const outbound = {
      enabled: true,
      url: "http://proxy:8888",
      indexers: true,
      stremio: true,
      debrid: true,
      usenet: false,
      metadata: false,
    };
    const get = vi.spyOn(api, "getConfig").mockResolvedValue({ outbound } as EtherConfig);
    const put = vi.spyOn(api, "putSection").mockResolvedValue(undefined as never);
    render(<AllDebridMediaFlow />);
    const button = await screen.findByRole("button", { name: "Save connection" });
    await waitFor(() => expect(button).toBeEnabled());
    fireEvent.click(screen.getByRole("checkbox", { name: "Use MediaFlow for AllDebrid" }));
    expect(button).toBeDisabled();
    fireEvent.change(screen.getByLabelText("MediaFlow URL"), {
      target: { value: "http://mediaflow:8888" },
    });
    fireEvent.change(screen.getByLabelText("MediaFlow API password"), {
      target: { value: "relay-password" },
    });
    fireEvent.click(button);
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("outbound", {
        ...outbound,
        allDebridMediaFlow: {
          enabled: true,
          url: "http://mediaflow:8888",
          password: "relay-password",
        },
      }),
    );
    expect(screen.getByRole("link", { name: /affiliate link/ })).toHaveAttribute(
      "href",
      "https://airvpn.org/?referred_by=483746",
    );
    expect(screen.getByText(/Cloudflare WARP/)).toBeInTheDocument();
    get.mockRestore();
    put.mockRestore();
  });
});
