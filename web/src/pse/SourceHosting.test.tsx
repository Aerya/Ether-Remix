import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { SourceHosting } from "./SourceHosting";
it("distinguishes a hosted provider from an available self-hosted project", () => {
  render(<SourceHosting source="PSE_MUSIC_ULTRAMAX_URL" publicInstance />);
  expect(screen.getByText(/Public instance|Instance publique/)).toBeTruthy();
  expect(screen.getByRole("link", { name: /GitHub/ })).toHaveAttribute(
    "href",
    "https://github.com/PaRaN01a-hash/ultramax-music",
  );
});
it("does not invent a self-hosting repository for an unknown provider", () => {
  render(<SourceHosting source="unlisted-provider" publicInstance />);
  expect(screen.queryByRole("link")).toBeNull();
});
