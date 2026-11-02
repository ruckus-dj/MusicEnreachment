import { expect, it } from "vitest";
import { originalFiles, originalReleases } from "./catalogData";
import { removeRootFromInventory } from "./sourceInventory";

it("keeps shared variants after one root is removed", () => {
	const next = removeRootFromInventory(
		originalFiles,
		originalReleases,
		"root-inbox",
	);
	expect(next.files["north-f1"].available).toBe(true);
	expect(next.files["north-f1"].locations).toHaveLength(1);
	expect(
		next.releases.find((release) => release.id === "north")?.tracks[0].sourceId,
	).toBe("north-f1");
	expect(originalFiles["north-f1"].locations).toHaveLength(2);
});

it("clears a final-location source without selecting MP3 or changing current publication", () => {
	const before = originalReleases
		.find((release) => release.id === "north")
		?.tracks.find((track) => track.id === "north-t3");
	expect(before?.sourceId).toBe("north-f3");
	const next = removeRootFromInventory(
		originalFiles,
		originalReleases,
		"root-library",
	);
	const after = next.releases
		.find((release) => release.id === "north")
		?.tracks.find((track) => track.id === "north-t3");
	expect(after?.sourceId).toBeNull();
	expect(after?.sourceIds).toEqual(["north-f3-mp3"]);
	expect(after?.published).toEqual(before?.published);
	expect(next.files["north-f3"]).toBeUndefined();
});
