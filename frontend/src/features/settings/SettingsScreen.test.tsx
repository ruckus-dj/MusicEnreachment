import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SettingsScreen } from "./SettingsScreen";

describe("SettingsScreen", () => {
  it("allows a manual catalog refresh", () => {
    render(<SettingsScreen />);
    fireEvent.click(screen.getByRole("button", { name: "Refresh catalog" }));
    expect(screen.getByText(/обновлён вручную/)).toBeTruthy();
  });
});
