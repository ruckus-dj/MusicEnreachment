import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SetupManager } from "./SetupManager";

describe("SetupManager", () => {
  it("requires absolute directories", () => {
    render(<SetupManager onCompleted={() => undefined} />);
    fireEvent.click(screen.getByRole("button", { name: "Продолжить" }));
    expect(screen.getByRole("alert").textContent).toContain("абсолютные");
  });
});
