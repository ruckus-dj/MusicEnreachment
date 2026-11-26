import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import {
  type ManagedPackageCardModel,
  SettingsManagedPackageCards,
} from "../ui/features/settings/SettingsManagedPackageCards";

const noop = () => {};

function packageModel({
  onRemoveBoth = noop,
  onRemoveOnly = noop,
  removeBothDisabled = false,
  removeOnlyDisabled = false,
}: {
  onRemoveBoth?: () => void;
  onRemoveOnly?: () => void;
  removeBothDisabled?: boolean;
  removeOnlyDisabled?: boolean;
}): ManagedPackageCardModel {
  return {
    kind: "ffmpeg",
    installations: [
      {
        id: "ffmpeg-8",
        release: { identity: "8.0" },
        active: false,
        lifeState: "ready",
        activate: { disabled: false, onPress: noop },
        remove: { disabled: removeBothDisabled, onPress: onRemoveBoth },
      },
      {
        id: "ffmpeg-7",
        release: { identity: "7.0" },
        active: false,
        lifeState: "ready",
        remove: { disabled: removeOnlyDisabled, onPress: onRemoveOnly },
      },
    ],
    catalog: { state: "notLoaded" },
    onReleaseChange: noop,
    downloadAndVerify: { disabled: true, onPress: noop },
  };
}

describe("SettingsManagedPackageCards row actions", () => {
  it("keeps Activate visible and opens Remove from the secondary menu", async () => {
    const remove = vi.fn();
    render(
      <SettingsManagedPackageCards
        locale="en"
        packages={[packageModel({ onRemoveBoth: remove })]}
      />,
    );

    expect(screen.getByRole("button", { name: "Activate 8.0" })).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "Remove 8.0" }),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "More actions: 8.0" }));
    const item = await screen.findByRole("menuitem", {
      name: "Remove 8.0",
    });
    expect(item).toHaveClass("text-[var(--error)]");
    fireEvent.click(item);

    await waitFor(() => expect(remove).toHaveBeenCalledTimes(1));
    expect(remove).toHaveBeenCalledWith(expect.any(HTMLButtonElement));
  });

  it("keeps a lone Remove action visible instead of hiding it in a menu", () => {
    const remove = vi.fn();
    render(
      <SettingsManagedPackageCards
        locale="en"
        packages={[packageModel({ onRemoveOnly: remove })]}
      />,
    );

    const removeButton = screen.getByRole("button", {
      name: "Remove 7.0",
    });
    expect(removeButton).toBeVisible();
    expect(
      screen.queryByRole("button", { name: "More actions: 7.0" }),
    ).not.toBeInTheDocument();

    fireEvent.click(removeButton);
    expect(remove).toHaveBeenCalledTimes(1);
  });

  it("keeps a disabled Remove action disabled inside the menu", async () => {
    render(
      <SettingsManagedPackageCards
        locale="en"
        packages={[packageModel({ removeBothDisabled: true })]}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "More actions: 8.0" }));
    const item = await screen.findByRole("menuitem", {
      name: "Remove 8.0",
    });
    expect(item).toHaveAttribute("aria-disabled", "true");
  });
});
