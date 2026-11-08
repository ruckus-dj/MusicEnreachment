import { expect, it } from "vitest";
import { durationForSave, formatDuration } from "./duration";

it("preserves fractional milliseconds until the duration is edited", () => {
  const storedMilliseconds = 223456;

  expect(formatDuration(storedMilliseconds)).toBe("3:43");
  expect(durationForSave(storedMilliseconds, false, "3:43")).toBe(
    storedMilliseconds,
  );
  expect(durationForSave(storedMilliseconds, true, "3:42")).toBe(222000);
  expect(durationForSave(storedMilliseconds, true, "")).toBeUndefined();
});
