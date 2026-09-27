import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AppShell } from "./AppShell";

describe("AppShell", () => {
  it("renders the application shell", () => {
    render(<AppShell />);
    expect(screen.getByRole("main").textContent).toContain("MusicEnreachment");
  });

  it("renders the settings route", () => {
    window.location.hash = "/settings";
    const { container } = render(<AppShell />);
    expect(within(container).getByRole("main").textContent).toContain(
      "Setup Manager",
    );
    window.location.hash = "";
  });
});
