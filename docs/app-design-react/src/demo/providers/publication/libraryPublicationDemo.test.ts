import { expect, it } from "vitest";
import {
  metadataChanged,
  originalArtists,
  originalFiles,
  originalReleases,
} from "../library/catalogData";
import {
  planDemoPublication,
  settleDemoPublication,
} from "./libraryPublicationDemo";
import { fixturePublicationDestination } from "./publicationOutput";

it("settles an unchanged preview independently from a stale sibling", () => {
  const catalog = {
    releases: structuredClone(originalReleases),
    artists: originalArtists,
    files: originalFiles,
    destination: fixturePublicationDestination,
    inventoryVersion: 0,
  };
  const positions = ["north-t6", "north-t7"].map((position_id) => ({
    position_id,
    release_id: "north",
  }));
  const [first, second] = planDemoPublication(catalog, positions);
  expect(first.disposition).toBe("create");
  expect(second.disposition).toBe("create");
  expect(settleDemoPublication(catalog, first)?.sourceId).toBe("north-f6");

  const track = catalog.releases
    .find(({ id }) => id === "north")
    ?.tracks.find(({ id }) => id === "north-t6");
  if (!track) throw new Error("Missing position fixture");
  track.sourceId = null;
  expect(settleDemoPublication(catalog, first)).toBeNull();
  expect(settleDemoPublication(catalog, second)?.sourceId).toBe("north-f7");
  expect(track.published).toBeNull();
});

it("plans container-specific keys and detects later movement edits", () => {
  const catalog = {
    releases: structuredClone(originalReleases),
    artists: originalArtists,
    files: originalFiles,
    destination: { ...fixturePublicationDestination, format: "m4a" as const },
    inventoryVersion: 0,
  };
  const release = catalog.releases.find(({ id }) => id === "north");
  const track = release?.tracks.find(({ id }) => id === "north-t6");
  if (!release || !track) throw new Error("Missing position fixture");
  track.recordingScalarIntent = { key: { state: "value", value: ["C minor"] } };
  track.positionComposition = {
    movement: {
      state: "value",
      value: { name: "Finale", number: "2", count: "3" },
    },
  };
  const [plan] = planDemoPublication(catalog, [
    { position_id: track.id, release_id: release.id },
  ]);
  expect(plan.disposition).toBe("create");
  const output = settleDemoPublication(catalog, plan);
  if (!output) throw new Error("Missing publication result");
  expect(output.tags).toMatchObject({
    INITIALKEY: ["C minor"],
    KEY: [],
    MOVEMENTNUMBER: ["2"],
    MOVEMENTCOUNT: ["3"],
  });
  track.published = output;
  track.metadataBaseline = output.tags;
  expect(metadataChanged(release, track)).toBe(false);
  track.positionComposition = {
    movement: {
      state: "value",
      value: { name: "Finale", number: "3", count: "3" },
    },
  };
  expect(metadataChanged(release, track)).toBe(true);
});
