import { fireEvent, render, screen } from "@testing-library/react";
import { ThemeProvider } from "@braedonsaunders/appkit-ui";
import { expect, it } from "vitest";
import { RemixAppearance } from "./RemixAppearance";
import { normalizePalette, paletteScript, PALETTE_KEY } from "./appearance";
import { theme } from "../theme";

it("applies a saved palette before first paint and rejects unknown values", () => {
  localStorage.setItem(PALETTE_KEY, "nord");
  new Function(paletteScript())();
  expect(document.documentElement.dataset.palette).toBe("nord");
  expect(normalizePalette("invalid")).toBe("default");
  localStorage.setItem(PALETTE_KEY, "<script>");
  new Function(paletteScript())();
  expect(document.documentElement.dataset.palette).toBe("default");
});
it("previews and saves a palette while keeping light and dark independently selectable", () => {
  localStorage.clear();
  render(
    <ThemeProvider {...theme}>
      <RemixAppearance />
    </ThemeProvider>,
  );
  fireEvent.click(screen.getByRole("button", { name: /Appearance|Apparence/ }));
  fireEvent.click(screen.getByRole("button", { name: "Solarized" }));
  expect(localStorage.getItem(PALETTE_KEY)).toBe("solarized");
  expect(document.documentElement.dataset.palette).toBe("solarized");
  fireEvent.click(screen.getByRole("button", { name: /Light|Clair/ }));
  expect(document.documentElement.classList.contains("light")).toBe(true);
  expect(document.documentElement.dataset.palette).toBe("solarized");
  fireEvent.keyDown(screen.getByRole("button", { name: "Solarized" }), { key: "Escape" });
  expect(screen.queryByRole("button", { name: "Solarized" })).toBeNull();
});
