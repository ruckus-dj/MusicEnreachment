import type {
	CatalogArtist,
	OriginalRelease,
	OriginalTrack,
} from "./catalogData";
import type { DeletionOperation } from "./DeletionRequest";
import { artistDeletionPlan } from "./ArtistDeletionRequest";
import {
	type ManagedPathObservation,
	type ManagedPublicationDeletionResult,
	resolveManagedPublicationDeletion,
} from "./managedPublicationDeletionModel";

/** The demo applies only the identities and publication paths confirmed by the operator. */
export function completeLibraryDeletion(
	operation: DeletionOperation,
	releases: OriginalRelease[],
	artists: CatalogArtist[],
): { releases: OriginalRelease[]; artists: CatalogArtist[] } | null {
	const trackIds = new Set(operation.libraryTrackIds ?? []);
	const releaseIds = new Set(operation.libraryReleaseIds ?? []);
	if (!trackIds.size && !releaseIds.size && !operation.libraryArtistId)
		return null;
	const tracks = releases.flatMap((release) => release.tracks);
	if ([...trackIds].some((id) => !tracks.some((track) => track.id === id)))
		return null;
	if (
		[...releaseIds].some((id) => !releases.some((release) => release.id === id))
	)
		return null;
	if (
		operation.libraryArtistId &&
		!artists.some((artist) => artist.id === operation.libraryArtistId)
	)
		return null;
	if (operation.libraryArtistId) {
		const current = artistDeletionPlan(
			operation.libraryArtistId,
			artists,
			releases,
		);
		if (
			!current ||
			JSON.stringify([...(current.libraryReleaseIds ?? [])].sort()) !==
				JSON.stringify([...releaseIds].sort()) ||
			JSON.stringify([...(current.libraryTrackIds ?? [])].sort()) !==
				JSON.stringify([...trackIds].sort())
		)
			return null;
	}
	// New dependencies require a fresh confirmation, not a widened deletion scope.
	if (
		releases.some(
			(release) =>
				releaseIds.has(release.id) &&
				release.tracks.some((track) => !trackIds.has(track.id)),
		)
	)
		return null;
	const actualPaths = tracks
		.filter((track) => trackIds.has(track.id))
		.flatMap((track) => (track.published ? [track.published.path] : []))
		.sort();
	const confirmedPaths = [...operation.paths].sort();
	if (
		JSON.stringify(actualPaths) !== JSON.stringify(confirmedPaths) ||
		actualPaths.length !== operation.publications
	)
		return null;
	if (operation.kind === "publication") {
		return {
			artists,
			releases: releases.map((release) => ({
				...release,
				tracks: release.tracks.map((track) =>
					trackIds.has(track.id)
						? {
								...track,
								published: null,
								metadataBaseline: undefined,
								metadataBaselineExtras: undefined,
							}
						: track,
				),
			})),
		};
	}
	return {
		artists: artists.filter(
			(artist) => artist.id !== operation.libraryArtistId,
		),
		releases: releases
			.filter((release) => !releaseIds.has(release.id))
			.map((release) => ({
				...release,
				tracks: release.tracks.filter((track) => !trackIds.has(track.id)),
			})),
	};
}

/** Explicit demo evidence for one confirmed managed path; no filesystem access. */
export interface ManagedPathDeletionEvidence {
	state: ManagedPathObservation;
	deleteSucceeded: boolean;
	metadataCommitSucceeded?: boolean;
}

/** Missing is conclusive only when the output root was confirmed available. */
export function managedDeletionObservation(
	observation: OriginalTrack["observedFile"],
): ManagedPathObservation {
	// The fixed fixture's registered publication is the only default observation.
	if (!observation) return "present";
	if (observation.rootAvailable === false) return "unknown";
	if (observation.state === "missing")
		return observation.rootAvailable === true ? "absent" : "unknown";
	if (["changed", "drift"].includes(observation.state ?? "")) return "replaced";
	if (["available", "unchanged"].includes(observation.state ?? ""))
		return "present";
	return "unknown";
}

/** A missing path entry is unknown and can never justify removing rows. */
export interface ManagedDeletionEvidence {
	paths: Partial<Record<string, ManagedPathDeletionEvidence>>;
	metadataCommitSucceeded: boolean;
}

export type ManagedLibraryDeletionOutcome =
	| {
			status: "completed";
			releases: OriginalRelease[];
			artists: CatalogArtist[];
	  }
	| {
			status: Exclude<ManagedPublicationDeletionResult, "completed" | "stale">;
			releases: OriginalRelease[];
			artists: CatalogArtist[];
			residualOperation: DeletionOperation;
	  }
	| {
			status: "stale";
	  };

/**
 * Validates the confirmed scope first, then interprets explicit per-path
 * evidence and the metadata commit outcome. Successfully committed publication
 * rows are applied independently; aggregate rows are released only when every
 * confirmed managed path and the final metadata commit have succeeded.
 */
export function completeManagedLibraryDeletion(
	operation: DeletionOperation,
	releases: OriginalRelease[],
	artists: CatalogArtist[],
	evidence: ManagedDeletionEvidence,
): ManagedLibraryDeletionOutcome {
	const next = completeLibraryDeletion(operation, releases, artists);
	if (!next) return { status: "stale" };
	const completedPaths = new Set<string>();
	const selectedTrackIds = new Set(operation.libraryTrackIds ?? []);
	let conflict = false;
	for (const path of operation.paths) {
		const owners = releases.flatMap((release) =>
			release.tracks.filter((track) => track.published?.path === path),
		);
		if (owners.some((track) => !selectedTrackIds.has(track.id))) {
			conflict = true;
			continue;
		}
		const observed = evidence.paths[path];
		const metadataCommitSucceeded =
			observed?.metadataCommitSucceeded ?? evidence.metadataCommitSucceeded;
		const resolution = resolveManagedPublicationDeletion(
			observed?.state ?? "unknown",
			observed?.deleteSucceeded ?? false,
			metadataCommitSucceeded,
		);
		if (resolution === "conflict") conflict = true;
		else if (resolution === "completed") completedPaths.add(path);
	}
	const partiallyUpdatedReleases = releases.map((release) => ({
		...release,
		tracks: release.tracks.map((track) =>
			track.published &&
			selectedTrackIds.has(track.id) &&
			completedPaths.has(track.published.path)
				? {
						...track,
						published: null,
						metadataBaseline: undefined,
						metadataBaselineExtras: undefined,
					}
				: track,
		),
	}));
	const remainingPaths = operation.paths.filter(
		(path) => !completedPaths.has(path),
	);
	const residualOperation = {
		...operation,
		paths: remainingPaths,
		publications: remainingPaths.length,
	};
	const finalMetadataCommitRequired =
		operation.kind !== "publication" || operation.paths.length === 0;
	const status: ManagedPublicationDeletionResult = conflict
		? "conflict"
		: remainingPaths.length ||
				(finalMetadataCommitRequired && !evidence.metadataCommitSucceeded)
			? "retry"
			: "completed";
	if (status !== "completed")
		return {
			status,
			releases: partiallyUpdatedReleases,
			artists,
			residualOperation,
		};
	return { status, releases: next.releases, artists: next.artists };
}
