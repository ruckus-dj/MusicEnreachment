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
  expect(readMatchingSourcePath("/Album/Disc 1/01 - Song.flac")).toEqual({
    fileName: "01 - Song.flac",
    parents: ["Album", "Disc 1"],
    truncated: false,
  });
  expect(readMatchingSourcePath("Album/01 - Song.flac")).toEqual({
    fileName: "01 - Song.flac",
    parents: ["Album"],
    truncated: false,
  });
});

it("handles short, relative and Windows paths without inventing parents", () => {
  expect(readMatchingSourcePath("01 - Song.flac")).toEqual({
    fileName: "01 - Song.flac",
    parents: [],
    truncated: false,
  });
  expect(readMatchingSourcePath("/01 - Song.flac")).toEqual({
    fileName: "01 - Song.flac",
    parents: [],
    truncated: false,
  });
  expect(readMatchingSourcePath("C:\\Music\\Album\\01 - Song.flac")).toEqual({
    fileName: "01 - Song.flac",
    parents: ["Music", "Album"],
    truncated: true,
  });
});

it("reports no path instead of an empty file name", () => {
  expect(readMatchingSourcePath(undefined)).toBeNull();
  expect(readMatchingSourcePath("")).toBeNull();
  expect(readMatchingSourcePath("/")).toBeNull();
});
