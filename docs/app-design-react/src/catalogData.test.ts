import { expect, it } from "vitest";
import {
	applyMetadataPreview,
	catalogData,
	type CatalogArtist,
	desiredTags,
	metadataChanged,
	originalFiles,
	originalReleases,
} from "./catalogData";

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

it("previews an explicit artist sort name change without a display-name fallback", () => {
	const artists = structuredClone(catalogData.artists) as CatalogArtist[];
	const releases = structuredClone(originalReleases);
	const files = structuredClone(originalFiles);
	const release = releases.find((item) => item.id === "north-japan-local");
	const track = release?.tracks.find(
		(item) => item.id === "north-japan-local-t1",
	);
	if (!release || !track) throw new Error("Missing baseline fixture");
	const mira = artists.find((artist) => artist.id === "mira");
	if (!mira) throw new Error("Missing artist fixture");

	expect(track.metadataBaseline?.ARTISTSORT).toEqual(["Mira Vale"]);
	expect(mira.sortName).toBe("Mira Vale");
	expect(metadataChanged(release, track)).toBe(false);
	expect(metadataChanged(release, track, artists)).toBe(false);

	mira.sortName = "Vale, Mira";
	expect(metadataChanged(release, track, artists)).toBe(true);
	expect(track.published?.tags.ARTISTSORT).toEqual(["Mira Vale"]);
	expect(desiredTags(release, track, artists).ARTISTSORT).toEqual([
		"Vale, Mira",
	]);

	delete mira.sortName;
	expect(desiredTags(release, track, artists).ARTISTSORT).toEqual([]);
	mira.sortName = "Vale, Mira";

	const next = applyMetadataPreview(
		releases,
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
