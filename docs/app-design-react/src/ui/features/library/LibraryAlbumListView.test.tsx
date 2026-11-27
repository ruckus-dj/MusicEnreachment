import { fireEvent, render } from "@testing-library/react";
import { expect, it } from "vitest";
import {
  LibraryAlbumListView,
  type LibraryAlbumRow,
} from "./LibraryAlbumListView";

const coverURL =
  "/api/library/releases/release-1/cover/thumbnails/generation-1";

function album(overrides: Partial<LibraryAlbumRow> = {}): LibraryAlbumRow {
  return {
    id: "local:release-1",
    title: "Covered Album",
    href: "#/library/releases/release-1",
    artist: "Album Artist",
    provider: "local",
    ...overrides,
  };
}

function renderAlbums(albums: readonly LibraryAlbumRow[]) {
  return render(
    <LibraryAlbumListView
      locale="en"
      albums={albums}
      expandedId={null}
      onToggleExpanded={() => {}}
    />,
  );
}

it("renders lazy decorative cover thumbnails in both album layouts", () => {
  const { container } = renderAlbums([album({ coverSrc: coverURL })]);
  const images = Array.from(container.querySelectorAll("img"));

  expect(images).toHaveLength(2);
  for (const image of images) {
    expect(image).toHaveAttribute("src", coverURL);
    expect(image).toHaveAttribute("alt", "");
    expect(image).toHaveAttribute("loading", "lazy");
  }
});

it("falls back to the title initial after a cover load error", () => {
  const { container, getAllByText } = renderAlbums([
    album({ coverSrc: coverURL }),
  ]);
  const images = Array.from(container.querySelectorAll("img"));
  expect(images).toHaveLength(2);

  for (const image of images) fireEvent.error(image);

  expect(container.querySelectorAll("img")).toHaveLength(0);
  expect(getAllByText("C", { exact: true })).toHaveLength(2);
});

it("keeps the title initial when the host has no selected cover", () => {
  const { container, getAllByText } = renderAlbums([album()]);

  expect(container.querySelector("img")).toBeNull();
  expect(getAllByText("C", { exact: true })).toHaveLength(2);
});
