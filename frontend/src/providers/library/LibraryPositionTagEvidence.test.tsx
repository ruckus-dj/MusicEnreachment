import "@testing-library/jest-dom/vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { LibrarySourceLocationResponse } from "../../api/generated/client.schemas";
import { LibraryPositionTagEvidence } from "./LibraryPositionTagEvidence";

const { readLocation, readComparison } = vi.hoisted(() => ({
  readLocation: vi.fn(),
  readComparison: vi.fn(),
}));

vi.mock("../../api/generated/client", () => ({
  getLibraryPositionTagComparison: readComparison,
  getSourceLocation: readLocation,
}));

it("compares only the chosen location and distinguishes missing CURRENT evidence", async () => {
  const variantId = "variant";
  const locations = ["first", "second"].map(
    (locationId): LibrarySourceLocationResponse => ({
      location_id: locationId,
      root_id: `root-${locationId}`,
      probe_status: "succeeded",
      relative_path: `${locationId}.flac`,
      root_enabled: true,
      root_status: "available",
      size_bytes: 1234,
    }),
  );
  const response = (index: number, title: string) => ({
    status: 200,
    data: {
      ...locations[index],
      media_variant_id: variantId,
      steps: [
        {
          name: "metadata",
          metadata: {
            tags: { TITLE: [title], ALBUM: ["Same album"], COMMENT: [""] },
            provenance: { name: "test reader", version: "1", contract: "test" },
          },
        },
      ],
    },
  });
  let finishFirst!: (value: ReturnType<typeof response>) => void;
  const first = new Promise<ReturnType<typeof response>>((resolve) => {
    finishFirst = resolve;
  });
  readLocation
    .mockReturnValueOnce(first)
    .mockResolvedValueOnce(response(1, "Second location tag"));
  readComparison.mockResolvedValue({
    status: 200,
    data: {
      provider: { name: "MusicBrainz", status: "unavailable" },
      overrides: { ASIN: [] },
    },
  });
  const view = render(
    <LibraryPositionTagEvidence
      locale="en"
      releaseId="release"
      positionId="position"
      open
      source={{ media_variant_id: variantId, locations }}
      current={{ state: "unavailable" }}
    />,
  );
  expect(readLocation).not.toHaveBeenCalled();
  expect(readComparison).toHaveBeenCalledWith(
    "release",
    "position",
    expect.objectContaining({ cache: "no-store" }),
  );
  expect(
    screen.getByText("The current publication tag snapshot is unavailable."),
  ).toBeVisible();
  expect(
    screen.queryByText("This position has no current publication."),
  ).toBeNull();

  const location = screen.getByRole("combobox", { name: "Source location" });
  fireEvent.change(location, { target: { value: "first" } });
  expect(readLocation).toHaveBeenNthCalledWith(
    1,
    "root-first",
    "first",
    expect.objectContaining({ cache: "no-store" }),
  );
  fireEvent.change(location, { target: { value: "second" } });
  expect(readLocation).toHaveBeenNthCalledWith(
    2,
    "root-second",
    "second",
    expect.objectContaining({ cache: "no-store" }),
  );
  expect(readLocation.mock.calls[0][2].signal.aborted).toBe(true);
  expect(await screen.findByText("Second location tag")).toBeVisible();
  expect(
    screen.getByRole("columnheader", { name: "Provider · MusicBrainz" }),
  ).toBeVisible();
  expect(screen.getByText("Provider tags are unavailable.")).toBeVisible();
  expect(screen.getByText("Cleared")).toBeVisible();
  expect(screen.queryByRole("rowheader", { name: / · ISRC$/ })).toBeNull();
  fireEvent.click(screen.getByRole("checkbox", { name: "Show all" }));
  expect(screen.getByRole("rowheader", { name: / · ISRC$/ })).toBeVisible();
  fireEvent.click(screen.getByRole("checkbox", { name: "Show all" }));
  await act(async () => finishFirst(response(0, "Superseded tag")));
  expect(screen.queryByText("Superseded tag")).toBeNull();
  expect(screen.getByText("Second location tag")).toBeVisible();
  expect(
    screen.queryByRole("checkbox", { name: "Differences only" }),
  ).toBeNull();

  view.rerender(
    <LibraryPositionTagEvidence
      locale="en"
      releaseId="release"
      positionId="position"
      open
      source={{ media_variant_id: variantId, locations }}
      current={{
        state: "saved",
        tags: { TITLE: ["Published tag"], ALBUM: ["Same album"] },
      }}
    />,
  );
  expect(
    screen.getByRole("columnheader", { name: "Source file" }),
  ).toBeVisible();
  expect(
    screen.getByRole("columnheader", { name: "Current publication" }),
  ).toBeVisible();
  expect(screen.getByRole("rowheader", { name: / · ALBUM$/ })).toBeVisible();
  expect(screen.queryByRole("rowheader", { name: / · COMMENT$/ })).toBeNull();
  fireEvent.click(screen.getByRole("checkbox", { name: "Differences only" }));
  expect(screen.queryByRole("rowheader", { name: / · ALBUM$/ })).toBeNull();
  expect(screen.getByRole("rowheader", { name: / · COMMENT$/ })).toBeVisible();
  expect(screen.getByText("Published tag")).toBeVisible();
  expect(readLocation).toHaveBeenCalledTimes(2);
  expect(readComparison).toHaveBeenCalledTimes(1);
});
