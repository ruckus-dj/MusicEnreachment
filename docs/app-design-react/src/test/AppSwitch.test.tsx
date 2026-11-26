import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AppSwitch } from "../ui/components/AppSwitch";

function switchLabel(control: HTMLElement) {
  const label = control.closest("label");
  if (!label) throw new Error("Missing switch label");
  return label;
}

describe("AppSwitch", () => {
  it("toggles from its accessible control and describes itself from both sources", () => {
    const onChange = vi.fn();
    render(
      <>
        <AppSwitch
          label="LRCLIB is enabled"
          description="Saves automatically."
          describedBy="lrclib-external-help"
          isSelected={false}
          onChange={onChange}
        />
        <p id="lrclib-external-help">No new lyric requests are made.</p>
      </>,
    );

    const control = screen.getByRole("switch", {
      name: "LRCLIB is enabled",
    });
    expect(control).toHaveAccessibleDescription(
      "Saves automatically. No new lyric requests are made.",
    );

    fireEvent.click(control);
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it("centers the thumb inside the track with flex instead of absolute offsets", () => {
    const { container } = render(
      <AppSwitch
        label="LRCLIB is enabled"
        isSelected={false}
        onChange={() => {}}
      />,
    );
    const control = screen.getByRole("switch", {
      name: "LRCLIB is enabled",
    });
    const label = switchLabel(control);
    const track = label.querySelector<HTMLElement>("span.h-5.w-9");
    const thumb = track?.firstElementChild;

    expect(label).toHaveClass("relative", "flex", "items-start");
    expect(track).toHaveClass("box-border", "flex", "items-center", "px-px");
    expect(thumb).not.toHaveClass("absolute");
    expect(thumb).toHaveClass("size-4", "group-data-[selected]:translate-x-4");
    expect(container.querySelector("span.absolute")).toBeNull();
  });

  it("exposes the disabled state on both the control and its frame", () => {
    render(
      <AppSwitch
        label="Source deletion"
        isSelected={false}
        isDisabled
        onChange={() => {}}
      />,
    );

    const control = screen.getByRole("switch", { name: "Source deletion" });
    const label = switchLabel(control);
    expect(control).toBeDisabled();
    expect(label).toHaveAttribute("data-disabled", "true");
    expect(label).toHaveClass(
      "data-[disabled]:cursor-not-allowed",
      "data-[disabled]:opacity-60",
    );
  });
});
