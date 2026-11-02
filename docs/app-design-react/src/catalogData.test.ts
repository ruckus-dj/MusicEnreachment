import { expect, it } from "vitest";
import {
	applyMetadataPreview,
	metadataChanged,
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
