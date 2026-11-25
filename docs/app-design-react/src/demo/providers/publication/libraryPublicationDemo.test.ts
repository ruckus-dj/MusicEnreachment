import { expect, it } from "vitest";
import {
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
