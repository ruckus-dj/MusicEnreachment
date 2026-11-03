import { expect, it } from "vitest";
import {
	applyMetadataPreview,
	originalArtists,
	originalFiles,
	originalReleases,
} from "./catalogData";
import {
	applyPlan,
	createIncomingState,
	makeReleasePlan,
} from "./incomingModel";

it("uses the explicit destination for new metadata-preview publications, not fixture MKA paths", () => {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north")!,
	);
	const track = release.tracks[0];
	track.published = null;
	track.sourceId = "north-f1";
	const result = applyMetadataPreview(
		[release],
		new Set([track.id]),
		originalFiles,
		originalArtists,
		{ outputDirectory: "/srv/new-output", format: "original" },
	);
	expect(result[0].tracks[0].published).toMatchObject({
		container: "FLAC",
		sourceId: "north-f1",
	});
	expect(result[0].tracks[0].published?.path).toMatch(
		/^\/srv\/new-output\/.*\.flac$/,
	);
	expect(track.published).toBeNull();
});

it("uses the explicit destination for first audio publication and preserves CURRENT during metadata-only updates", () => {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north")!,
	);
	const track = release.tracks[0];
	track.published = null;
	track.sourceId = "north-f1";
	const plan = makeReleasePlan(
		createIncomingState(),
		release,
		originalFiles,
		track.id,
	);
	expect(
		applyPlan(
			plan,
			originalFiles,
			[release],
			undefined,
			false,
			originalArtists,
			{ outputDirectory: "/srv/new-output", format: "original" },
		),
	).toBe(1);
	const path = release.tracks[0].published?.path;
	expect(path).toMatch(/^\/srv\/new-output\/.*\.flac$/);
	track.title = "Corrected title";
	const updated = applyMetadataPreview(
		[release],
		new Set([track.id]),
		originalFiles,
		originalArtists,
		{ outputDirectory: "/srv/another-output", format: "mka" },
	);
	expect(updated[0].tracks[0].published).toMatchObject({
		path,
		container: "FLAC",
		tags: { TITLE: ["Corrected title"] },
	});
});
