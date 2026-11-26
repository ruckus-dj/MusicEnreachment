import { render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import {
  FileFormatBadge,
  MetadataBadge,
} from "../ui/components/FileFormatBadge";
import { LibraryAlbumPositionsView } from "../ui/features/library/LibraryAlbumPositionsView";
import {
  LibraryTrackListView,
  type LibraryTrackRow,
} from "../ui/features/library/LibraryTrackListView";

const selection = {
  isSelected: false,
  isIndeterminate: false,
  isDisabled: false,
  onChange: () => undefined,
};

const row: LibraryTrackRow = {
  id: "track-1",
  title: "First Light",
  durationMs: 245_000,
  href: "#/library/positions/track-1",
  releaseTitle: "North",
  releaseHref: "#/library/releases/north",
  positionLabel: "1 · 01",
  hasAudio: true,
  variantCount: 2,
  published: true,
  sourceCodec: "FLAC",
  properties: "24 bit · 48 kHz",
  publicationContainer: "MKA",
  publicationCodec: "FLAC",
  selection,
};

it("renders a format as one compact non-status chip without changing its text", () => {
  render(<FileFormatBadge value="MKA · FLAC" />);
  const badge = screen.getByText("MKA · FLAC");
  expect(badge).toHaveClass("rounded-full");
  expect(badge).toHaveClass("bg-[var(--accent-soft)]");
  expect(badge).toHaveClass("text-[var(--accent-strong)]");
  expect(badge).not.toHaveClass("bg-[var(--success-soft)]");
});

it("keeps empty format values and em-dash metadata as plain display text", () => {
  const { container, rerender } = render(<FileFormatBadge value="  " />);
  expect(container).toBeEmptyDOMElement();
  rerender(<MetadataBadge value="—" />);
  expect(container).toHaveTextContent("—");
  expect(container.querySelector(".rounded-full")).toBeNull();
});

it("uses the same format and metadata chips in mobile and desktop track views", () => {
  render(<LibraryTrackListView locale="en" rows={[row]} />);
  const mobile = screen.getByRole("list", { name: "Library tracks" });
  const desktop = screen.getByRole("grid", { name: "Library tracks" });

  for (const surface of [mobile, desktop]) {
    expect(within(surface).getByText("FLAC")).toHaveClass("rounded-full");
    expect(within(surface).getByText("MKA · FLAC")).toHaveClass("rounded-full");
    expect(within(surface).getByText("24 bit · 48 kHz")).toHaveClass(
      "rounded-full",
    );
  }
});

it("keeps matching confidence beside a publication format chip", () => {
  render(
    <LibraryAlbumPositionsView
      locale="en"
      releaseTitle="North"
      layout="desktop"
      positions={[
        {
          id: "position-1",
          numberLabel: "01",
          title: "First Light",
          recordingHref: "#/library/positions/position-1",
          tagsHref: "#/library/positions/position-1",
          sourceCount: 1,
          sourceLabel: "FLAC",
          properties: "24 bit · 48 kHz",
          publication: {
            state: "published",
            container: "MKA",
            codec: "FLAC",
            matchingConfidence: 0.92,
          },
          selection,
        },
      ]}
    />,
  );

  expect(screen.getByText("MKA · FLAC")).toHaveClass("rounded-full");
  expect(screen.getByText("· Match: 92%")).toBeInTheDocument();
  expect(screen.getByText("24 bit · 48 kHz")).toHaveClass("rounded-full");
});
