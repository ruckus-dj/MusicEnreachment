import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AppShell } from "./AppShell";

describe("AppShell", () => {
  it("renders the application shell", () => {
    render(<AppShell />);
    expect(screen.getByRole("main").textContent).toContain("MusicEnreachment");
  });
});
