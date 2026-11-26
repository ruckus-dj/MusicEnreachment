import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import { originalReleases } from "./catalogData";
import { TagsComparison } from "./TagsComparison";

it("hides empty fields until show all and keeps an explicit clear visible", () => {
  const release = structuredClone(originalReleases[0]);
  const track = release.tracks[0];
  track.providerTrackId = null;
  delete track.published?.tags.COMMENT;

  const view = render(
    <TagsComparison release={release} track={track} files={{}} />,
  );
  expect(screen.queryByRole("row", { name: /COMMENT/ })).toBeNull();
  expect(
    screen.getByText("У этой позиции нет связанного провайдера."),
  ).toBeVisible();

  fireEvent.click(screen.getByRole("checkbox", { name: "Показать все" }));
  const providerCell = within(
    screen.getByRole("row", { name: /COMMENT/ }),
  ).getAllByRole("cell")[1];
  expect(providerCell).toHaveTextContent("—");
  expect(providerCell).not.toHaveTextContent("MusicBrainz");
  view.unmount();

  track.recordingComparisonOverrides = { comment: [] };
  render(<TagsComparison release={release} track={track} files={{}} />);
  const overrideCell = within(
    screen.getByRole("row", { name: /COMMENT/ }),
  ).getAllByRole("cell")[2];
  expect(overrideCell).toHaveTextContent("Очищено");
});
