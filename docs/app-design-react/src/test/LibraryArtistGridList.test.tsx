import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import {
  type LibraryArtistCard,
  LibraryArtistCards,
} from "../ui/features/library/LibraryArtistCards";

function artist(overrides: Partial<LibraryArtistCard> = {}): LibraryArtistCard {
  return {
    id: "mira",
    name: "Mira Vale",
    href: "#/library/artists/mira",
    monogram: "MV",
    palette: "forest",
    genres: ["ambient", "folk"],
    releaseCount: 3,
    positionCount: 9,
    availableCount: 6,
    publishedCount: 4,
    selection: {
      isSelected: false,
      isIndeterminate: false,
      isDisabled: false,
      onChange: vi.fn(),
    },
    ...overrides,
  };
}

const mira = artist();
const arden = artist({
  id: "arden",
  name: "Arden Trio",
  href: "#/library/artists/arden",
  monogram: "AT",
  palette: "dusk",
  genres: ["jazz"],
});
const coda = artist({
  id: "coda",
  name: "Coda",
  href: "#/library/artists/coda",
  monogram: "CO",
  palette: "sand",
  genres: ["ambient"],
});

function SelectionHarness({
  artists,
  onChange,
}: {
  artists: LibraryArtistCard[];
  onChange: (id: string, value: boolean) => void;
}) {
  const [selected, setSelected] = useState(
    () =>
      new Set(
        artists
          .filter((item) => item.selection.isSelected)
          .map((item) => item.id),
      ),
  );
  return (
    <LibraryArtistCards
      locale="en"
      artists={artists.map((item) => ({
        ...item,
        selection: {
          ...item.selection,
          isSelected: selected.has(item.id),
          onChange: (value) => {
            onChange(item.id, value);
            setSelected((current) => {
              const next = new Set(current);
              if (value) {
                next.add(item.id);
              } else {
                next.delete(item.id);
              }
              return next;
            });
          },
        },
      }))}
    />
  );
}

function PagedHarness({ pages }: { pages: LibraryArtistCard[][] }) {
  const [page, setPage] = useState(0);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  return (
    <>
      <button
        type="button"
        onClick={() => setPage((current) => (current + 1) % pages.length)}
      >
        Next page
      </button>
      <output data-testid="selected-ids">
        {[...selected].sort().join(",")}
      </output>
      <LibraryArtistCards
        locale="en"
        artists={pages[page].map((item) => ({
          ...item,
          selection: {
            ...item.selection,
            isSelected: selected.has(item.id),
            onChange: (value) => {
              setSelected((current) => {
                const next = new Set(current);
                if (value) {
                  next.add(item.id);
                } else {
                  next.delete(item.id);
                }
                return next;
              });
            },
          },
        }))}
      />
    </>
  );
}

it.each([
  ["en", "Library artists"],
  ["ru", "Артисты медиатеки"],
] as const)("renders the %s gallery as a labeled grid", (locale, label) => {
  render(<LibraryArtistCards locale={locale} artists={[mira]} />);

  const grid = screen.getByRole("grid", { name: label });
  expect(grid).toHaveAttribute("aria-multiselectable", "true");
  expect(within(grid).getAllByRole("row")).toHaveLength(1);
});

it("keeps one title link and exposes artwork without duplicating the genre description", () => {
  render(<LibraryArtistCards locale="en" artists={[mira]} />);

  const row = screen.getByRole("row");
  expect(row).toHaveAccessibleName("Mira Vale ambient · folk");
  expect(within(row).getByRole("checkbox")).toHaveAccessibleName(
    "Select positions by Mira Vale",
  );

  const links = within(row).getAllByRole("link");
  expect(links).toHaveLength(1);
  expect(links[0]).toHaveAccessibleName("Mira Vale");
  expect(links[0]).toHaveAttribute("href", "#/library/artists/mira");

  const artwork = within(row).getByText("MV");
  expect(artwork).toHaveAttribute("aria-hidden", "true");
  expect(row).not.toHaveAccessibleName(/MV/);
});

it("toggles selection without navigating", () => {
  const changes = vi.fn<(id: string, value: boolean) => void>();
  window.history.replaceState(
    null,
    "",
    "#/library?view=artists&publication=all&availability=all",
  );
  const hash = window.location.hash;
  render(<SelectionHarness artists={[mira]} onChange={changes} />);

  let checkbox = screen.getByRole("checkbox", {
    name: /Select positions by Mira Vale/,
  });
  fireEvent.click(checkbox);
  expect(checkbox).toBeChecked();
  expect(changes).toHaveBeenLastCalledWith("mira", true);

  checkbox = screen.getByRole("checkbox", {
    name: /Select positions by Mira Vale/,
  });
  fireEvent.click(checkbox);
  expect(checkbox).not.toBeChecked();
  expect(changes).toHaveBeenLastCalledWith("mira", false);
  expect(window.location.hash).toBe(hash);
});

it("exposes a partially selected artist checkbox as mixed", () => {
  const partial = artist({
    id: "partial",
    name: "Partial Artist",
    href: "#/library/artists/partial",
    monogram: "PA",
    genres: ["ambient"],
    selection: {
      ...mira.selection,
      isIndeterminate: true,
    },
  });
  render(<LibraryArtistCards locale="en" artists={[partial]} />);

  const checkbox = screen.getByRole("checkbox", {
    name: /Select positions by Partial Artist/,
  });
  expect(checkbox).toBePartiallyChecked();
});

it("keeps a selection-disabled artist navigable through the title link", () => {
  const locked = artist({
    id: "locked",
    name: "Locked Artist",
    href: "#/library/artists/locked",
    monogram: "LA",
    genres: ["jazz"],
    selection: {
      ...mira.selection,
      isDisabled: true,
    },
  });
  render(<LibraryArtistCards locale="en" artists={[mira, locked]} />);

  const row = screen.getByRole("row", { name: /Locked Artist/ });
  expect(row).not.toHaveAttribute("aria-disabled");

  const link = within(row).getByRole("link", { name: "Locked Artist" });
  expect(link).toHaveAttribute("href", "#/library/artists/locked");
  expect(link).not.toHaveAttribute("aria-disabled");
  expect(link).toHaveAttribute("tabindex", "0");
});

it("does not call a disabled artist handler when selecting all", () => {
  const enabledChange = vi.fn<(value: boolean) => void>();
  const disabledChange = vi.fn<(value: boolean) => void>();
  const enabled = artist({
    selection: { ...mira.selection, onChange: enabledChange },
  });
  const locked = artist({
    id: "locked",
    name: "Locked Artist",
    href: "#/library/artists/locked",
    monogram: "LA",
    genres: ["jazz"],
    selection: {
      ...mira.selection,
      isDisabled: true,
      onChange: disabledChange,
    },
  });
  render(<LibraryArtistCards locale="en" artists={[enabled, locked]} />);

  const row = screen.getAllByRole("row")[0];
  act(() => {
    row.focus();
    fireEvent.keyDown(row, { key: "a", code: "KeyA", ctrlKey: true });
  });

  expect(enabledChange).toHaveBeenCalledWith(true);
  expect(disabledChange).not.toHaveBeenCalled();
});

it("keeps off-page selected IDs when the visible artist page changes", () => {
  render(<PagedHarness pages={[[mira, arden], [coda]]} />);

  fireEvent.click(
    screen.getByRole("checkbox", {
      name: /Select positions by Mira Vale/,
    }),
  );
  expect(screen.getByTestId("selected-ids")).toHaveTextContent("mira");

  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  expect(
    screen.queryByRole("checkbox", { name: /Mira Vale/ }),
  ).not.toBeInTheDocument();
  fireEvent.click(
    screen.getByRole("checkbox", { name: /Select positions by Coda/ }),
  );
  expect(screen.getByTestId("selected-ids")).toHaveTextContent("coda,mira");

  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  expect(
    screen.getByRole("checkbox", {
      name: /Select positions by Mira Vale/,
    }),
  ).toBeChecked();
  expect(screen.getByTestId("selected-ids")).toHaveTextContent("coda,mira");
});
