import { fireEvent, render, screen, within } from "@testing-library/react";
import { useRef, useState } from "react";
import { expect, it, vi } from "vitest";
import {
  type ActiveFilterTag,
  ActiveFilterTags,
} from "../ui/components/ActiveFilterTags";

const active: ActiveFilterTag[] = [
  { id: "search", label: "Search: “Mira”" },
  { id: "status", label: "Published" },
];

function Harness({
  initial = active,
  onRemove = vi.fn(),
  onReset = vi.fn(),
}: {
  initial?: ActiveFilterTag[];
  onRemove?: (id: string) => void;
  onReset?: () => void;
}) {
  const [items, setItems] = useState(initial);
  const filterInputRef = useRef<HTMLInputElement>(null);
  return (
    <>
      <label>
        Earlier search
        <input type="search" />
      </label>
      <label>
        Library search
        <input ref={filterInputRef} type="search" />
      </label>
      <ActiveFilterTags
        locale="en"
        items={items}
        focusTargetRef={filterInputRef}
        onRemove={(id) => {
          onRemove(id);
          setItems((current) => current.filter((item) => item.id !== id));
        }}
        onReset={() => {
          onReset();
          setItems([]);
        }}
      />
    </>
  );
}

it("renders active conditions and a reset action only while something is active", () => {
  const onRemove = vi.fn();
  const onReset = vi.fn();
  const { rerender } = render(
    <ActiveFilterTags
      locale="en"
      items={active}
      onRemove={onRemove}
      onReset={onReset}
    />,
  );

  expect(
    screen.getByRole("grid", { name: "Active filters" }),
  ).toBeInTheDocument();
  expect(screen.getAllByRole("row")).toHaveLength(2);

  fireEvent.click(
    within(screen.getByRole("row", { name: /Search/ })).getByRole("button"),
  );
  expect(onRemove).toHaveBeenCalledWith("search");

  fireEvent.click(screen.getByRole("button", { name: "Reset all" }));
  expect(onReset).toHaveBeenCalledTimes(1);

  rerender(
    <ActiveFilterTags
      locale="en"
      items={[]}
      onRemove={onRemove}
      onReset={onReset}
    />,
  );
  expect(screen.queryByRole("grid", { name: "Active filters" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Reset all" })).toBeNull();
});

it("removes a focused tag with Delete and keeps focus on the next tag", () => {
  const onRemove = vi.fn();
  render(<Harness onRemove={onRemove} />);

  const searchTag = screen.getByRole("row", { name: /Search/ });
  searchTag.focus();
  fireEvent.keyDown(searchTag, { key: "Delete" });

  expect(onRemove).toHaveBeenCalledWith("search");
  expect(screen.getByRole("row", { name: "Published" })).toHaveFocus();
});

it("keeps follow-up Delete on the next surviving tag after a middle removal", () => {
  const onRemove = vi.fn();
  const three: ActiveFilterTag[] = [
    { id: "one", label: "One" },
    { id: "two", label: "Two" },
    { id: "three", label: "Three" },
  ];
  render(<Harness initial={three} onRemove={onRemove} />);

  const middle = screen.getByRole("row", { name: "Two" });
  middle.focus();
  fireEvent.keyDown(middle, { key: "Delete" });

  expect(onRemove).toHaveBeenLastCalledWith("two");
  const next = screen.getByRole("row", { name: "Three" });
  expect(next).toHaveFocus();

  fireEvent.keyDown(next, { key: "Delete" });
  expect(onRemove).toHaveBeenLastCalledWith("three");
  expect(screen.getByRole("row", { name: "One" })).toHaveFocus();
});

it("returns focus to the host target when the last tag is removed", () => {
  render(<Harness initial={[active[1]]} />);

  const tag = screen.getByRole("row", { name: "Published" });
  tag.focus();
  fireEvent.keyDown(tag, { key: "Delete" });

  expect(
    screen.getByRole("searchbox", { name: "Library search" }),
  ).toHaveFocus();
  expect(
    screen.getByRole("searchbox", { name: "Earlier search" }),
  ).not.toHaveFocus();
  expect(document.activeElement).not.toBe(document.body);
});

it("returns focus to the host target when reset clears every condition", () => {
  render(<Harness />);

  const reset = screen.getByRole("button", { name: "Reset all" });
  reset.focus();
  fireEvent.click(reset);

  expect(screen.queryByRole("grid", { name: "Active filters" })).toBeNull();
  expect(
    screen.getByRole("searchbox", { name: "Library search" }),
  ).toHaveFocus();
  expect(
    screen.getByRole("searchbox", { name: "Earlier search" }),
  ).not.toHaveFocus();
  expect(document.activeElement).not.toBe(document.body);
});
