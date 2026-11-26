import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { TrackActionsView } from "../ui/features/library/TrackActionsView";

const rename = {
  open: false,
  title: "First Light",
  onOpenChange: vi.fn(),
  onTitleChange: vi.fn(),
  onStart: vi.fn(),
  onSubmit: vi.fn(),
};

it("keeps local title editing visible and puts provider actions in the menu", async () => {
  const onRefresh = vi.fn();
  const onRepublish = vi.fn();
  const onOpenChange = vi.fn();
  const { rerender } = render(
    <TrackActionsView
      locale="en"
      notice=""
      rename={{ ...rename, onOpenChange }}
      onRefresh={onRefresh}
      onRepublish={onRepublish}
    />,
  );

  const edit = screen.getByRole("button", { name: "Edit local title" });
  expect(edit).toBeVisible();
  expect(
    screen.queryByRole("button", { name: "Refresh provider cache" }),
  ).toBeNull();
  fireEvent.click(edit);
  expect(rename.onStart).toHaveBeenCalledTimes(1);

  fireEvent.click(screen.getByRole("button", { name: "Other actions" }));
  const refresh = await screen.findByRole("menuitem", {
    name: "Refresh provider cache",
  });
  fireEvent.click(refresh);
  expect(onRefresh).toHaveBeenCalledTimes(1);

  fireEvent.click(screen.getByRole("button", { name: "Other actions" }));
  fireEvent.click(
    await screen.findByRole("menuitem", { name: "Republish explicitly" }),
  );
  expect(onRepublish).toHaveBeenCalledTimes(1);

  rerender(
    <TrackActionsView
      locale="en"
      notice=""
      rename={{ ...rename, open: true, onOpenChange }}
      onRefresh={onRefresh}
      onRepublish={onRepublish}
    />,
  );
  expect(screen.getByRole("dialog")).toHaveTextContent("Edit local tags only");
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(onOpenChange).toHaveBeenCalledWith(false);
});
