import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { LibraryCatalogPageViewProps } from "../ui/features/library/LibraryCatalogPageView";
import { LibraryCatalogPageView } from "../ui/features/library/LibraryCatalogPageView";

function pageProps(
  overrides: Partial<LibraryCatalogPageViewProps> = {},
): LibraryCatalogPageViewProps {
  return {
    locale: "en",
    view: "artists",
    search: "Mira",
    onSearch: vi.fn(),
    filter: "published",
    onFilter: vi.fn(),
    publication: "published",
    availability: "all",
    onPublicationChange: vi.fn(),
    onAvailabilityChange: vi.fn(),
    onResetFilters: vi.fn(),
    onViewChange: vi.fn(),
    state: { status: "results" },
    results: { kind: "artists", artists: [] },
    page: 1,
    totalPages: 1,
    totalItems: 0,
    hasPrevious: false,
    hasNext: false,
    onPageChange: vi.fn(),
    ...overrides,
  };
}

it("renders each active canonical axis as its own tag", () => {
  render(
    <LibraryCatalogPageView
      {...pageProps({ publication: "published", availability: "available" })}
    />,
  );

  expect(
    screen.getByRole("grid", { name: "Active filters" }),
  ).toBeInTheDocument();
  expect(screen.getByRole("row", { name: /Search: “Mira”/ })).toBeVisible();
  expect(screen.getByRole("row", { name: /Published/ })).toBeVisible();
  expect(screen.getByRole("row", { name: /Has audio/ })).toBeVisible();
});

it("shows the unpublished axis even though the union status is all", () => {
  render(
    <LibraryCatalogPageView
      {...pageProps({
        search: "",
        filter: "all",
        publication: "unpublished",
        availability: "all",
      })}
    />,
  );

  expect(screen.getByRole("row", { name: /Unpublished/ })).toBeVisible();
  expect(screen.queryByRole("row", { name: /^All$/ })).toBeNull();
});

it("hides the summary when every condition is at its default", () => {
  render(
    <LibraryCatalogPageView
      {...pageProps({
        search: "",
        filter: "all",
        publication: "all",
        availability: "all",
      })}
    />,
  );

  expect(screen.queryByRole("grid", { name: "Active filters" })).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Reset all" }),
  ).not.toBeInTheDocument();
});

it("removes only the touched canonical axis", () => {
  const onFilter = vi.fn();
  const onPublicationChange = vi.fn();
  const onAvailabilityChange = vi.fn();
  render(
    <LibraryCatalogPageView
      {...pageProps({
        filter: "published",
        onFilter,
        publication: "published",
        availability: "available",
        onPublicationChange,
        onAvailabilityChange,
      })}
    />,
  );

  fireEvent.click(
    within(screen.getByRole("row", { name: /Published/ })).getByRole("button"),
  );
  expect(onPublicationChange).toHaveBeenCalledWith("all");
  expect(onAvailabilityChange).not.toHaveBeenCalled();
  expect(onFilter).not.toHaveBeenCalled();

  fireEvent.click(
    within(screen.getByRole("row", { name: /Has audio/ })).getByRole("button"),
  );
  expect(onAvailabilityChange).toHaveBeenCalledWith("all");
});

it("removes search through the canonical search callback", () => {
  const onSearch = vi.fn();
  const onPublicationChange = vi.fn();
  render(
    <LibraryCatalogPageView
      {...pageProps({ onSearch, onPublicationChange })}
    />,
  );

  fireEvent.click(
    within(screen.getByRole("row", { name: /Search: “Mira”/ })).getByRole(
      "button",
    ),
  );
  expect(onSearch).toHaveBeenCalledWith("");
  expect(onPublicationChange).not.toHaveBeenCalled();
});

it("resets through the atomic callback instead of composing axes", () => {
  const onSearch = vi.fn();
  const onFilter = vi.fn();
  const onPublicationChange = vi.fn();
  const onAvailabilityChange = vi.fn();
  const onResetFilters = vi.fn();
  render(
    <LibraryCatalogPageView
      {...pageProps({
        onSearch,
        onFilter,
        onPublicationChange,
        onAvailabilityChange,
        onResetFilters,
      })}
    />,
  );

  fireEvent.click(screen.getByRole("button", { name: "Reset all" }));
  expect(onResetFilters).toHaveBeenCalledTimes(1);
  expect(onSearch).not.toHaveBeenCalled();
  expect(onFilter).not.toHaveBeenCalled();
  expect(onPublicationChange).not.toHaveBeenCalled();
  expect(onAvailabilityChange).not.toHaveBeenCalled();
});
