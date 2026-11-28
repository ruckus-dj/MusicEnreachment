import { describe, expect, it } from "vitest";
import {
  libraryPositionHref,
  parseLibraryPositionAddress,
} from "./libraryDetailRoute";

describe("library position route", () => {
  it("parses the publication tab and queue return context", () => {
    expect(
      parseLibraryPositionAddress(
        "#/library/releases/north-japan-local/positions/north-japan-local-t2?tab=publication&from=publication-queue",
      ),
    ).toEqual({
      releaseId: "north-japan-local",
      positionId: "north-japan-local-t2",
      tab: "publication",
      from: "publication-queue",
    });
  });

  it("ignores unknown query values and keeps the bare address", () => {
    expect(
      parseLibraryPositionAddress(
        "#/library/releases/release/positions/position?tab=unknown&from=elsewhere",
      ),
    ).toEqual({ releaseId: "release", positionId: "position" });
  });

  it("validates raw path segments before decoding", () => {
    const route = "#/library/releases/release/positions/position?tab=metadata";
    expect(
      parseLibraryPositionAddress(route, (raw) => raw === "release"),
    ).toBeUndefined();
    expect(parseLibraryPositionAddress(route, () => true)?.tab).toBe(
      "metadata",
    );
  });

  it("builds canonical addresses with optional query state", () => {
    expect(libraryPositionHref("release", "position")).toBe(
      "#/library/releases/release/positions/position",
    );
    expect(
      libraryPositionHref("release", "position", {
        tab: "publication",
        from: "publication-queue",
      }),
    ).toBe(
      "#/library/releases/release/positions/position?tab=publication&from=publication-queue",
    );
  });
});
