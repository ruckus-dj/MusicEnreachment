import { expect, it } from "vitest";
import { artistDeletionPlan } from "./ArtistDeletionRequest";
import {
	originalArtists,
	originalProviders,
	originalReleases,
} from "./catalogData";
import type { DeletionOperation } from "./DeletionRequest";
import { completeLibraryDeletion } from "./libraryDeletionModel";

const clone = <T>(value: T): T => structuredClone(value);

function publicationOperation(): DeletionOperation {
	const release = originalReleases.find((item) => item.id === "north");
	const track = release?.tracks.find((item) => item.id === "north-t1");
	if (!track?.published) throw new Error("Missing published track fixture");
	return {
		kind: "publication",
		target: track.title,
		scope: "confirmed publication only",
		publications: 1,
		paths: [track.published.path],
		libraryTrackIds: [track.id],
	};
}

it("removes only the confirmed publication and preserves library, sources, and provider data", () => {
	const releases = clone(originalReleases);
	const artists = clone(originalArtists);
	const providers = clone(originalProviders);
	const files = { sentinel: { available: true, path: "/source/audio.flac" } };
	const originalReleasesSnapshot = clone(releases);
	const result = completeLibraryDeletion(
		publicationOperation(),
		releases,
		artists,
	);

	expect(result).not.toBeNull();
	const selected = result?.releases
		.flatMap((release) => release.tracks)
		.find((track) => track.id === "north-t1");
	expect(selected?.published).toBeNull();
	expect(selected?.metadataBaseline).toBeUndefined();
	expect(selected?.metadataBaselineExtras).toBeUndefined();
	expect(
		result?.releases
			.flatMap((release) => release.tracks)
			.map((track) => track.id),
	).toEqual(
		originalReleasesSnapshot
			.flatMap((release) => release.tracks)
			.map((track) => track.id),
	);
	expect(result?.artists).toEqual(artists);
	expect(releases).toEqual(originalReleasesSnapshot);
	expect(providers).toEqual(originalProviders);
	expect(files).toEqual({
		sentinel: { available: true, path: "/source/audio.flac" },
	});
});

it("deletes a release and its positions while retaining other releases", () => {
	const releases = clone(originalReleases);
	const artists = clone(originalArtists);
	const target = releases.find((release) => release.id === "north");
	if (!target) throw new Error("Missing release fixture");
	const operation: DeletionOperation = {
		kind: "library",
		target: target.title,
		scope: "confirmed release",
		publications: target.tracks.filter((track) => track.published).length,
		paths: target.tracks.flatMap((track) =>
			track.published ? [track.published.path] : [],
		),
		libraryReleaseIds: [target.id],
		libraryTrackIds: target.tracks.map((track) => track.id),
	};
	const result = completeLibraryDeletion(operation, releases, artists);

	expect(result).not.toBeNull();
	expect(result?.releases.some((release) => release.id === target.id)).toBe(
		false,
	);
	expect(result?.releases.map((release) => release.id)).toEqual(
		releases
			.filter((release) => release.id !== target.id)
			.map((release) => release.id),
	);
	expect(result?.artists).toEqual(artists);
	expect(releases.some((release) => release.id === target.id)).toBe(true);
});

it("applies the artist deletion plan across releases, including shared-recording positions", () => {
	const releases = clone(originalReleases);
	const artists = clone(originalArtists);
	const artist = artists[0];
	const plan = artistDeletionPlan(artist.id, artists, releases);
	if (!plan) throw new Error("Missing artist deletion plan");
	const result = completeLibraryDeletion(plan, releases, artists);

	expect(result).not.toBeNull();
	expect(result?.artists.map((item) => item.id)).not.toContain(artist.id);
	expect(result?.releases.map((release) => release.id)).not.toEqual(
		releases.map((release) => release.id),
	);
	const removedPositionIds = new Set(plan.libraryTrackIds);
	const remainingTracks =
		result?.releases.flatMap((release) => release.tracks) ?? [];
	for (const id of removedPositionIds) {
		expect(remainingTracks.some((track) => track.id === id)).toBe(false);
	}
	const directRecordingIds = new Set(
		releases
			.flatMap((release) => release.tracks)
			.filter((track) => track.artistId === artist.id)
			.map((track) => track.recordingId)
			.filter((id): id is string => Boolean(id)),
	);
	for (const track of remainingTracks) {
		expect(directRecordingIds.has(track.recordingId ?? "")).toBe(false);
	}
	// Related releases can contain positions unrelated to the artist; they are
	// removed with the release, while unrelated releases/positions stay intact.
	for (const release of result?.releases ?? []) {
		expect(
			release.tracks.every((track) => !removedPositionIds.has(track.id)),
		).toBe(true);
	}
	expect(releases).toEqual(clone(originalReleases));
});

it.each([
	[
		"stale publication path",
		(operation: DeletionOperation) => ({
			...operation,
			paths: ["/stale/path"],
		}),
	],
	[
		"new dependency in selected release",
		(operation: DeletionOperation) => ({
			...operation,
			libraryReleaseIds: ["north"],
			libraryTrackIds: ["north-t1"],
		}),
	],
])("rejects %s without mutating its inputs", (_case, alter) => {
	const releases = clone(originalReleases);
	const artists = clone(originalArtists);
	const beforeReleases = clone(releases);
	const beforeArtists = clone(artists);
	const result = completeLibraryDeletion(
		alter(publicationOperation()),
		releases,
		artists,
	);

	expect(result).toBeNull();
	expect(releases).toEqual(beforeReleases);
	expect(artists).toEqual(beforeArtists);
});

it("rejects empty and unknown deletion identities", () => {
	const base: DeletionOperation = {
		kind: "library",
		target: "invalid",
		scope: "invalid",
		publications: 0,
		paths: [],
	};
	expect(
		completeLibraryDeletion(
			base,
			clone(originalReleases),
			clone(originalArtists),
		),
	).toBeNull();
	expect(
		completeLibraryDeletion(
			{ ...base, libraryTrackIds: ["missing-track"] },
			clone(originalReleases),
			clone(originalArtists),
		),
	).toBeNull();
	expect(
		completeLibraryDeletion(
			{ ...base, libraryArtistId: "missing-artist" },
			clone(originalReleases),
			clone(originalArtists),
		),
	).toBeNull();
});
