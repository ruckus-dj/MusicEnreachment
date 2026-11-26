import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import {
  GenrePicker,
  type GenrePickerCatalog,
  type GenreReference,
} from "../ui/features/library/GenrePicker";
import { genreMessages } from "../ui/features/library/genreMessages";

const options: GenreReference[] = [
  { id: "rock", source_name: "Rock", name: "Rock" },
  { id: "jazz", source_name: "jazz music", name: "Jazz" },
];

const copy = genreMessages.en;

function catalog(
  overrides: Partial<GenrePickerCatalog> = {},
): GenrePickerCatalog {
  return {
    options,
    query: "",
    onQueryChange: () => undefined,
    loading: false,
    catalogError: "",
    onClearCatalogError: () => undefined,
    onRetryCatalog: () => undefined,
    catalogStatus: {
      updated_at: "2026-01-01T00:00:00Z",
      count: options.length,
    },
    catalogStatusLoading: false,
    catalogStatusError: "",
    onOpenCatalog: () => undefined,
    onRetryStatus: () => undefined,
    ...overrides,
  };
}

function Harness() {
  const [value, setValue] = useState<GenreReference[]>([
    { id: "legacy", source_name: "Legacy", name: "Legacy" },
  ]);
  return (
    <GenrePicker
      value={value}
      onChange={setValue}
      editing
      label="Genres"
      copy={copy}
      disabled={false}
      {...catalog({ query: "ro" })}
    />
  );
}

function RemovalHarness({ initial }: { initial: GenreReference[] }) {
  const [value, setValue] = useState(initial);
  return (
    <GenrePicker
      value={value}
      onChange={setValue}
      editing
      label="Genres"
      copy={copy}
      disabled={false}
      {...catalog()}
    />
  );
}

it("keeps supplied provider results and appends the selected genre", async () => {
  render(<Harness />);

  fireEvent.click(screen.getByRole("button", { name: "Genres" }));
  expect(
    await screen.findByRole("searchbox", { name: copy.search }),
  ).toHaveValue("ro");
  expect(screen.queryByRole("dialog")).toBeNull();

  // "Jazz" does not match the query: the popover must not filter the page
  // returned by the provider a second time. Its accessible name comes from
  // the genre name, not the visible source-name suffix.
  expect(screen.getByRole("option", { name: "Jazz" })).toBeInTheDocument();
  expect(screen.getByText("jazz music")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("option", { name: "Jazz" }));

  expect(
    screen.getByRole("button", { name: "Remove Legacy" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("button", { name: "Remove Jazz" }),
  ).toBeInTheDocument();
});

it("moves focus to an enabled popover action when search is unavailable", async () => {
  const onLoad = vi.fn();

  const unloaded = render(
    <GenrePicker
      value={[]}
      onChange={() => undefined}
      editing
      label="Genres"
      copy={copy}
      disabled={false}
      {...catalog({
        catalogStatus: undefined,
        onLoadBundledCatalog: onLoad,
      })}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Genres" }));
  const load = await screen.findByRole("button", {
    name: copy.loadBundledCatalog,
  });
  expect(screen.getByRole("searchbox", { name: copy.search })).toBeDisabled();
  await waitFor(() => expect(load).toHaveFocus());
  unloaded.unmount();

  const loading = render(
    <GenrePicker
      value={[]}
      onChange={() => undefined}
      editing
      label="Genres"
      copy={copy}
      disabled={false}
      {...catalog({
        catalogStatus: undefined,
        catalogStatusLoading: true,
        onLoadBundledCatalog: onLoad,
      })}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Genres" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: copy.done })).toHaveFocus(),
  );
  loading.unmount();

  render(
    <GenrePicker
      value={[]}
      onChange={() => undefined}
      editing
      label="Genres"
      copy={copy}
      disabled={false}
      {...catalog({
        catalogStatus: { updated_at: "2026-01-01T00:00:00Z", count: 0 },
      })}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Genres" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: copy.done })).toHaveFocus(),
  );
});

it("closes on Escape without canceling the editor and restores the trigger", async () => {
  const onCancel = vi.fn();
  render(
    <section
      onKeyDown={(event) => {
        if (event.key === "Escape") onCancel();
      }}
    >
      <GenrePicker
        value={[{ id: "jazz", source_name: "Jazz", name: "Jazz" }]}
        onChange={() => undefined}
        editing
        label="Genres"
        copy={copy}
        disabled={false}
        {...catalog()}
      />
    </section>,
  );
  const add = screen.getByRole("button", { name: "Genres" });
  fireEvent.click(add);
  const search = await screen.findByRole("searchbox", { name: copy.search });
  await waitFor(() => expect(search).toHaveFocus());

  fireEvent.keyDown(search, { key: "Escape" });

  await waitFor(() =>
    expect(screen.queryByRole("searchbox", { name: copy.search })).toBeNull(),
  );
  expect(onCancel).not.toHaveBeenCalled();
  await waitFor(() => expect(add).toHaveFocus());

  fireEvent.click(add);
  add.focus();
  fireEvent.keyDown(add, { key: "Escape" });
  await waitFor(() =>
    expect(screen.queryByRole("searchbox", { name: copy.search })).toBeNull(),
  );
  expect(onCancel).not.toHaveBeenCalled();
  await waitFor(() => expect(add).toHaveFocus());
});

it("returns focus to search when the final Show more page is revealed", async () => {
  const many = Array.from({ length: 61 }, (_, index) => ({
    id: `genre-${index}`,
    source_name: `Genre ${index}`,
    name: `Genre ${index}`,
  }));
  render(
    <GenrePicker
      value={[]}
      onChange={() => undefined}
      editing
      label="Genres"
      copy={copy}
      disabled={false}
      {...catalog({
        options: many,
        catalogStatus: { updated_at: "2026-01-01T00:00:00Z", count: 61 },
      })}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "Genres" }));
  const search = await screen.findByRole("searchbox", { name: copy.search });
  fireEvent.click(screen.getByRole("button", { name: /Show more/ }));

  await waitFor(() =>
    expect(screen.queryByRole("button", { name: /Show more/ })).toBeNull(),
  );
  await waitFor(() => expect(search).toHaveFocus());
});

it("keeps focus sensible after removing tags", async () => {
  render(
    <RemovalHarness
      initial={[
        { id: "rock", source_name: "Rock", name: "Rock" },
        { id: "jazz", source_name: "Jazz", name: "Jazz" },
      ]}
    />,
  );

  fireEvent.click(screen.getByRole("button", { name: "Remove Rock" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Remove Jazz" })).toHaveFocus(),
  );

  fireEvent.click(screen.getByRole("button", { name: "Remove Jazz" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Genres" })).toHaveFocus(),
  );
});

it("renders read-only tags as plain list items without tab stops", () => {
  render(
    <GenrePicker
      value={[
        { id: "rock", source_name: "Rock", name: "Rock" },
        { id: "jazz", source_name: "jazz music", name: "Jazz" },
      ]}
      onChange={() => undefined}
      editing={false}
      label="Genres"
      copy={copy}
      disabled={false}
      {...catalog()}
    />,
  );

  const list = screen.getByRole("list", { name: copy.selected });
  expect(within(list).getAllByRole("listitem")).toHaveLength(2);
  expect(within(list).queryAllByRole("button")).toHaveLength(0);
  for (const item of within(list).getAllByRole("listitem"))
    expect(item).not.toHaveAttribute("tabindex");
});

it("localizes the visible removable tag", () => {
  render(
    <GenrePicker
      value={[{ id: "jazz", source_name: "jazz", name: "Jazz" }]}
      onChange={() => undefined}
      editing
      label="Жанры"
      copy={genreMessages.ru}
      disabled={false}
      {...catalog()}
    />,
  );

  expect(
    screen.getByRole("button", { name: "Удалить Jazz" }),
  ).toBeInTheDocument();
});
