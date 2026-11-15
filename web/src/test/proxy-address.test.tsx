import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { importProxyAddress } from "../lib/proxy-address";
import { OutboundInternet } from "../pages/settings/OutboundInternet";
import { api } from "../lib/api";
import type { EtherConfig } from "../lib/types";

describe("authenticated proxy import", () => {
  it.each(["http", "https", "socks5"])(
    "imports %s credentials including encoded delimiters",
    (scheme) => {
      expect(
        importProxyAddress(`${scheme}://relay:p%40ss%3Aword%25@ssdv2-vpn-example:8888`),
      ).toEqual({
        url: `${scheme}://ssdv2-vpn-example:8888`,
        username: "relay",
        password: "p@ss:word%",
      });
    },
  );
  it("keeps explicit default ports and IPv6", () => {
    expect(importProxyAddress("http://u:p@[::1]:80/")?.url).toBe("http://[::1]:80");
    expect(importProxyAddress("https://u:p@proxy:443")?.url).toBe("https://proxy:443");
    expect(importProxyAddress("http://u@proxy:8888")?.password).toBe("");
  });
  it("leaves manual addresses and credentials alone", () => {
    expect(importProxyAddress("http://proxy:8888")).toBeNull();
  });
  it.each([
    "http://u:secret@proxy",
    "http://u:secret@proxy:8888/path",
    "ftp://u:secret@proxy:8888",
    "http://u:%ZZ@proxy:8888",
    "http://u:secret%0A@proxy:8888",
    "http://u:secret@proxy:8888?x=1",
  ])("rejects invalid input without exposing secrets", (value) => {
    expect(() => importProxyAddress(value)).toThrow("Invalid authenticated proxy URL");
  });
  it("saves split credentials while preserving MediaFlow and route choices", async () => {
    const outbound = {
      enabled: true,
      url: "http://old:8888",
      username: "old",
      password: "••••",
      indexers: false,
      stremio: true,
      debrid: true,
      usenet: false,
      metadata: false,
      allDebridMediaFlow: { enabled: true, url: "http://mediaflow:8888", password: "••••" },
    };
    vi.spyOn(api, "getConfig").mockResolvedValue({ outbound } as EtherConfig);
    const put = vi.spyOn(api, "putSection").mockResolvedValue(undefined as never);
    const check = vi
      .spyOn(api, "checkOutbound")
      .mockResolvedValue({ proxyEnabled: true, sourceIp: "198.51.100.2" });
    render(<OutboundInternet />);
    const address = await screen.findByLabelText("Proxy address");
    // A malformed paste must not replace the old address or display its secret.
    fireEvent.paste(address, {
      clipboardData: { getData: () => "http://relay:private-secret@proxy:8888/path" },
    });
    expect(address).toHaveValue("http://old:8888");
    expect(screen.queryByText(/private-secret/)).not.toBeInTheDocument();
    fireEvent.paste(address, {
      clipboardData: { getData: () => "http://relay:test%40password@ssdv2-vpn-example:8888" },
    });
    expect(address).toHaveValue("http://ssdv2-vpn-example:8888");
    expect(await screen.findByLabelText("Username")).toHaveValue("relay");
    fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
    await waitFor(() =>
      expect(check).toHaveBeenCalledWith(
        expect.objectContaining({
          username: "relay",
          password: "test@password",
          allDebridMediaFlow: outbound.allDebridMediaFlow,
        }),
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        "outbound",
        expect.objectContaining({
          url: "http://ssdv2-vpn-example:8888",
          username: "relay",
          password: "test@password",
          indexers: false,
          usenet: false,
          allDebridMediaFlow: outbound.allDebridMediaFlow,
        }),
      ),
    );
    vi.restoreAllMocks();
  });
});
