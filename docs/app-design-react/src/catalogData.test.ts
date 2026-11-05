import { expect, it } from "vitest";
import {
	applyMetadataPreview,
	catalogData,
	type CatalogArtist,
	desiredTags,
	metadataChanged,
	originalFiles,
	originalReleases,
	publicationSortTagCollisions,
} from "./catalogData";
import { metadataSnapshotDiff } from "./publicationMetadataDiff";

it("updates metadata without changing the existing audio codec or source", () => {
	const release = originalReleases.find((item) => item.id === "north");
	const track = release?.tracks.find((item) => item.id === "north-t2");
	expect(track?.published).toBeTruthy();
	if (!release || !track) throw new Error("Missing baseline fixture");
	expect(metadataChanged(release, track)).toBe(true);
	const next = applyMetadataPreview(originalReleases, new Set([track.id]));
	const nextRelease = next.find((item) => item.id === release.id);
	const nextTrack = nextRelease?.tracks.find((item) => item.id === track.id);
	expect(nextTrack?.published?.codec).toBe(track.published?.codec);
	expect(nextTrack?.published?.sourceId).toBe(track.published?.sourceId);
	expect(nextTrack?.published?.path).toBe(track.published?.path);
	if (!nextRelease || !nextTrack) throw new Error("Missing result fixture");
	expect(metadataChanged(nextRelease, nextTrack)).toBe(false);
	expect(metadataChanged(release, track)).toBe(true);
});

it("creates only from the explicitly selected available source and skips missing positions", () => {
	const next = applyMetadataPreview(
		originalReleases,
		new Set(["north-t6", "north-t9"]),
	);
	const tracks = next.find((release) => release.id === "north")?.tracks;
	expect(
		tracks?.find((track) => track.id === "north-t6")?.published?.sourceId,
	).toBe("north-f6");
	expect(
		tracks?.find((track) => track.id === "north-t9")?.published,
	).toBeNull();
});

it("treats case and spacing differences as exact metadata changes", () => {
	const releases = structuredClone(originalReleases);
	const release = releases.find((item) => item.id === "north");
	const track = release?.tracks.find((item) => item.id === "north-t2");
	if (!release || !track) throw new Error("Missing north-t2 fixture");
	const baseline = applyMetadataPreview(releases, new Set([track.id]));
	const baselineRelease = baseline.find((item) => item.id === release.id);
	const baselineTrack = baselineRelease?.tracks.find(
		(item) => item.id === track.id,
	);
	if (!baselineRelease || !baselineTrack)
		throw new Error("Missing projected publication baseline");
	baselineTrack.title = `${baselineTrack.title} `;
	expect(metadataChanged(baselineRelease, baselineTrack)).toBe(true);
});

it("previews explicit sort-name changes and falls back to the linked artist name", () => {
	const artists = structuredClone(catalogData.artists) as CatalogArtist[];
	const releases = structuredClone(originalReleases);
	const files = structuredClone(originalFiles);
	let release = releases.find((item) => item.id === "north-japan-local");
	let track = release?.tracks.find(
		(item) => item.id === "north-japan-local-t1",
	);
	if (!release || !track) throw new Error("Missing baseline fixture");
	const mira = artists.find((artist) => artist.id === "mira");
	if (!mira) throw new Error("Missing artist fixture");

	expect(mira.sortName).toBe("Mira Vale");
	expect(desiredTags(release, track).ARTISTSORT).toEqual(
		desiredTags(release, track, catalogData.artists).ARTISTSORT,
	);
	// Establish a valid publication snapshot from the current metadata projection
	// before asserting the artist-sort-only delta.
	const baseline = applyMetadataPreview(
		releases,
		new Set([track.id]),
		files,
		artists,
	);
	release = baseline.find((item) => item.id === release?.id);
	track = release?.tracks.find((item) => item.id === "north-japan-local-t1");
	if (!release || !track) throw new Error("Missing projected baseline fixture");
	expect(track.metadataBaseline?.ARTISTSORT).toEqual(["Mira Vale"]);
	expect(metadataChanged(release, track)).toBe(false);
	expect(metadataChanged(release, track, artists)).toBe(false);

	mira.sortName = "Vale, Mira";
	expect(metadataChanged(release, track, artists)).toBe(true);
	expect(track.published?.tags.ARTISTSORT).toEqual(["Mira Vale"]);
	expect(desiredTags(release, track, artists).ARTISTSORT).toEqual([
		"Vale, Mira",
	]);

	delete mira.sortName;
	expect(desiredTags(release, track, artists).ARTISTSORT).toEqual([
		"Mira Vale",
	]);
	expect(mira.sortName).toBeUndefined();
	mira.sortName = "Vale, Mira";

	const next = applyMetadataPreview(
		baseline,
		new Set([track.id]),
		files,
		artists,
	);
	const nextTrack = next
		.find((item) => item.id === release.id)
		?.tracks.find((item) => item.id === track.id);
	expect(track.published?.tags.ARTISTSORT).toEqual(["Mira Vale"]);
	expect(nextTrack?.published?.tags.ARTISTSORT).toEqual(["Vale, Mira"]);
});

it("derives ordered fallback sort names from linked artists, not credit overrides", () => {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north-japan-local"),
	);
	const track = release?.tracks[0];
	if (!release || !track) throw new Error("Missing local release fixture");
	const artists: CatalogArtist[] = [
		{ id: "first", name: "First Artist" },
		{ id: "middle", name: "Middle Artist" },
		{ id: "last", name: "Last Artist", sortName: "Artist, Last" },
	];
	const recordingCredits = [
		{ artistId: "first", creditName: "First Alias", joinPhrase: " & " },
		{ artistId: "middle", creditName: "Middle Alias", joinPhrase: " feat. " },
		{ artistId: "last", creditName: "Last Alias", joinPhrase: "" },
	];
	track.recordingCredits = recordingCredits;

	const tags = desiredTags(release, track, artists);
	expect(tags.ARTISTSSORT).toEqual([
		"First Artist",
		"Middle Artist",
		"Artist, Last",
	]);
	expect(tags.ARTISTSORT).toEqual([
		"First Artist & Middle Artist feat. Artist, Last",
	]);
	expect(tags.ARTISTS).toEqual(["First Alias", "Middle Alias", "Last Alias"]);
	expect(artists.slice(0, 2).map((artist) => artist.sortName)).toEqual([
		undefined,
		undefined,
	]);
	recordingCredits[1].artistId = "unknown-artist";
	const unknownLinkedArtistTags = desiredTags(release, track, artists);
	expect(unknownLinkedArtistTags.ARTISTSSORT).toEqual([
		"First Artist",
		"",
		"Artist, Last",
	]);
	expect(unknownLinkedArtistTags.ARTISTSORT).toEqual([]);
});

it("omits only the colliding credit group's sort tags and clears prior values", () => {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north-japan-local"),
	);
	const track = release?.tracks[0];
	if (!release || !track) throw new Error("Missing local release fixture");
	const artists: CatalogArtist[] = [
		{ id: "recording-a", name: "Shared" },
		{ id: "recording-b", name: "Other", sortName: "Shared" },
		{ id: "release-a", name: "Release A", sortName: "A" },
		{ id: "release-b", name: "Release B", sortName: "B" },
	];
	track.recordingCredits = [
		{ artistId: "recording-a", creditName: "A", joinPhrase: " & " },
		{ artistId: "recording-b", creditName: "B", joinPhrase: "" },
	];
	release.credits = [
		{ artistId: "release-a", credit: "Release A", joinPhrase: " & " },
		{ artistId: "release-b", credit: "Release B", joinPhrase: "" },
	];
	expect(publicationSortTagCollisions(release, track, artists)).toEqual({
		recording: true,
		release: false,
	});
	const tags = desiredTags(release, track, artists);
	expect(tags.ARTISTSORT).toEqual([]);
	expect(tags.ARTISTSSORT).toEqual([]);
	expect(tags.ARTIST).toEqual(["A & B"]);
	expect(tags.ARTISTS).toEqual(["A", "B"]);
	expect(tags.ALBUMARTISTSORT).toEqual(["A & B"]);
	expect(tags.ALBUMARTISTSSORT).toEqual(["A", "B"]);

	const diff = metadataSnapshotDiff(
		{
			ARTISTSORT: ["Old ordered sort"],
			ARTISTSSORT: ["Old A", "Old B"],
		},
		tags,
		{ coverHash: null, lyrics: null },
		{ coverHash: null, lyrics: null },
	);
	expect(diff.flatMap((group) => group.changes)).toEqual(
		expect.arrayContaining([
			expect.objectContaining({
				key: "ARTISTSORT",
				before: ["Old ordered sort"],
				after: [],
			}),
			expect.objectContaining({
				key: "ARTISTSSORT",
				before: ["Old A", "Old B"],
				after: [],
			}),
		]),
	);

	artists[1].sortName = "Different recording sort";
	artists[3].sortName = "A";
	release.credits = [
		{ artistId: "release-a", credit: "Release A", joinPhrase: " & " },
		{ artistId: "release-b", credit: "Release B", joinPhrase: "" },
	];
	track.recordingCredits = [
		{ artistId: "recording-a", creditName: "A", joinPhrase: " & " },
		{ artistId: "recording-b", creditName: "B", joinPhrase: "" },
	];
	expect(publicationSortTagCollisions(release, track, artists)).toEqual({
		recording: false,
		release: true,
	});
	const inverseTags = desiredTags(release, track, artists);
	expect(inverseTags.ARTISTSORT).toEqual(["Shared & Different recording sort"]);
	expect(inverseTags.ARTISTSSORT).toEqual([
		"Shared",
		"Different recording sort",
	]);
	expect(inverseTags.ALBUMARTISTSORT).toEqual([]);
	expect(inverseTags.ALBUMARTISTSSORT).toEqual([]);
	expect(inverseTags.ALBUMARTISTS).toEqual(["Release A", "Release B"]);
	const inverseDiff = metadataSnapshotDiff(
		{
			ALBUMARTISTSORT: ["Old release sort"],
			ALBUMARTISTSSORT: ["Old release A", "Old release B"],
		},
		inverseTags,
		{ coverHash: null, lyrics: null },
		{ coverHash: null, lyrics: null },
	);
	expect(inverseDiff.flatMap((group) => group.changes)).toEqual(
		expect.arrayContaining([
			expect.objectContaining({ key: "ALBUMARTISTSORT", after: [] }),
			expect.objectContaining({ key: "ALBUMARTISTSSORT", after: [] }),
		]),
	);
});
