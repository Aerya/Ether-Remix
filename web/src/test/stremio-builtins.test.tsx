import { fireEvent, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderApp } from "./harness";

describe("Stremio built-ins", () => {
  it("adds a subtitle add-on or plugin from Integrations, not from Playback", async () => {
    renderApp("/services/overview");
    fireEvent.click(await screen.findByRole("button", { name: "Add subtitle provider" }));
    const choices = await screen.findByRole("dialog", { name: "Add subtitle provider" });
    expect(within(choices).getByRole("link", { name: /Plugin/ })).toHaveAttribute(
      "href",
      "/plugins/marketplace",
    );
    fireEvent.click(within(choices).getByRole("button", { name: /Stremio add-on/ }));
    expect(await screen.findByRole("dialog", { name: "Add a Stremio add-on" })).toBeInTheDocument();
    expect(screen.queryByLabelText("Plugin URL")).not.toBeInTheDocument();
  });

  it("adds a Live TV plugin through the shared plugin drawer", async () => {
    renderApp("/livetv");
    fireEvent.click(await screen.findByRole("button", { name: "Add" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Stremio plugin/ }));
    expect(await screen.findByText("Install link")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("https://… /manifest.json")).toBeInTheDocument();
    expect(screen.queryByLabelText("Plugin URL")).not.toBeInTheDocument();
  });
});
