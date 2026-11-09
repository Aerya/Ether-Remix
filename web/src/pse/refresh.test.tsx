import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PseRefreshField } from "./RefreshField";
import { pseRefreshEvery, pseRefreshHours, pseRefreshLabel } from "./refresh";

describe("cadence libre des collections", () => {
  it("lit et borne « Nh »", () => {
    expect(pseRefreshHours("6h")).toBe(6);
    expect(pseRefreshHours("daily")).toBe(0);
    expect(pseRefreshHours("0h")).toBe(0);
    expect(pseRefreshHours("169h")).toBe(0);
    expect(pseRefreshEvery(0)).toBe("1h");
    expect(pseRefreshEvery(500)).toBe("168h");
    expect(pseRefreshLabel("hourly")).toBe("");
    expect(pseRefreshLabel("6h")).toMatch(/6/);
  });

  it("propose « toutes les N h » et règle le nombre d'heures", () => {
    const onChange = vi.fn();
    const { rerender } = render(
      <PseRefreshField label="Refresh" refresh="daily" onChange={onChange} />,
    );
    fireEvent.click(screen.getByRole("radio", { name: /N h/ }));
    expect(onChange).toHaveBeenLastCalledWith("6h");
    rerender(<PseRefreshField label="Refresh" refresh="6h" onChange={onChange} />);
    const hours = screen.getByRole("spinbutton");
    fireEvent.change(hours, { target: { value: "12" } });
    expect(onChange).toHaveBeenLastCalledWith("12h");
  });

  it("masque live pour les listes d'addons", () => {
    render(<PseRefreshField label="Refresh" refresh="6h" onChange={() => {}} allowLive={false} />);
    expect(screen.queryByRole("radio", { name: "Live" })).toBeNull();
    expect(screen.getAllByRole("radio").length).toBe(4);
  });
});
