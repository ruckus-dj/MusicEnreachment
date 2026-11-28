import { expect, it } from "vitest";
import { parseMeloTroveRoute } from "../../ui/app/MeloTroveRoute";
import {
  libraryPositionHref,
  parseLibraryPositionAddress,
} from "../../ui/features/library/libraryDetailRoute";

it("preserves the publication tab and incoming origin on exact position links", () => {
  const href = libraryPositionHref("edition/x", "track?1", {
    tab: "publication",
    from: "publication-queue",
  });
  expect(href).toBe(
    "#/library/releases/edition%2Fx/positions/track%3F1?tab=publication&from=publication-queue",
  );
  expect(parseMeloTroveRoute(href)).toMatchObject({
    kind: "library-release",
    address: {
      releaseId: "edition/x",
      positionId: "track?1",
      tab: "publication",
      from: "publication-queue",
    },
  });
});

it("ignores unsupported tab and origin values without losing the position", () => {
  const href = libraryPositionHref("edition", "track");
  expect(parseLibraryPositionAddress(`${href}?tab=files&from=unknown`)).toEqual(
    {
      releaseId: "edition",
      positionId: "track",
    },
  );
});
