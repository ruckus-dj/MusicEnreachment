import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AppButton } from "./AppButton";

describe("AppButton", () => {
  it("applies the shared design and preserves caller class names and press behavior", () => {
    const onPress = vi.fn();
    render(
      <AppButton className="caller-class" onPress={onPress}>
        Save
      </AppButton>,
    );

    const button = screen.getByRole("button", { name: "Save" });
    expect(button).toHaveClass("ui-button", "min-h-[30px]", "caller-class");
    fireEvent.click(button);
    expect(onPress).toHaveBeenCalledOnce();
  });

  it("retains React Aria className render callbacks and disabled behavior", () => {
    const className = vi.fn(({ isDisabled }: { isDisabled: boolean }) =>
      isDisabled ? "caller-disabled" : "caller-enabled",
    );
    const onPress = vi.fn();
    render(
      <AppButton className={className} isDisabled onPress={onPress}>
        Disabled
      </AppButton>,
    );

    const button = screen.getByRole("button", { name: "Disabled" });
    expect(button).toHaveClass("caller-disabled");
    fireEvent.click(button);
    expect(onPress).not.toHaveBeenCalled();
  });
});
