import { expect, it } from "vitest";
import { readMatchingSourcePath } from "../../ui/features/matching/matchingSourcePath";

it("keeps the filename and at most two nearest parents", () => {
  expect(
    readMatchingSourcePath("/music/Artist/Album/Disc 1/01 - Song.flac"),
  ).toEqual({
    fileName: "01 - Song.flac",
    parents: ["Album", "Disc 1"],
    truncated: true,
  });
  expect(readMatchingSourcePath("Album/01 - Song.flac")).toEqual({
    fileName: "01 - Song.flac",
    parents: ["Album"],
    truncated: false,
  });
  expect(
    readMatchingSourcePath("C:\\Music\\Album\\01 - Song.flac")?.parents,
  ).toEqual(["Music", "Album"]);
  expect(readMatchingSourcePath(undefined)).toBeNull();
});
