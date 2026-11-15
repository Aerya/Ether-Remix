import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, it, expect } from "vitest";
import { SourceProxyFields } from "../pse/SourceProxyFields";
import type { SourceProxy } from "../lib/types";
function Harness() {
  const [p, set] = useState<SourceProxy>({ enabled: false, url: "", password: "" });
  return (
    <>
      <SourceProxyFields title="Account proxy" value={p} onChange={set} />
      <output data-testid="value">{JSON.stringify(p)}</output>
    </>
  );
}
describe("source proxy form", () => {
  it("inherits until enabled and imports authenticated URLs into separate fields", () => {
    render(<Harness />);
    fireEvent.click(screen.getByLabelText("Account proxy"));
    fireEvent.paste(screen.getByLabelText("Proxy address"), {
      clipboardData: { getData: () => "http://relay:pass%40word@proxy:8888" },
    });
    expect(screen.getByLabelText("Proxy address")).toHaveValue("http://proxy:8888");
    expect(screen.getByLabelText("Proxy username")).toHaveValue("relay");
    expect(screen.getByLabelText("Proxy password")).toHaveValue("pass@word");
    fireEvent.click(screen.getByLabelText("Account proxy"));
    expect(JSON.parse(screen.getByTestId("value").textContent!)).toMatchObject({
      enabled: false,
      url: "http://proxy:8888",
      password: "pass@word",
    });
  });
  it("rejects malformed pasted credentials without displaying their value", () => {
    render(<Harness />);
    fireEvent.click(screen.getByLabelText("Account proxy"));
    fireEvent.paste(screen.getByLabelText("Proxy address"), {
      clipboardData: { getData: () => "http://relay:do-not-expose@proxy:8888/path" },
    });
    expect(screen.getByRole("alert")).toHaveTextContent("Invalid URL");
    expect(screen.getByLabelText("Proxy address")).toHaveValue("");
    expect(screen.queryByText(/do-not-expose/)).not.toBeInTheDocument();
  });
});
