import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { OriginalRelease, OriginalTrack } from "./catalogData";
import { SelectionDock } from "./SelectionDock";

const track = (id: string, title: string): OriginalTrack => ({
  id,
  title,
  position: 1,
  duration: null,
  artistId: null,
  sourceId: null,
  published: null,
});

const release: OriginalRelease = {
  id: "release-1",
  title: "Release",
  artistId: null,
  year: "2026",
  country: "",
  catalog: "",
  label: "",
  providerId: null,
  tracks: [
    track("visible", "Visible"),
    track("hidden-1", "Hidden one"),
    track("hidden-2", "Hidden two"),
  ],
};

const dock = (
  selected: Set<string>,
  onSelect = vi.fn(),
  onPreview = vi.fn(),
) => (
  <SelectionDock
    selected={selected}
    visiblePositionIds={new Set(["visible"])}
    releases={[release]}
    onSelect={onSelect}
    onPreview={onPreview}
    onQueueDeletion={vi.fn()}
  />
);

it("opens publication deletion from the overflow menu with its consequences", async () => {
  render(dock(new Set(["visible", "hidden-1", "hidden-2"])));

  expect(
    screen.queryByRole("button", { name: "Удалить публикации" }),
  ).toBeNull();
  const trigger = screen.getByRole("button", {
    name: "Другие действия с выбранными позициями",
  });
  fireEvent.click(trigger);
  fireEvent.click(
    await screen.findByRole("menuitem", { name: "Удалить публикации" }),
  );

  const dialog = await screen.findByRole("dialog");
  expect(
    within(dialog).getByText(/Будут затронуты только перечисленные/),
  ).toBeVisible();
  expect(
    within(dialog).getByText(/Прототип только ставит демонстрационный запрос/),
  ).toBeVisible();
  expect(
    within(dialog).getByRole("button", { name: "Подтвердить удаление" }),
  ).toBeDisabled();
  fireEvent.click(within(dialog).getByRole("button", { name: "Отмена" }));
  await waitFor(() => expect(trigger).toHaveFocus());
});

it("reviews and removes selections hidden outside the current page", () => {
  const onSelect = vi.fn();
  const onPreview = vi.fn();
  const { rerender } = render(
    dock(new Set(["visible", "hidden-1", "hidden-2"]), onSelect, onPreview),
  );

  expect(screen.getByRole("status")).toHaveTextContent(
    "Выбрано позиций: 3 · Вне этой страницы: 2",
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Предпросмотр публикации" }),
  );
  expect(onPreview).toHaveBeenCalledTimes(1);
  expect(onPreview.mock.calls[0][0]).toHaveLength(3);
  expect(onPreview.mock.calls[0][0]).toEqual(
    expect.arrayContaining(["visible", "hidden-1", "hidden-2"]),
  );

  fireEvent.click(screen.getByRole("button", { name: "Проверить выбор" }));
  const dialog = screen.getByRole("dialog");
  expect(within(dialog).getByText("Hidden one")).toBeVisible();
  fireEvent.click(
    within(dialog).getByRole("button", {
      name: "Снять выбор: Hidden one · Release",
    }),
  );
  expect(onSelect).toHaveBeenCalledWith(["hidden-1"], false);

  rerender(dock(new Set(["visible", "hidden-2"]), onSelect, onPreview));
  expect(
    within(screen.getByRole("dialog")).queryByText("Hidden one"),
  ).toBeNull();
  fireEvent.click(
    within(screen.getByRole("dialog")).getByRole("button", { name: "Закрыть" }),
  );
  expect(screen.getByRole("status")).toHaveTextContent(
    "Выбрано позиций: 2 · Вне этой страницы: 1",
  );
});
