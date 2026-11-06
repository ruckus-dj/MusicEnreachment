import { expect, it } from "vitest";
import {
	applyMetadataPreview,
	type CatalogArtist,
	desiredTags,
	originalArtists,
	originalFiles,
	originalReleases,
	publicationMetadataIssues,
} from "../catalogData";
import { genreCatalog } from "./catalogue";

function catalogGenre(sourceName: string) {
	const genre = genreCatalog.find((item) => item.sourceName === sourceName);
	if (!genre)
		throw new Error(`Missing MusicBrainz genre fixture: ${sourceName}`);
	return genre;
}

function northTrack(trackId = "north-t1") {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north"),
	);
	const track = release?.tracks.find((item) => item.id === trackId);
	if (!release || !track) throw new Error("Missing north track fixture");
	return { release, track };
}

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

it("falls back from recording to release to recording artist genres", () => {
	const recordingGenre = catalogGenre("alternative r&b");
	const releaseGenre = catalogGenre("ambient");
	const artistGenre = catalogGenre("folk");
	const { release, track } = northTrack();
	delete track.recordingMetadata;
	delete track.recordingGenreRefs;

	track.recordingGenres = [recordingGenre.sourceName];
	release.metadataGenres = [
		{ id: releaseGenre.id, name: releaseGenre.sourceName },
	];
	const artists: CatalogArtist[] = originalArtists.map((artist) =>
		artist.id === track.artistId
			? {
					...artist,
					genres: [{ id: artistGenre.id, name: artistGenre.sourceName }],
				}
			: artist,
	);
	expect(desiredTags(release, track, artists).GENRE).toEqual([
		recordingGenre.name,
	]);

	delete track.recordingGenres;
	expect(desiredTags(release, track, artists).GENRE).toEqual([
		releaseGenre.name,
	]);

	release.metadataGenres = [];
	expect(desiredTags(release, track, artists).GENRE).toEqual([
		artistGenre.name,
	]);
});

it("keeps recording genres ahead of release metadata genre overrides", () => {
	const recordingGenre = catalogGenre("ambient");
	const releaseOverride = catalogGenre("folk");
	const { release, track } = northTrack();
	delete track.recordingMetadata;
	delete track.recordingGenreRefs;
	track.recordingGenres = [recordingGenre.sourceName];

	release.metadataValues = { genre: [] };
	expect(desiredTags(release, track).GENRE).toEqual([recordingGenre.name]);

	release.metadataValues = { genre: [releaseOverride.sourceName] };
	expect(desiredTags(release, track).GENRE).toEqual([recordingGenre.name]);
});

it("unions recording artist genres by MBID in credit and genre order", () => {
	const firstGenre = catalogGenre("alternative r&b");
	const sharedGenre = catalogGenre("ambient");
	const lastGenre = catalogGenre("folk");
	const { release, track } = northTrack();
	delete track.recordingGenres;
	delete track.recordingGenreRefs;
	delete release.metadataGenres;
	const artists: CatalogArtist[] = [
		{
			id: "credit-a",
			name: "Credit A",
			genres: [
				{ id: firstGenre.id, name: firstGenre.sourceName },
				{ id: sharedGenre.id, name: sharedGenre.sourceName },
			],
		},
		{
			id: "credit-b",
			name: "Credit B",
			genres: [
				{ id: sharedGenre.id, name: sharedGenre.sourceName },
				{ id: lastGenre.id, name: lastGenre.sourceName },
			],
		},
	];
	const before = structuredClone(artists);
	track.recordingCredits = [
		{ artistId: "credit-a", creditName: "A", joinPhrase: " & " },
		{ artistId: "credit-b", creditName: "B", joinPhrase: " & " },
		{ artistId: "credit-a", creditName: "A", joinPhrase: "" },
	];

	expect(desiredTags(release, track, artists).GENRE).toEqual([
		firstGenre.name,
		sharedGenre.name,
		lastGenre.name,
	]);
	expect(artists).toEqual(before);
});

it("lets a nonempty recording genre override fallback and an explicit empty stop it", () => {
	const fallbackGenre = catalogGenre("ambient");
	const overrideGenre = catalogGenre("folk");
	const { release, track } = northTrack();
	track.recordingGenres = [fallbackGenre.sourceName];
	release.metadataGenres = [
		{ id: fallbackGenre.id, name: fallbackGenre.sourceName },
	];

	track.recordingMetadata = { genre: [overrideGenre.sourceName] };
	expect(desiredTags(release, track).GENRE).toEqual([overrideGenre.name]);

	track.recordingMetadata = { genre: [] };
	expect(desiredTags(release, track).GENRE).toEqual([]);
});

it("applies release metadata genre overrides, including explicit empty", () => {
	const releaseGenre = catalogGenre("ambient");
	const overrideGenre = catalogGenre("folk");
	const { release, track } = northTrack();
	delete track.recordingGenres;
	delete track.recordingGenreRefs;
	delete track.recordingMetadata;
	release.metadataGenres = [
		{ id: releaseGenre.id, name: releaseGenre.sourceName },
	];
	const artists: CatalogArtist[] = originalArtists.map((artist) =>
		artist.id === track.artistId
			? {
					...artist,
					genres: [{ id: releaseGenre.id, name: releaseGenre.sourceName }],
				}
			: artist,
	);

	release.metadataValues = { genre: [] };
	expect(desiredTags(release, track, artists).GENRE).toEqual([]);

	release.metadataValues = { genre: [overrideGenre.sourceName] };
	expect(desiredTags(release, track, artists).GENRE).toEqual([
		overrideGenre.name,
	]);
});

it("publishes release metadata genre overrides without scope errors", () => {
	const overrideGenre = catalogGenre("folk");

	const empty = northTrack("north-t6");
	delete empty.track.recordingGenres;
	delete empty.track.recordingGenreRefs;
	delete empty.track.recordingMetadata;
	empty.release.metadataValues = { genre: [] };
	expect(publicationMetadataIssues(empty.release, empty.track)).toEqual([]);
	const emptyNext = applyMetadataPreview(
		[empty.release],
		new Set([empty.track.id]),
		originalFiles,
		originalArtists,
	);
	expect(
		emptyNext[0].tracks.find((track) => track.id === empty.track.id)?.published
			?.tags.GENRE,
	).toEqual([]);

	const nonempty = northTrack("north-t6");
	delete nonempty.track.recordingGenres;
	delete nonempty.track.recordingGenreRefs;
	delete nonempty.track.recordingMetadata;
	nonempty.release.metadataValues = {
		genre: [overrideGenre.sourceName],
	};
	expect(publicationMetadataIssues(nonempty.release, nonempty.track)).toEqual(
		[],
	);
	const next = applyMetadataPreview(
		[nonempty.release],
		new Set([nonempty.track.id]),
		originalFiles,
		originalArtists,
	);
	expect(
		next[0].tracks.find((track) => track.id === nonempty.track.id)?.published
			?.tags.GENRE,
	).toEqual([overrideGenre.name]);
});
