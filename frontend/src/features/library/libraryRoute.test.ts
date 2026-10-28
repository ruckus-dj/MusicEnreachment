import { expect, it } from "vitest";
import {
  parseLibraryArtistRoute,
  parseLibraryPositionRoute,
  parseLibraryReleaseRoute,
  parseLibraryRoute,
} from "./libraryRoute";

const releaseId = "3f1c8e59-2a4b-4c7d-9e0f-1a2b3c4d5e6f";
const positionId = "4f1c8e59-2a4b-4c7d-9e0f-1a2b3c4d5e6f";

it("parses exact UUID release and position routes", () => {
  expect(parseLibraryReleaseRoute(`#/library/releases/${releaseId}`)).toEqual({
    releaseId,
  });
  expect(
    parseLibraryPositionRoute(
      `#/library/releases/${releaseId}/positions/${positionId}`,
    ),
  ).toEqual({ releaseId, positionId });
  expect(
    parseLibraryPositionRoute(
      `#/library/releases/${releaseId}/positions/${positionId}?from=catalog`,
    ),
  ).toEqual({ releaseId, positionId });
});

it("does not accept malformed UUIDs or extra path segments", () => {
  expect(
    parseLibraryReleaseRoute("#/library/releases/not-a-uuid"),
  ).toBeUndefined();
  expect(
    parseLibraryPositionRoute(
      `#/library/releases/${releaseId}/positions/not-a-uuid`,
    ),
  ).toBeUndefined();
  expect(
    parseLibraryPositionRoute(
      `#/library/releases/${releaseId}/positions/${positionId}/extra`,
    ),
  ).toBeUndefined();
});

it("keeps the library landing and artist route parsers unchanged", () => {
  expect(parseLibraryRoute("#/library?view=tracks&q=blue")).toMatchObject({
    view: "tracks",
    query: "blue",
  });
  expect(
    parseLibraryArtistRoute("#/library/artists/artist-id?view=albums"),
  ).toMatchObject({ artistId: "artist-id", view: "albums" });
});
