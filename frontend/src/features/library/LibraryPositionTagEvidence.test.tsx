import "@testing-library/jest-dom/vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { LibrarySourceLocationResponse } from "../../api/generated/client.schemas";
import { LibraryPositionTagEvidence } from "./LibraryPositionTagEvidence";

const { readLocation } = vi.hoisted(() => ({ readLocation: vi.fn() }));

vi.mock("../../api/generated/client", () => ({
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
            tags: { TITLE: [title], ALBUM: ["Same album"] },
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
  const view = render(
    <LibraryPositionTagEvidence
      locale="en"
      open
      source={{ media_variant_id: variantId, locations }}
      current={{ state: "unavailable" }}
    />,
  );
  expect(readLocation).not.toHaveBeenCalled();
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
  await act(async () => finishFirst(response(0, "Superseded tag")));
  expect(screen.queryByText("Superseded tag")).toBeNull();
  expect(screen.getByText("Second location tag")).toBeVisible();
  expect(
    screen.queryByRole("checkbox", { name: "Differences only" }),
  ).toBeNull();

  view.rerender(
    <LibraryPositionTagEvidence
      locale="en"
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
  expect(screen.getByRole("rowheader", { name: "ALBUM" })).toBeVisible();
  fireEvent.click(screen.getByRole("checkbox", { name: "Differences only" }));
  expect(screen.queryByRole("rowheader", { name: "ALBUM" })).toBeNull();
  expect(screen.getByText("Published tag")).toBeVisible();
  expect(readLocation).toHaveBeenCalledTimes(2);
});
