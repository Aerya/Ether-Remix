import { fireEvent, render, screen, cleanup, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it } from "vitest";
import { REMIX_UPDATES, RemixUpdates } from "./RemixUpdates";

beforeEach(() => localStorage.clear());

afterEach(() => {
  cleanup();
  localStorage.clear();
});
function dialogs() {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
    configurable: true,
    value: function (this: HTMLDialogElement) {
      this.setAttribute("open", "");
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, "close", {
    configurable: true,
    value: function (this: HTMLDialogElement) {
      this.removeAttribute("open");
    },
  });
}
it("shows an unseen delivery, remembers dismissal and can reopen it", async () => {
  dialogs();
  const first = render(<RemixUpdates />);
  expect(await screen.findByRole("dialog")).toHaveAttribute("open");
  fireEvent.click(screen.getByRole("button", { name: /Got it|J’ai compris/ }));
  expect(localStorage.getItem("ether-remix.updates.seen")).toBe(REMIX_UPDATES[0].id);
  first.unmount();
  render(<RemixUpdates />);
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: /what's new|Nouveautés Ether-Remix/i }));
  expect(await screen.findByRole("dialog")).toHaveAttribute("open");
});
it("shows new notes when an older delivery was dismissed and handles Escape", async () => {
  dialogs();
  localStorage.setItem("ether-remix.updates.seen", REMIX_UPDATES[1].id);
  render(<RemixUpdates />);
  const dialog = await screen.findByRole("dialog");
  fireEvent(dialog, new Event("cancel", { bubbles: true, cancelable: true }));
  await waitFor(() => expect(dialog).not.toHaveAttribute("open"));
  expect(localStorage.getItem("ether-remix.updates.seen")).toBe(REMIX_UPDATES[0].id);
});
