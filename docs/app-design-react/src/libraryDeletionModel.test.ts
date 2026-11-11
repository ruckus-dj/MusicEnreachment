import { expect, it } from "vitest";
import { artistDeletionPlan } from "./ArtistDeletionRequest";
import {
	originalArtists,
	originalProviders,
	originalReleases,
} from "./catalogData";
import type { DeletionOperation } from "./DeletionRequest";
import {
	completeLibraryDeletion,
	completeManagedLibraryDeletion,
	managedDeletionObservation,
	type ManagedDeletionEvidence,
} from "./libraryDeletionModel";

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

const managedEvidence = (
	operation: DeletionOperation,
	state: "present" | "absent" | "replaced" | "unknown" = "present",
	metadataCommitSucceeded = true,
): ManagedDeletionEvidence => ({
	paths: Object.fromEntries(
		operation.paths.map((path) => [
			path,
			{ state, deleteSucceeded: state === "present" },
		]),
	),
	metadataCommitSucceeded,
});

it.each([
	[{ state: "missing", rootAvailable: true }, "absent"],
	[{ state: "missing", rootAvailable: false }, "unknown"],
	[{ state: "missing" }, "unknown"],
	[{ state: "changed", rootAvailable: true }, "replaced"],
	[{ state: "unchanged", rootAvailable: true }, "present"],
	[{ state: "unchanged", rootAvailable: false }, "unknown"],
	[{ state: "unchecked" }, "unknown"],
] as const)("maps managed observations safely: %j", (observation, expected) => {
	expect(managedDeletionObservation(observation)).toBe(expected);
});

it("completes a validated managed deletion without mutating authoritative inputs", () => {
	const releases = clone(originalReleases);
	const artists = clone(originalArtists);
	const before = clone({ releases, artists });
	const operation = publicationOperation();
	const result = completeManagedLibraryDeletion(
		operation,
		releases,
		artists,
		managedEvidence(operation),
	);
	expect(result.status).toBe("completed");
	if (result.status !== "completed")
		throw new Error("Expected successful baseline");
	expect(
		result.releases
			.flatMap((release) => release.tracks)
			.find((track) => track.id === operation.libraryTrackIds?.[0])?.published,
	).toBeNull();
	expect({ releases, artists }).toEqual(before);
});

it("retries metadata cleanup after unlink without deleting a replacement", () => {
	const releases = clone(originalReleases);
	const artists = clone(originalArtists);
	const before = clone({ releases, artists });
	const operation = publicationOperation();
	expect(
		completeManagedLibraryDeletion(
			operation,
			releases,
			artists,
			managedEvidence(operation, "present", false),
		).status,
	).toBe("retry");
	expect(
		completeManagedLibraryDeletion(
			operation,
			releases,
			artists,
			managedEvidence(operation, "absent"),
		).status,
	).toBe("completed");
	expect(
		completeManagedLibraryDeletion(
			operation,
			releases,
			artists,
			managedEvidence(operation, "replaced"),
		).status,
	).toBe("conflict");
	expect({ releases, artists }).toEqual(before);
});

it("does not interpret unavailable or unobserved paths as absence", () => {
	const operation = publicationOperation();
	for (const evidence of [
		managedEvidence(operation, "unknown"),
		{ paths: {}, metadataCommitSucceeded: true },
	]) {
		expect(
			completeManagedLibraryDeletion(
				operation,
				clone(originalReleases),
				clone(originalArtists),
				evidence,
			).status,
		).toBe("retry");
	}
	const evidence = managedEvidence(operation);
	const path = operation.paths[0];
	evidence.paths[path] = { state: "present", deleteSucceeded: false };
	expect(
		completeManagedLibraryDeletion(
			operation,
			clone(originalReleases),
			clone(originalArtists),
			evidence,
		).status,
	).toBe("retry");
});

it("rejects stale scope before applying managed deletion outcomes", () => {
	const operation = publicationOperation();
	const releases = clone(originalReleases);
	const artists = clone(originalArtists);
	const before = clone({ releases, artists });
	expect(
		completeManagedLibraryDeletion(
			{ ...operation, paths: ["/stale"] },
			releases,
			artists,
			managedEvidence(operation),
		),
	).toEqual({ status: "stale" });
	expect({ releases, artists }).toEqual(before);
});

it("commits successful publication rows while retaining unresolved aggregate dependencies", () => {
	const releases = clone(originalReleases);
	const artists = clone(originalArtists);
	const release = releases.find((item) => item.id === "north");
	if (!release) throw new Error("Missing release fixture");
	const operation: DeletionOperation = {
		kind: "library",
		target: release.title,
		scope: "confirmed release",
		publications: release.tracks.filter((track) => track.published).length,
		paths: release.tracks.flatMap((track) =>
			track.published ? [track.published.path] : [],
		),
		libraryReleaseIds: [release.id],
		libraryTrackIds: release.tracks.map((track) => track.id),
	};
	expect(operation.paths.length).toBeGreaterThan(1);
	expect(
		completeManagedLibraryDeletion(
			operation,
			releases,
			artists,
			managedEvidence(operation),
		).status,
	).toBe("completed");
	const blockedPath = operation.paths[0];
	const unselectedOwner = releases
		.flatMap((item) => item.tracks)
		.find(
			(track) =>
				!operation.libraryTrackIds?.includes(track.id) && track.published,
		);
	if (!blockedPath || !unselectedOwner?.published)
		throw new Error("Missing shared-path fixture");
	unselectedOwner.published = {
		...unselectedOwner.published,
		path: blockedPath,
	};
	const evidence = managedEvidence(operation);
	const failedPath = operation.paths.at(-1) ?? "";
	evidence.metadataCommitSucceeded = false;
	for (const path of operation.paths.filter((item) => item !== failedPath))
		evidence.paths[path] = {
			state: "present",
			deleteSucceeded: true,
			metadataCommitSucceeded: true,
		};
	evidence.paths[failedPath] = {
		state: "replaced",
		deleteSucceeded: false,
	};
	const result = completeManagedLibraryDeletion(
		operation,
		releases,
		artists,
		evidence,
	);
	expect(result.status).toBe("conflict");
	if (result.status !== "conflict") throw new Error("Expected path conflict");
	expect(result.residualOperation.paths).toEqual([blockedPath, failedPath]);
	expect(result.residualOperation.libraryReleaseIds).toEqual([release.id]);
	expect(
		result.releases
			.find((item) => item.id === release.id)
			?.tracks.filter((track) => track.published)
			.map((track) => track.published?.path),
	).toEqual([blockedPath, failedPath]);
	expect(
		result.releases
			.flatMap((item) => item.tracks)
			.find((track) => track.id === unselectedOwner.id)?.published?.path,
	).toBe(blockedPath);
	expect(result.releases.some((item) => item.id === release.id)).toBe(true);
	expect(result.artists).toEqual(artists);
	const retryReleases = clone(result.releases);
	const retryOwner = retryReleases
		.flatMap((item) => item.tracks)
		.find((track) => track.id === unselectedOwner.id);
	if (!retryOwner) throw new Error("Missing retry owner fixture");
	retryOwner.published = null;
	const retry = completeManagedLibraryDeletion(
		result.residualOperation,
		retryReleases,
		result.artists,
		managedEvidence(result.residualOperation),
	);
	expect(retry.status).toBe("completed");
	if (retry.status !== "completed")
		throw new Error("Expected retry completion");
	expect(retry.releases.some((item) => item.id === release.id)).toBe(false);
});

it("requires the metadata commit even when no managed paths exist", () => {
	const releases = clone(originalReleases);
	const artists = clone(originalArtists);
	const operation = publicationOperation();
	const selected = releases
		.flatMap((release) => release.tracks)
		.find((track) => track.id === operation.libraryTrackIds?.[0]);
	if (!selected) throw new Error("Missing track fixture");
	selected.published = null;
	const noPaths = { ...operation, publications: 0, paths: [] };
	expect(
		completeManagedLibraryDeletion(noPaths, releases, artists, {
			paths: {},
			metadataCommitSucceeded: true,
		}).status,
	).toBe("completed");
	expect(
		completeManagedLibraryDeletion(noPaths, releases, artists, {
			paths: {},
			metadataCommitSucceeded: false,
		}).status,
	).toBe("retry");
});
