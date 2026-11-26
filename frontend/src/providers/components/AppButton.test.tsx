import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AppButton } from "../../ui/components/AppButton";

describe("AppButton", () => {
  it("applies the shared design and preserves caller class names and press behavior", () => {
    const onPress = vi.fn();
    render(
      <AppButton className="caller-class" onPress={onPress}>
        Save
      </AppButton>,
    );

    const button = screen.getByRole("button", { name: "Save" });
    expect(button).toHaveClass("ui-button", "caller-class");
    fireEvent.click(button);
    expect(onPress).toHaveBeenCalledOnce();
  });

  it("lets caller classes replace conflicting shared classes", () => {
    render(<AppButton className="min-h-12 px-6">Tall</AppButton>);

    const button = screen.getByRole("button", { name: "Tall" });
    expect(button).toHaveClass("ui-button", "min-h-12", "px-6");
    expect(button).not.toHaveClass("min-h-9");
    expect(button).not.toHaveClass("px-3");
  });

  it("applies the small size over the default control size", () => {
    render(<AppButton size="sm">Small</AppButton>);

    const button = screen.getByRole("button", { name: "Small" });
    expect(button).toHaveClass("ui-button", "min-h-8", "px-2.5");
    expect(button).not.toHaveClass("min-h-9");
    expect(button).not.toHaveClass("px-3");
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
