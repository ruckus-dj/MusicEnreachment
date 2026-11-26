import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { RowActionMenu } from "../ui/components/RowActionMenu";

it("closes the menu, runs the action, and passes the persistent trigger", async () => {
  const onAction = vi.fn();
  render(
    <RowActionMenu
      label="Row actions: Northern Lines"
      actions={[
        { id: "retry", label: "Retry", onAction },
        {
          id: "delete",
          label: "Delete",
          isDestructive: true,
          onAction,
        },
      ]}
    />,
  );
  const trigger = screen.getByRole("button", {
    name: "Row actions: Northern Lines",
  });
  fireEvent.click(trigger);

  const retry = await screen.findByRole("menuitem", { name: "Retry" });
  fireEvent.click(retry);

  await waitFor(() => expect(onAction).toHaveBeenCalledTimes(1));
  expect(onAction).toHaveBeenCalledWith(trigger);
  await waitFor(() =>
    expect(screen.queryByRole("menu")).not.toBeInTheDocument(),
  );
});

it("keeps disabled and pending actions unavailable", async () => {
  const onAction = vi.fn();
  render(
    <RowActionMenu
      label="Row actions: Northern Lines"
      actions={[
        {
          id: "delete",
          label: "Delete",
          isDisabled: true,
          onAction,
        },
        {
          id: "retry",
          label: "Retry",
          isPending: true,
          onAction,
        },
      ]}
    />,
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Row actions: Northern Lines" }),
  );

  const remove = await screen.findByRole("menuitem", { name: "Delete" });
  const retry = screen.getByRole("menuitem", { name: "Retry" });
  expect(remove).toHaveAttribute("aria-disabled", "true");
  expect(retry).toHaveAttribute("aria-disabled", "true");
  fireEvent.click(remove);
  fireEvent.click(retry);
  expect(onAction).not.toHaveBeenCalled();
});

it("does not run an action that became pending while the menu closes", async () => {
  const onAction = vi.fn();
  const label = "Row actions: Northern Lines";
  const { rerender } = render(
    <RowActionMenu
      label={label}
      actions={[{ id: "retry", label: "Retry", onAction }]}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: label }));
  const retry = await screen.findByRole("menuitem", { name: "Retry" });

  act(() => {
    fireEvent.click(retry);
    rerender(
      <RowActionMenu
        label={label}
        actions={[{ id: "retry", label: "Retry", isPending: true, onAction }]}
      />,
    );
  });

  await waitFor(() =>
    expect(screen.queryByRole("menu")).not.toBeInTheDocument(),
  );
  expect(onAction).not.toHaveBeenCalled();
});

it("does not run an action removed while the menu closes", async () => {
  const onAction = vi.fn();
  const label = "Row actions: Northern Lines";
  const { rerender } = render(
    <RowActionMenu
      label={label}
      actions={[{ id: "retry", label: "Retry", onAction }]}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: label }));
  const retry = await screen.findByRole("menuitem", { name: "Retry" });

  act(() => {
    fireEvent.click(retry);
    rerender(<RowActionMenu label={label} actions={[]} />);
  });

  await waitFor(() =>
    expect(screen.queryByRole("menu")).not.toBeInTheDocument(),
  );
  expect(onAction).not.toHaveBeenCalled();
});

it("returns focus to the trigger on Escape", async () => {
  render(
    <RowActionMenu
      label="Row actions: Northern Lines"
      actions={[{ id: "retry", label: "Retry", onAction: vi.fn() }]}
    />,
  );
  const trigger = screen.getByRole("button", {
    name: "Row actions: Northern Lines",
  });
  fireEvent.click(trigger);

  const retry = await screen.findByRole("menuitem", { name: "Retry" });
  retry.focus();
  fireEvent.keyDown(retry, { key: "Escape", code: "Escape" });

  await waitFor(() => expect(trigger).toHaveFocus());
});
