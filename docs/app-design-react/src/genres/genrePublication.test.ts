import { expect, it } from "vitest";
import {
	applyMetadataPreview,
	desiredTags,
	originalArtists,
	originalFiles,
	originalReleases,
} from "../catalogData";
import { genreCatalog } from "./catalogue";

it("writes automatic catalog names to GENRE without changing source metadata or current publications", () => {
	const release = structuredClone(originalReleases[0]);
	const track = release.tracks[0];
	const genre = genreCatalog.find(
		(item) => item.sourceName === "alternative r&b",
	);
	if (!genre) throw new Error("Missing MusicBrainz genre fixture");
	const before = structuredClone(track.published);
	track.recordingGenres = [genre.sourceName, "Uncatalogued user value"];
	track.recordingGenreRefs = [
		{ id: genre.id, name: genre.sourceName },
		{ id: "legacy-local", name: "Uncatalogued user value" },
	];
	expect(desiredTags(release, track).GENRE).toEqual([
		"Alternative R&B",
		"Uncatalogued user value",
	]);
	expect(track.recordingGenres).toEqual([
		genre.sourceName,
		"Uncatalogued user value",
	]);
	expect(track.published).toEqual(before);
	const next = applyMetadataPreview(
		[release],
		new Set([track.id]),
		originalFiles,
		originalArtists,
	);
	expect(next[0].tracks[0].published?.tags.GENRE).toEqual([
		"Alternative R&B",
		"Uncatalogued user value",
	]);
});
