import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { AddonProxyFields } from "./AddonProxyFields";
const p = { enabled: true, url: "http://vpn:8888", username: "", password: "" };
it("fills both request and playback proxies from the single block", () => {
  const change = vi.fn();
  render(<AddonProxyFields onChange={change} />);
  fireEvent.click(screen.getByRole("switch", { name: "Addon proxy · requests and playback" }));
  const v = change.mock.calls[0]![0];
  expect(v.requestProxy.enabled).toBe(true);
  expect(v.streamProxy).toEqual(v.requestProxy);
  expect(screen.queryByRole("status")).toBeNull();
});
it("warns when both proxies differ", () => {
  render(<AddonProxyFields requestProxy={p} onChange={vi.fn()} />);
  expect(screen.getByRole("status")).toHaveTextContent("differ");
});
