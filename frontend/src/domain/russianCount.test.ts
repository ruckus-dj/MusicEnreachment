import { describe, expect, it } from "vitest";
import { formatRussianCount } from "./russianCount";

describe("formatRussianCount", () => {
  it.each([
    [1, "1 трек"],
    [2, "2 трека"],
    [5, "5 треков"],
    [11, "11 треков"],
    [21, "21 трек"],
    [22, "22 трека"],
    [25, "25 треков"],
  ])("formats %i with the correct Russian noun form", (count, expected) => {
    expect(formatRussianCount(count, ["трек", "трека", "треков"])).toBe(expected);
  });
});
