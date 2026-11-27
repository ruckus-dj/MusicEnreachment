import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import {
  type LibraryArtistCard,
  LibraryArtistCards,
  type LibraryArtistPhoto,
} from "./LibraryArtistCards";

const photoURL = "https://upload.wikimedia.org/artist-1-thumb.jpg";
const sourceURL = "https://commons.wikimedia.org/wiki/File:Artist_1.jpg";
const licenseURL = "https://creativecommons.org/licenses/by-sa/4.0/";

const photo: LibraryArtistPhoto = {
  src: photoURL,
  sourceUrl: sourceURL,
  author: "Jane Doe",
  license: "CC BY-SA 4.0",
  licenseUrl: licenseURL,
};

function artist(overrides: Partial<LibraryArtistCard> = {}): LibraryArtistCard {
  return {
    id: "artist-1",
    name: "Album Artist",
    href: "#/library/artists/artist-1",
    monogram: "A",
    palette: "clay",
    genres: [],
    releaseCount: 1,
    positionCount: 2,
    availableCount: 1,
    publishedCount: 1,
    selection: {
      isSelected: false,
      isIndeterminate: false,
      isDisabled: false,
      onChange: () => {},
    },
    ...overrides,
  };
}

it("renders a photo and its real attribution in the existing art slot", () => {
  const { container } = render(
    <LibraryArtistCards locale="en" artists={[artist({ photo })]} />,
  );
  const image = container.querySelector("img");

  expect(image).toHaveAttribute("src", photoURL);
  expect(image).toHaveAttribute("alt", "");
  expect(image).toHaveAttribute("loading", "lazy");
  expect(image).toHaveAttribute("referrerpolicy", "no-referrer");
  expect(screen.getByText(/Cropped thumbnail/)).toBeVisible();
  expect(screen.getByText(/Jane Doe/)).toBeVisible();
  expect(screen.getByRole("link", { name: "CC BY-SA 4.0" })).toHaveAttribute(
    "href",
    licenseURL,
  );
  expect(
    screen.getByRole("link", { name: "Wikimedia Commons" }),
  ).toHaveAttribute("href", sourceURL);
});

it("keeps the monogram and hides attribution when no photo is supplied or the photo fails", () => {
  const { container, rerender } = render(
    <LibraryArtistCards locale="en" artists={[artist()]} />,
  );
  const artSlot = () => container.querySelector('[data-palette="clay"]');
  expect(container.querySelector("img")).toBeNull();
  expect(artSlot()).toHaveTextContent("A");
  expect(screen.queryByText(/Cropped thumbnail/)).not.toBeInTheDocument();

  rerender(<LibraryArtistCards locale="en" artists={[artist({ photo })]} />);
  const image = container.querySelector("img");
  expect(image).not.toBeNull();
  expect(artSlot()).not.toHaveTextContent("A");
  if (!image) throw new Error("missing artist photo");
  fireEvent.error(image);

  expect(container.querySelector("img")).toBeNull();
  expect(artSlot()).toHaveTextContent("A");
  expect(screen.queryByText(/Cropped thumbnail/)).not.toBeInTheDocument();
});
