import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import type { IncomingGroupsBody } from "../../api/generated/client.schemas";
import { IncomingGroupsOverview } from "./IncomingGroupsOverview";

it("counts equal relative paths in different roots separately and opens a group", () => {
  const snapshot: IncomingGroupsBody = {
    revision: "r1",
    epoch: 1,
    groups: [
      {
        id: "group",
        revision: "g1",
        manual: false,
        members: ["a", "b"],
        unready_members: ["b"],
        diagnostics: [],
      },
    ],
    files: ["a", "b"].map((id) => ({
      variant_id: id,
      capture_analysis_id: `analysis-${id}`,
      capture_identity: `capture-${id}`,
      tags: {},
      locations: [
        {
          root_id: id,
          location_id: `location-${id}`,
          work_id: `work-${id}`,
          technical_identity: `technical-${id}`,
          configured_path: `/root-${id}`,
          inventory_path: `/root-${id}`,
          relative_path: "album/01.flac",
          size_bytes: 10,
          mtime: "2026-10-15T00:00:00Z",
        },
      ],
    })),
  };
  const onSelect = vi.fn();
  render(
    <IncomingGroupsOverview
      snapshot={snapshot}
      selectedGroupId="group"
      onSelect={onSelect}
    />,
  );
  expect(screen.getByText("2 / 2")).toBeInTheDocument();
  expect(screen.getByText("1 / 2")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Открыть group" }));
  expect(onSelect).toHaveBeenCalledWith("group");
});
