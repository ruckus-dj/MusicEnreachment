import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ArtistEditionRowView } from "../ui/features/library/ArtistEditionRowView";
import type { ArtistEditionModel } from "../ui/features/library/ArtistScreenView";

const edition: ArtistEditionModel = {
  key: "release-first-light",
  title: "First Light",
  titleHref: "#/library/releases/release-first-light",
  year: "2020",
  catalog: "CAT-1",
  ariaLabel: "First Light · 2020 · CAT-1",
  artwork: { title: "First Light", palette: "forest" },
  positionCount: 0,
  country: "US",
  label: "Label",
  publishedCount: 0,
  providerTrackCount: 0,
  metadataChangedCount: 0,
};

it.each([
  ["en", "Open release", "Show positions"],
  ["ru", "Открыть издание", "Показать позиции"],
] as const)("keeps a localized release link beside the %s expansion trigger", (locale, openRelease, showPositions) => {
  render(<ArtistEditionRowView locale={locale} edition={edition} />);

  const link = screen.getByRole("link", {
    name: `${openRelease}: ${edition.ariaLabel}`,
  });
  expect(link.textContent).toBe(openRelease);
  expect(link).toHaveAttribute(
    "href",
    "#/library/releases/release-first-light",
  );
  expect(
    screen.getByRole("button", {
      name: `${showPositions}: ${edition.ariaLabel}`,
    }),
  ).toHaveAttribute("aria-expanded", "false");
});

it("expands from the explicit chevron and reveals the full-width details", () => {
  render(<ArtistEditionRowView locale="en" edition={edition} />);
  const trigger = screen.getByRole("button", {
    name: `Show positions: ${edition.ariaLabel}`,
  });

  fireEvent.click(trigger);

  expect(
    screen.getByRole("button", {
      name: `Hide positions: ${edition.ariaLabel}`,
    }),
  ).toHaveAttribute("aria-expanded", "true");
  expect(screen.getByText("No positions added.")).toBeVisible();
});

it("expands when the album title or another non-control part of the row is clicked", () => {
  render(<ArtistEditionRowView locale="en" edition={edition} />);
  const trigger = screen.getByRole("button", {
    name: `Show positions: ${edition.ariaLabel}`,
  });

  fireEvent.click(screen.getByText("First Light"));

  expect(trigger).toHaveAttribute("aria-expanded", "true");
});

it("keeps the selection checkbox from toggling the row", () => {
  const onChange = vi.fn();
  render(
    <ArtistEditionRowView
      locale="en"
      edition={{
        ...edition,
        selection: {
          isSelected: false,
          isIndeterminate: false,
          isDisabled: false,
          onChange,
        },
      }}
    />,
  );
  const trigger = screen.getByRole("button", {
    name: `Show positions: ${edition.ariaLabel}`,
  });

  fireEvent.click(
    screen.getByRole("checkbox", {
      name: `Select all positions in this edition: ${edition.ariaLabel}`,
    }),
  );

  expect(onChange).toHaveBeenCalledWith(true);
  expect(trigger).toHaveAttribute("aria-expanded", "false");
});
