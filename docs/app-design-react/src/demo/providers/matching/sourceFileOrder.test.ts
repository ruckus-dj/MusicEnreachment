import { expect, it } from "vitest";
import type { SourceFileSort } from "../../../ui/features/matching/sourceFileOrder";
import {
  compareSourceFiles,
  readSourceTrackOrder,
} from "../../../ui/features/matching/sourceFileOrder";

const collator = new Intl.Collator("en", {
  numeric: true,
  sensitivity: "base",
});

type OrderEntry = {
  name: string;
  discNumber?: number;
  trackNumber?: number;
};

function sortedNames(
  entries: readonly OrderEntry[],
  mode: SourceFileSort = "track",
) {
  return entries
    .map((entry) => ({ ...entry }))
    .sort((a, b) => compareSourceFiles(a, b, mode, collator))
    .map((entry) => entry.name);
}

it("reads disc and track positions from primary and alias tags", () => {
  expect(
    readSourceTrackOrder({ DISCNUMBER: ["2"], TRACKNUMBER: ["07"] }),
  ).toEqual({ discNumber: 2, trackNumber: 7 });
  expect(readSourceTrackOrder({ DISC: ["3"], TRACK: ["4"] })).toEqual({
    discNumber: 3,
    trackNumber: 4,
  });
  expect(
    readSourceTrackOrder({ "DISC NUMBER": ["1"], "TRACK NUMBER": ["11"] }),
  ).toEqual({ discNumber: 1, trackNumber: 11 });
  expect(
    readSourceTrackOrder({ DISC_NUMBER: ["4"], TRACK_NUMBER: ["12"] }),
  ).toEqual({ discNumber: 4, trackNumber: 12 });
  expect(
    readSourceTrackOrder({ PARTNUMBER: ["2"], TRACKNUMBER: [" 05 "] }),
  ).toEqual({ discNumber: 2, trackNumber: 5 });
});

it("rejects non-atomic, non-positive or unsafe positions instead of guessing", () => {
  for (const value of [
    "",
    "   ",
    "0",
    "-1",
    "1.5",
    "1/10",
    "A1",
    "0x10",
    "9007199254740993",
  ]) {
    const order = readSourceTrackOrder({
      DISCNUMBER: [value],
      TRACKNUMBER: [value],
    });
    expect(order.discNumber).toBeUndefined();
    expect(order.trackNumber).toBeUndefined();
  }
});

it("falls through to the next alias when the first position is unusable", () => {
  expect(
    readSourceTrackOrder({ TRACKNUMBER: ["0"], TRACK: ["8"] }).trackNumber,
  ).toBe(8);
  expect(
    readSourceTrackOrder({ DISCNUMBER: ["A1"], DISC: ["2"] }).discNumber,
  ).toBe(2);

  const empty = readSourceTrackOrder();
  expect(empty.discNumber).toBeUndefined();
  expect(empty.trackNumber).toBeUndefined();
});

it("orders tagged files by disc and then numeric track", () => {
  expect(
    sortedNames([
      { name: "disc-two", discNumber: 2, trackNumber: 1 },
      { name: "ten", discNumber: 1, trackNumber: 10 },
      { name: "two", discNumber: 1, trackNumber: 2 },
    ]),
  ).toEqual(["two", "ten", "disc-two"]);
});

it("treats a missing disc as disc one", () => {
  expect(
    sortedNames([
      { name: "explicit-second", discNumber: 1, trackNumber: 2 },
      { name: "disc-two", discNumber: 2, trackNumber: 1 },
      { name: "implicit-first", trackNumber: 1 },
    ]),
  ).toEqual(["implicit-first", "explicit-second", "disc-two"]);
});

it("uses tag positions rather than numbers guessed from filenames", () => {
  expect(
    sortedNames([
      { name: "01 - Zulu.flac", trackNumber: 2 },
      { name: "02 - Alpha.flac", trackNumber: 1 },
    ]),
  ).toEqual(["02 - Alpha.flac", "01 - Zulu.flac"]);
});

it("keeps missing or malformed track positions after tagged files", () => {
  const files: { name: string; tags: Record<string, string[]> }[] = [
    { name: "01 - Missing.flac", tags: {} },
    { name: "99 - Tagged.flac", tags: { TRACKNUMBER: ["3"] } },
    { name: "02 - Malformed.flac", tags: { TRACKNUMBER: ["1/10"] } },
  ];
  const entries = files.map(({ name, tags }) => ({
    name,
    ...readSourceTrackOrder(tags),
  }));

  expect(sortedNames(entries)).toEqual([
    "99 - Tagged.flac",
    "01 - Missing.flac",
    "02 - Malformed.flac",
  ]);
});

it("finds a valid position among normalized tag aliases and values", () => {
  expect(
    readSourceTrackOrder({ " track ": ["", "03"], disc: ["002"] }),
  ).toEqual({
    discNumber: 2,
    trackNumber: 3,
  });
});

it("falls back to natural filename order for equal or absent positions", () => {
  expect(
    sortedNames([
      { name: "Track 10.flac", discNumber: 1, trackNumber: 1 },
      { name: "Track 2.flac", discNumber: 1, trackNumber: 1 },
    ]),
  ).toEqual(["Track 2.flac", "Track 10.flac"]);
  expect(
    sortedNames([{ name: "Track 10.flac" }, { name: "Track 2.flac" }]),
  ).toEqual(["Track 2.flac", "Track 10.flac"]);
});

it("ignores tag positions in name mode", () => {
  expect(
    sortedNames(
      [
        { name: "02 - Second.flac", trackNumber: 1 },
        { name: "01 - First.flac", trackNumber: 9 },
      ],
      "name",
    ),
  ).toEqual(["01 - First.flac", "02 - Second.flac"]);
});
